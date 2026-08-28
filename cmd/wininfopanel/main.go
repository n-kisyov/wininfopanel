// Command wininfopanel renders hardware sensor data onto desktop overlays,
// USB LCD panels, and a built-in web interface.
//
// Windows 11 x64 only.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"

	"github.com/n-kisyov/wininfopanel/internal/api"
	"github.com/n-kisyov/wininfopanel/internal/app"
	"github.com/n-kisyov/wininfopanel/internal/logging"
	"github.com/n-kisyov/wininfopanel/internal/web"
)

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "wininfopanel: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		dataDir    = flag.String("data-dir", "", "override the configuration directory")
		noOverlays = flag.Bool("no-overlays", false, "run the engine without showing desktop windows")
		noWeb      = flag.Bool("no-web", false, "do not start the built-in web server")
		webPort    = flag.Int("web-port", 0, "override the web server port")
		console    = flag.Bool("console", false, "also log to stderr")
		debug      = flag.Bool("debug", false, "log at debug level")
	)
	flag.Parse()

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}

	logs, err := logging.Setup(logging.Options{
		Level:   level,
		Console: *console || *debug,
	})
	if err != nil {
		return fmt.Errorf("set up logging: %w", err)
	}
	defer logs.Close()

	api.Version = version
	log := logging.For("main")
	log.Info("starting", "version", version)

	// Ctrl+C shuts down cleanly rather than killing the process, so overlays
	// are torn down and configuration is written back.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	application, err := app.New(app.Options{
		DataDir:    *dataDir,
		NoOverlays: *noOverlays,
	})
	if err != nil {
		return err
	}

	if err := application.Start(ctx); err != nil {
		return err
	}
	defer application.Stop()

	// Seeding runs after the engine is up, not before: creating a profile
	// reconciles the overlays, and doing that while the display manager is
	// still stopped drops the new profile on the floor.
	if _, err := application.EnsureDefaultProfile(); err != nil {
		return err
	}

	settings := application.Store.Settings()
	webEnabled := settings.WebServer.Enabled && !*noWeb

	// The web server is opt-in in settings, but an explicit port on the
	// command line is a clear request to run it.
	if *webPort > 0 {
		settings.WebServer.Port = *webPort
		webEnabled = !*noWeb
	}

	if webEnabled {
		server := web.New(web.Options{
			API:         application.API,
			Sensors:     application.Sensors,
			Fonts:       application.Fonts,
			Images:      application.Images,
			History:     application.History,
			ListenIP:    settings.WebServer.ListenIP,
			Port:        settings.WebServer.Port,
			RefreshRate: settings.WebServer.RefreshRate,
		})

		go func() {
			if err := server.Run(ctx); err != nil {
				log.Error("web server stopped", "error", err)
			}
		}()
		fmt.Printf("web interface: http://%s/\n", server.Address())
	}

	fmt.Println("wininfopanel is running; press Ctrl+C to stop")
	<-ctx.Done()

	log.Info("shutting down")
	return nil
}
