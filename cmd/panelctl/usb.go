package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/panels/beada"
	"github.com/n-kisyov/wininfopanel/internal/panels/usb"
	"github.com/n-kisyov/wininfopanel/internal/render/font"
	"github.com/n-kisyov/wininfopanel/internal/render/graphics"
)

func runUSB(ctx context.Context, args []string) error {
	fs := newFlagSet("usb")
	if err := fs.Parse(args); err != nil {
		return err
	}

	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: panelctl usb <list|panels|test> [flags]")
	}

	switch rest[0] {
	case "list":
		return usbList(rest[1:])
	case "panels":
		return usbPanels(rest[1:])
	case "test":
		return usbTest(ctx, rest[1:])
	default:
		return fmt.Errorf("unknown subcommand %q (want list, panels, or test)", rest[0])
	}
}

// usbList shows every device reachable through WinUSB.
func usbList(args []string) error {
	fs := newFlagSet("usb list")
	asJSON := fs.Bool("json", false, "emit JSON instead of a table")
	filter := fs.String("filter", "", "only show devices whose description or ID contains this text")

	if err := fs.Parse(args); err != nil {
		return err
	}

	devices, err := usb.Enumerate()
	if err != nil {
		return err
	}

	if *filter != "" {
		needle := strings.ToLower(*filter)
		kept := devices[:0]
		for _, device := range devices {
			if strings.Contains(strings.ToLower(device.Description), needle) ||
				strings.Contains(strings.ToLower(device.InstanceID), needle) {
				kept = append(kept, device)
			}
		}
		devices = kept
	}

	if *asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(devices)
	}

	if len(devices) == 0 {
		fmt.Println("No devices expose the WinUSB interface.")
		fmt.Println("Panels bound to a class driver do not appear here and cannot be driven this way.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "VID:PID\tDESCRIPTION\tREPORTED\tPORT\tSERIAL\tLOCATION")
	for _, device := range devices {
		fmt.Fprintf(w, "%04X:%04X\t%s\t%s\t%s\t%s\t%s\n",
			device.VendorID, device.ProductID,
			truncate(device.Description, 26),
			truncate(orDash(device.BusDescription), 20),
			orDash(device.PortName),
			orDash(device.Serial),
			truncate(device.Location, 22))
	}
	fmt.Fprintf(w, "\n%d device(s)\n", len(devices))

	// Windows binds every CDC-ACM device to usbser.sys and describes them all
	// as "USB Serial Device", so REPORTED -- the name the device gives for
	// itself -- is the only column that tells one serial panel from another.
	if serial := countSerial(devices); serial > 0 {
		fmt.Fprintf(w, "%d present a serial port; REPORTED is the device's own name for itself\n", serial)
	}
	return w.Flush()
}

// usbPanels identifies the attached LCD panels.
func usbPanels(args []string) error {
	fs := newFlagSet("usb panels")
	asJSON := fs.Bool("json", false, "emit JSON instead of a table")
	verbose := fs.Bool("v", false, "log probe activity to stderr")

	if err := fs.Parse(args); err != nil {
		return err
	}
	setupConsoleLogging(*verbose)

	panels, err := beada.Discover()
	if err != nil {
		return err
	}

	if *asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(panels)
	}

	if len(panels) == 0 {
		fmt.Println("No BeadaPanel was found.")
		fmt.Println()
		fmt.Println("If one is attached, check that:")
		fmt.Println("  - no other application is using it, since a panel accepts one owner at a time")
		fmt.Println("  - it is bound to the WinUSB driver rather than a class driver")
		fmt.Println("  - the cable carries data, not power only")
		reportSerialCandidates()
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "MODEL\tRESOLUTION\tPHYSICAL\tSERIAL\tFIRMWARE\tBRIGHTNESS")
	for _, panel := range panels {
		size := beada.Models[panel.Model]
		fmt.Fprintf(w, "%s\t%dx%d\t%dx%dmm\t%s\t%#04x\t%d/%d\n",
			panel.ModelName,
			panel.Width(), panel.Height(),
			size.WidthMM, size.HeightMM,
			orDash(panel.SerialNumber),
			panel.FirmwareVersion,
			panel.CurrentBrightness, panel.MaxBrightness)
	}
	fmt.Fprintf(w, "\n%d panel(s)\n", len(panels))
	return w.Flush()
}

