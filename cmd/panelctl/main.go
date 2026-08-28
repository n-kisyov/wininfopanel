// Command panelctl is the development CLI for wininfopanel.
//
// It drives the engine headlessly -- dumping sensors, rendering a profile to a
// PNG, probing USB panels -- so each subsystem can be exercised long before,
// and independently of, the desktop UI.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
)

// command is one panelctl subcommand.
type command struct {
	name    string
	summary string
	run     func(ctx context.Context, args []string) error
}

func commands() []command {
	return []command{
		{"sensors", "list sensors from a data source", runSensors},
		{"render", "render a graphics test frame to a PNG", runRender},
		{"panel", "render a profile layout to a PNG using live sensors", runPanel},
		{"overlay", "show a live desktop overlay window", runOverlay},
		{"profiles", "list profiles and choose which ones show an overlay", withoutContext(runProfiles)},
		{"import", "import profiles from an existing InfoPanel installation", runImport},
		{"usb", "list USB devices, identify LCD panels, or stream a test pattern", runUSB},
		{"version", "print build information", runVersion},
	}
}

func main() {
	// Ctrl+C cancels in-flight work rather than killing the process outright,
	// so mapped sections and device handles are released.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "panelctl: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("no command given")
	}

	name := args[0]
	if name == "-h" || name == "--help" || name == "help" {
		usage()
		return nil
	}

	for _, cmd := range commands() {
		if cmd.name == name {
			return cmd.run(ctx, args[1:])
		}
	}

	usage()
	return fmt.Errorf("unknown command %q", name)
}

func usage() {
	var b strings.Builder
	b.WriteString("panelctl - wininfopanel development CLI\n\n")
	b.WriteString("usage: panelctl <command> [flags]\n\ncommands:\n")
	for _, cmd := range commands() {
		fmt.Fprintf(&b, "  %-10s %s\n", cmd.name, cmd.summary)
	}
	b.WriteString("\nrun 'panelctl <command> -h' for a command's flags\n")
	fmt.Fprint(os.Stderr, b.String())
}

// withoutContext adapts a subcommand that has no long-running work to do.
func withoutContext(run func(args []string) error) func(context.Context, []string) error {
	return func(_ context.Context, args []string) error { return run(args) }
}

// newFlagSet returns a flag set that prints the command name in errors.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("panelctl "+name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

// setupConsoleLogging routes engine logs to stderr at the requested level,
// keeping stdout clean for machine-readable output.
func setupConsoleLogging(verbose bool) {
	level := slog.LevelWarn
	if verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
}
