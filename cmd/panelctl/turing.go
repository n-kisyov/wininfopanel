package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"image"
	"image/draw"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/panels/turing"
	renderdraw "github.com/n-kisyov/wininfopanel/internal/render/draw"
	"github.com/n-kisyov/wininfopanel/internal/render/font"
	"github.com/n-kisyov/wininfopanel/internal/render/graphics"
	"github.com/n-kisyov/wininfopanel/internal/sensor"
	"github.com/n-kisyov/wininfopanel/internal/sensor/native"
)

func runTuring(ctx context.Context, args []string) error {
	fs := newFlagSet("turing")
	if err := fs.Parse(args); err != nil {
		return err
	}

	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: panelctl turing <list|info|probe|brightness|clear|show> [flags]")
	}

	switch rest[0] {
	case "list":
		return turingList()
	case "info":
		return turingInfo(rest[1:])
	case "brightness":
		return turingBrightness(rest[1:])
	case "clear":
		return turingClear(rest[1:])
	case "show":
		return turingShow(ctx, rest[1:])
	case "probe":
		return turingProbe(rest[1:])
	default:
		return fmt.Errorf("unknown subcommand %q (want list, info, probe, brightness, clear, or show)", rest[0])
	}
}

// turingList reports the panels found without opening any of them, so a device
// already owned by another application still shows up.
func turingList() error {
	candidates, err := turing.Discover()
	if err != nil {
		return err
	}

	if len(candidates) == 0 {
		fmt.Println("No Turing panel was found.")
		fmt.Println("Run 'panelctl usb list' to see every attached serial device.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "PORT\tVID:PID\tREPORTED\tSERIAL")
	for _, c := range candidates {
		fmt.Fprintf(w, "%s\t%04X:%04X\t%s\t%s\n",
			c.PortName, c.VendorID, c.ProductID, c.BusDescription, orDash(c.Serial))
	}
	fmt.Fprintf(w, "\n%d panel(s)\n", len(candidates))
	return w.Flush()
}

// openTuring connects to the panel named by the flags, or the only one found.
func openTuring(port string, verbose bool) (*turing.Device, error) {
	setupConsoleLogging(verbose)
	return turing.OpenFirst(turing.Options{PortName: port})
}

func turingInfo(args []string) error {
	fs := newFlagSet("turing info")
	port := fs.String("port", "", "serial port to use; empty picks the first panel found")
	verbose := fs.Bool("v", false, "log activity to stderr")

	if err := fs.Parse(args); err != nil {
		return err
	}

	device, err := openTuring(*port, *verbose)
	if err != nil {
		return err
	}
	defer device.Close()

	fmt.Printf("port:       %s\n", device.Port())
	fmt.Printf("firmware:   %s\n", device.Firmware())
	fmt.Printf("resolution: %dx%d\n", turing.Width, turing.Height)
	fmt.Printf("frame:      %d bytes\n", turing.FrameSize)
	return nil
}

func turingBrightness(args []string) error {
	fs := newFlagSet("turing brightness")
	port := fs.String("port", "", "serial port to use; empty picks the first panel found")
	verbose := fs.Bool("v", false, "log activity to stderr")

	level, rest := splitLeadingArg(args)
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if level == "" {
		level = fs.Arg(0)
	}

	percent, err := strconv.Atoi(level)
	if err != nil {
		return fmt.Errorf("usage: panelctl turing brightness <0-100> [flags]")
	}

	device, err := openTuring(*port, *verbose)
	if err != nil {
		return err
	}
	defer device.Close()

	if err := device.SetBrightness(percent); err != nil {
		return err
	}
	fmt.Printf("brightness set to %d%%\n", percent)
	return nil
}

func turingClear(args []string) error {
	fs := newFlagSet("turing clear")
	port := fs.String("port", "", "serial port to use; empty picks the first panel found")
	colorText := fs.String("color", "#000000", "fill colour")
	verbose := fs.Bool("v", false, "log activity to stderr")

	if err := fs.Parse(args); err != nil {
		return err
	}

	fill, err := graphics.ParseColor(*colorText)
	if err != nil {
		return err
	}

	device, err := openTuring(*port, *verbose)
	if err != nil {
		return err
	}
	defer device.Close()

	if err := device.Clear(fill.R, fill.G, fill.B); err != nil {
		return err
	}
	fmt.Printf("cleared to %s\n", graphics.FormatColor(fill))
	return nil
}

// turingShow renders a layout to the panel, once or on a loop.
func turingShow(ctx context.Context, args []string) error {
	fs := newFlagSet("turing show")
	port := fs.String("port", "", "serial port to use; empty picks the first panel found")
	profilePath := fs.String("profile", "", "profile layout JSON to render; omitted renders the demo")
	dataDir := fs.String("data-dir", "", "render a stored profile from this data directory")
	profileID := fs.String("profile-id", "", "profile to render from -data-dir; omitted renders the first")
	duration := fs.Duration("for", 0, "keep refreshing for this long; 0 sends a single frame")
	frameRate := fs.Int("fps", 2, "frames per second while refreshing")
	verbose := fs.Bool("v", false, "log activity to stderr")

	if err := fs.Parse(args); err != nil {
		return err
	}

	profile, items, images, err := loadRenderable(*dataDir, *profileID, *profilePath, turing.Width, turing.Height)
	if err != nil {
		return err
	}

	// Live sensors, so the panel shows something that moves rather than a
	// frame of dashes.
	monitor := native.New(native.Options{Interval: 500 * time.Millisecond, StorageEnabled: true})
	monitorCtx, stopMonitor := context.WithCancel(ctx)
	defer stopMonitor()
	go func() { monitor.Run(monitorCtx) }()

	if err := waitFor(ctx, 5*time.Second, monitor.Available); err != nil {
		return fmt.Errorf("native monitor produced no sensors: %w", err)
	}
	var resolver sensor.Resolver = monitor

	device, err := openTuring(*port, *verbose)
	if err != nil {
		return err
	}
	defer device.Close()

	fmt.Printf("rendering %q to %s (%s)\n", profile.Name, device.Port(), device.Firmware())

	surface := graphics.New(profile.Width, profile.Height, graphics.Options{
		Fonts:     font.NewCache(),
		FontScale: profile.FontScale,
	})
	// The panel takes one exact size, so a layout of any other shape is
	// composited onto a panel-sized canvas rather than refused.
	canvas := image.NewRGBA(image.Rect(0, 0, turing.Width, turing.Height))
	frame := make([]byte, turing.FrameSize)

	send := func() error {
		renderdraw.Render(surface, items, renderdraw.Frame{
			Profile: profile,
			Sensors: resolver,
			Images:  images,
			Now:     time.Now(),
		})

		draw.Draw(canvas, canvas.Bounds(), image.NewUniform(image.Black), image.Point{}, draw.Src)
		draw.Draw(canvas, surface.Image().Bounds(), surface.Image(), image.Point{}, draw.Over)

		turing.EncodeRGBA(frame, canvas)
		return device.DisplayFrame(frame)
	}

	if *duration <= 0 {
		if err := send(); err != nil {
			return err
		}
		fmt.Println("sent one frame")
		return nil
	}

	interval := time.Second / time.Duration(max(*frameRate, 1))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	deadline := time.After(*duration)
	sent := 0
	for {
		if err := send(); err != nil {
			return err
		}
		sent++

		select {
		case <-ctx.Done():
			fmt.Printf("\nstopped after %d frame(s)\n", sent)
			return nil
		case <-deadline:
			fmt.Printf("sent %d frame(s)\n", sent)
			return nil
		case <-ticker.C:
		}
	}
}

// turingProbe sends the handshake and dumps the raw reply, for telling a panel
// that says nothing from one that says something unexpected.
func turingProbe(args []string) error {
	fs := newFlagSet("turing probe")
	port := fs.String("port", "", "serial port to probe; empty picks the first panel found")
	wait := fs.Duration("wait", 3*time.Second, "how long to listen for a reply")

	if err := fs.Parse(args); err != nil {
		return err
	}

	name := *port
	if name == "" {
		candidates, err := turing.Discover()
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			return fmt.Errorf("no Turing panel found; pass -port")
		}
		name = candidates[0].PortName
	}

	fmt.Printf("probing %s for %s\n", name, *wait)

	received, err := turing.Probe(name, *wait)
	if err != nil {
		return err
	}

	if len(received) == 0 {
		fmt.Println("the panel sent nothing back")
		return nil
	}

	fmt.Printf("%d byte(s):\n%s\n", len(received), hex.Dump(received))
	return nil
}
