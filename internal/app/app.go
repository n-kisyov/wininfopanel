// Package app wires the engine together and owns its lifecycle: the config
// store, the sensor sources, the overlay windows, and the web server.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/api"
	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/config/store"
	"github.com/n-kisyov/wininfopanel/internal/display"
	"github.com/n-kisyov/wininfopanel/internal/logging"
	"github.com/n-kisyov/wininfopanel/internal/paths"
	"github.com/n-kisyov/wininfopanel/internal/plugins"
	"github.com/n-kisyov/wininfopanel/internal/render/draw"
	"github.com/n-kisyov/wininfopanel/internal/render/font"
	"github.com/n-kisyov/wininfopanel/internal/render/media"
	"github.com/n-kisyov/wininfopanel/internal/sensor"
	"github.com/n-kisyov/wininfopanel/internal/sensor/hwinfo"
	"github.com/n-kisyov/wininfopanel/internal/sensor/native"
	"github.com/n-kisyov/wininfopanel/internal/sensor/registry"
)

// App is the running application.
type App struct {
	log *slog.Logger

	Store    *store.Store
	API      *api.Service
	Sensors  *registry.Registry
	Displays *display.Manager
	Fonts    *font.Cache
	Images   *media.Loader
	History  *draw.HistoryStore

	hwinfo  *hwinfo.Reader
	native  *native.Monitor
	Plugins *plugins.Manager

	mu      sync.Mutex
	running bool
	stop    context.CancelFunc
	workers sync.WaitGroup
}

// Options configures the application.
type Options struct {
	// DataDir overrides where configuration is stored. Empty uses the standard
	// per-user location.
	DataDir string
	// NoOverlays starts the engine without showing any desktop windows, which
	// is what headless tooling wants.
	NoOverlays bool
	// NoPlugins skips starting plugin processes.
	NoPlugins bool
}

// New builds the application without starting anything.
func New(opts Options) (*App, error) {
	dataDir := opts.DataDir
	if dataDir == "" {
		var err error
		if dataDir, err = paths.LocalRoot(); err != nil {
			return nil, fmt.Errorf("resolve data directory: %w", err)
		}
	}

	configStore, err := store.Open(dataDir)
	if err != nil {
		return nil, err
	}

	// Layouts and assets left behind by deleted profiles are cleared once at
	// startup rather than on every save.
	if err := configStore.PruneOrphans(); err != nil {
		return nil, err
	}

	settings := configStore.Settings()
	fonts := font.NewCache()

	a := &App{
		log:     logging.For("app"),
		Store:   configStore,
		Sensors: registry.New(),
		Fonts:   fonts,
		Images:  media.NewLoader(media.Options{AssetRoot: configStore.AssetsDir}),
		History: draw.NewHistoryStore(time.Duration(settings.TargetGraphUpdateRate)*time.Millisecond, draw.DefaultHistoryCapacity),
	}

	if settings.HWiNFOEnabled {
		a.hwinfo = hwinfo.New()
		a.hwinfo.SetInterval(time.Duration(settings.HWiNFOInterval) * time.Millisecond)
		a.Sensors.Register(sensor.SourceHWiNFO, a.hwinfo)
	}
	if settings.NativeEnabled {
		a.native = native.New(native.Options{
			StorageEnabled:  settings.NativeStorage,
			StorageInterval: time.Duration(settings.NativeStorageInterval) * time.Second,
		})
		a.Sensors.Register(sensor.SourceNative, a.native)
	}

	if !opts.NoPlugins {
		bundled, _ := paths.BundledPluginsDir()
		external, _ := paths.ExternalPluginsDir()
		configDir, _ := paths.PluginConfigDir()

		a.Plugins = plugins.NewManager(plugins.ManagerOptions{
			BundledDir:  bundled,
			ExternalDir: external,
			ConfigDir:   configDir,
		})
		a.Sensors.Register(sensor.SourcePlugin, a.Plugins)
	}

	if !opts.NoOverlays {
		a.Displays = display.NewManager(display.ManagerOptions{
			Layouts:           storeLayouts{store: configStore},
			Sensors:           a.Sensors,
			Fonts:             fonts,
			Images:            a.Images,
			History:           a.History,
			FrameRate:         settings.TargetFrameRate,
			GraphUpdateRate:   settings.TargetGraphUpdateRate,
			OnPositionChanged: a.persistPosition,
		})
	}

	a.API = api.New(api.Options{
		Store:             configStore,
		HWiNFO:            a.hwinfo,
		Native:            a.native,
		Plugins:           a.Plugins,
		Fonts:             fonts,
		OnProfilesChanged: a.syncDisplays,
	})

	return a, nil
}