// usbTest streams a test pattern to a panel.
func usbTest(ctx context.Context, args []string) error {
	fs := newFlagSet("usb test")
	serial := fs.String("serial", "", "panel serial number; omitted uses the only attached panel")
	duration := fs.Duration("for", 10*time.Second, "how long to stream")
	frameRate := fs.Int("fps", 15, "frames per second")
	brightness := fs.Int("brightness", 100, "backlight level, 0-100")
	verbose := fs.Bool("v", false, "log activity to stderr")

	if err := fs.Parse(args); err != nil {
		return err
	}
	setupConsoleLogging(*verbose)

	device, err := beada.OpenBySerial(*serial)
	if err != nil {
		return err
	}
	defer device.Close()

	info := device.Info()
	fmt.Printf("panel: %s  %dx%d  serial %s\n",
		info.ModelName, info.Width(), info.Height(), orDash(info.SerialNumber))

	if err := device.SetBrightness(*brightness); err != nil {
		return fmt.Errorf("set brightness: %w", err)
	}

	fonts := font.NewCache()
	surface := graphics.New(info.Width(), info.Height(),
		graphics.Options{Fonts: fonts, FontScale: 1})

	started := time.Now()
	streamCtx, cancel := context.WithTimeout(ctx, *duration)
	defer cancel()

	frames := 0
	err = device.Run(streamCtx, *frameRate, func() *image.RGBA {
		frames++
		drawTestPattern(surface, info, time.Since(started))
		return surface.Image()
	})

	fmt.Printf("streamed %d frames in %s\n", frames, time.Since(started).Round(time.Millisecond))
	return err
}

// drawTestPattern paints a frame that makes panel problems obvious: colour
// bars reveal channel ordering, the border reveals cropping, and the moving
// element reveals a frozen display.
func drawTestPattern(g *graphics.Graphics, info beada.PanelInfo, elapsed time.Duration) {
	width := float64(g.Width())
	height := float64(g.Height())

	g.Clear(color.NRGBA{0, 0, 0, 255})

	// Colour bars. Red and blue swapped on the panel means the channel order
	// is wrong.
	bars := []color.NRGBA{
		{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255},
		{255, 255, 0, 255}, {0, 255, 255, 255}, {255, 0, 255, 255},
		{255, 255, 255, 255},
	}
	barWidth := width / float64(len(bars))
	for i, c := range bars {
		g.FillRect(graphics.Rect{X: float64(i) * barWidth, Y: 0, W: barWidth, H: height * 0.25},
			0, graphics.SolidFill(c), graphics.Transform{})
	}

	// A one-pixel border: any edge missing means the frame is being cropped or
	// the declared geometry is wrong.
	g.StrokeRect(graphics.Rect{X: 0.5, Y: 0.5, W: width - 1, H: height - 1}, 0,
		graphics.SolidStroke(color.NRGBA{255, 255, 255, 255}, 1), graphics.Transform{})

	// A sweeping bar: a frozen panel shows it stopped.
	period := 3.0
	position := float64(elapsed.Milliseconds()%int64(period*1000)) / (period * 1000)
	g.FillRect(graphics.Rect{X: position * (width - 20), Y: height * 0.3, W: 20, H: height * 0.15},
		4, graphics.SolidFill(color.NRGBA{255, 200, 0, 255}), graphics.Transform{})

	g.DrawText(graphics.TextSpec{
		Text: "wininfopanel", Family: "Segoe UI", Size: int(height / 12), Bold: true,
	}, width/2, height*0.55, graphics.TextOptions{
		Color: color.NRGBA{255, 255, 255, 255},
		Align: graphics.AlignCenter,
	})

	g.DrawText(graphics.TextSpec{
		Text:   fmt.Sprintf("%s  %dx%d", info.ModelName, info.Width(), info.Height()),
		Family: "Segoe UI", Size: int(height / 20),
	}, width/2, height*0.72, graphics.TextOptions{
		Color: color.NRGBA{160, 200, 255, 255},
		Align: graphics.AlignCenter,
	})

	g.DrawText(graphics.TextSpec{
		Text:   fmt.Sprintf("%.1fs", elapsed.Seconds()),
		Family: "Segoe UI", Size: int(height / 24),
	}, width/2, height*0.85, graphics.TextOptions{
		Color: color.NRGBA{140, 140, 150, 255},
		Align: graphics.AlignCenter,
	})
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	if limit <= 3 {
		return s[:limit]
	}
	return s[:limit-3] + "..."
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// countSerial reports how many devices are reachable as a serial port.
func countSerial(devices []usb.DeviceInfo) int {
	n := 0
	for _, device := range devices {
		if device.IsSerial() {
			n++
		}
	}
	return n
}

// reportSerialCandidates lists the serial devices attached, so a panel this
// build has no driver for is still visible rather than silently absent.
//
// BeadaPanel is the only driver implemented; Turing, Thermalright, and
// Thermaltake panels all present a COM port instead of a WinUSB interface, and
// every one of them would otherwise show up here as nothing at all.
func reportSerialCandidates() {
	devices, err := usb.Enumerate()
	if err != nil {
		return
	}

	var serial []usb.DeviceInfo
	for _, device := range devices {
		if device.IsSerial() {
			serial = append(serial, device)
		}
	}
	if len(serial) == 0 {
		return
	}

	fmt.Println()
	fmt.Printf("%d serial device(s) are attached. No driver for these is implemented yet,\n", len(serial))
	fmt.Println("so none can be driven, but a panel would appear among them:")
	fmt.Println()

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "  PORT\tVID:PID\tREPORTED\tSERIAL")
	for _, device := range serial {
		fmt.Fprintf(w, "  %s\t%04X:%04X\t%s\t%s\n",
			device.PortName, device.VendorID, device.ProductID,
			orDash(device.BusDescription), orDash(device.Serial))
	}
	w.Flush()
}