// Start brings the engine up: sensor pollers first, then overlays.
func (a *App) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return nil
	}

	runCtx, cancel := context.WithCancel(ctx)
	a.stop = cancel
	a.running = true
	a.mu.Unlock()

	if a.hwinfo != nil {
		a.spawn(func() { a.hwinfo.Run(runCtx) })
	}
	if a.native != nil {
		a.spawn(func() { a.native.Run(runCtx) })
	}
	if a.Plugins != nil {
		if err := a.Plugins.Start(runCtx); err != nil {
			// A plugin subsystem failure must not stop the application: panels
			// bound to other sources still work.
			a.log.Error("could not start plugins", "error", err)
		}
	}

	if a.Displays != nil {
		if err := a.Displays.Start(runCtx); err != nil {
			return fmt.Errorf("start display manager: %w", err)
		}
		a.syncDisplays()
	}

	a.log.Info("engine started",
		"profiles", len(a.Store.Profiles()),
		"hwinfo", a.hwinfo != nil,
		"native", a.native != nil,
		"plugins", a.Plugins != nil)
	return nil
}

// spawn runs a worker and tracks it so Stop can wait for it.
func (a *App) spawn(work func()) {
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		work()
	}()
}

// Stop shuts the engine down and waits for its workers.
func (a *App) Stop() {
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return
	}
	a.running = false
	stop := a.stop
	a.mu.Unlock()

	if a.Displays != nil {
		a.Displays.Stop()
	}
	if a.Plugins != nil {
		a.Plugins.Stop()
	}
	if stop != nil {
		stop()
	}
	a.workers.Wait()

	if err := a.Store.SaveAll(); err != nil {
		a.log.Error("could not save configuration on shutdown", "error", err)
	}
	a.log.Info("engine stopped")
}

// syncDisplays reconciles the overlays against the current profiles.
func (a *App) syncDisplays() {
	if a.Displays == nil {
		return
	}
	a.Displays.Sync(a.Store.Profiles())
}

// persistPosition saves an overlay's new position after a drag.
func (a *App) persistPosition(profileID string, x, y int) {
	err := a.Store.UpdateProfile(profileID, func(p *model.Profile) {
		p.WindowX, p.WindowY = x, y
	})
	if err != nil {
		a.log.Warn("could not save overlay position", "profile", profileID, "error", err)
	}
}

// EnsureDefaultProfile creates a starter profile when none exists, so a first
// run has something to show rather than an empty screen.
func (a *App) EnsureDefaultProfile() (*model.Profile, error) {
	if profiles := a.Store.Profiles(); len(profiles) > 0 {
		return profiles[0], nil
	}

	profile, err := a.API.CreateProfile("Default", 800, 480)
	if err != nil {
		return nil, err
	}
	a.log.Info("created the default profile", "id", profile.ID)
	return profile, nil
}

// storeLayouts adapts the config store to the display manager's needs.
type storeLayouts struct{ store *store.Store }

func (s storeLayouts) Profile(profileID string) (*model.Profile, bool) {
	return s.store.Profile(profileID)
}

func (s storeLayouts) WithLayout(profileID string, fn func(model.ItemList)) error {
	return s.store.WithLayout(profileID, fn)
}
