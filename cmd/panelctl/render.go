package main

import (
	"context"
	"fmt"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"

	"github.com/n-kisyov/wininfopanel/internal/render/font"
	"github.com/n-kisyov/wininfopanel/internal/render/graphics"
)

func runRender(_ context.Context, args []string) error {
	fs := newFlagSet("render")
	out := fs.String("out", "frame.png", "PNG file to write")
	width := fs.Int("width", 800, "surface width in pixels")
	height := fs.Int("height", 480, "surface height in pixels")
	scale := fs.Float64("font-scale", 1.0, "font size multiplier")
	verbose := fs.Bool("v", false, "log engine activity to stderr")

	if err := fs.Parse(args); err != nil {
		return err
	}
	setupConsoleLogging(*verbose)

	g := graphics.New(*width, *height, graphics.Options{
		Fonts:     font.NewCache(),
		FontScale: *scale,
	})

	drawShowcase(g)

	return writePNG(*out, g)
}

// drawShowcase exercises every graphics primitive, so the output is a visual
// regression check for the drawing layer as a whole.
func drawShowcase(g *graphics.Graphics) {
	w := float64(g.Width())

	g.Clear(color.NRGBA{18, 18, 22, 255})

	// Title, with the glow effect enabled.
	g.DrawText(graphics.TextSpec{
		Text: "wininfopanel", Family: "Segoe UI", Size: 34, Bold: true,
	}, w/2, 16, graphics.TextOptions{
		Color: color.NRGBA{235, 235, 240, 255},
		Align: graphics.AlignCenter,
		Glow: graphics.Glow{
			Enabled: true, Radius: 10, Color: color.NRGBA{80, 140, 255, 200},
		},
	})

	// Rectangles: plain, rounded, gradient, and outlined.
	g.FillRect(graphics.Rect{X: 30, Y: 90, W: 150, H: 60}, 0,
		graphics.SolidFill(color.NRGBA{70, 120, 200, 255}), graphics.Transform{})

	g.FillRect(graphics.Rect{X: 200, Y: 90, W: 150, H: 60}, 16,
		graphics.SolidFill(color.NRGBA{200, 90, 70, 255}), graphics.Transform{})

	g.FillRect(graphics.Rect{X: 370, Y: 90, W: 150, H: 60}, 8, graphics.Fill{
		Enabled: true, Gradient: true, Kind: graphics.GradientLinear, Angle: 0,
		Color:         color.NRGBA{60, 200, 140, 255},
		GradientColor: color.NRGBA{30, 60, 120, 255},
	}, graphics.Transform{})

	g.StrokeRect(graphics.Rect{X: 540, Y: 90, W: 150, H: 60}, 12,
		graphics.SolidStroke(color.NRGBA{220, 200, 100, 255}, 3), graphics.Transform{})

	// A rotated rectangle, verifying transforms turn about the shape's center.
	g.FillRect(graphics.Rect{X: 30, Y: 190, W: 120, H: 50}, 6,
		graphics.SolidFill(color.NRGBA{150, 100, 220, 255}),
		graphics.Transform{Rotation: 20})

	// Ellipse with a radial gradient.
	g.FillEllipse(graphics.Rect{X: 190, Y: 175, W: 90, H: 90}, graphics.Fill{
		Enabled: true, Gradient: true, Kind: graphics.GradientRadial,
		Color:         color.NRGBA{240, 220, 120, 255},
		GradientColor: color.NRGBA{120, 60, 20, 255},
	}, graphics.Transform{})

	// Donuts: a full ring and a 270-degree gauge.
	g.Donut(graphics.Point{X: 340, Y: 220}, 45, 12, 0.72, 360,
		color.NRGBA{80, 200, 255, 255}, color.NRGBA{45, 50, 60, 255},
		graphics.Stroke{}, graphics.Transform{})

	g.Donut(graphics.Point{X: 460, Y: 220}, 45, 12, 0.45, 270,
		color.NRGBA{255, 140, 80, 255}, color.NRGBA{45, 50, 60, 255},
		graphics.Stroke{}, graphics.Transform{})

	// Polygons: a triangle and a five-pointed star.
	g.Polygon([]graphics.Point{{X: 560, Y: 265}, {X: 620, Y: 175}, {X: 680, Y: 265}},
		graphics.SolidFill(color.NRGBA{90, 200, 120, 255}),
		graphics.SolidStroke(color.NRGBA{240, 240, 240, 255}, 2),
		graphics.Transform{})

	g.Polygon(starPoints(740, 220, 45, 20, 5),
		graphics.SolidFill(color.NRGBA{240, 200, 60, 255}),
		graphics.Stroke{}, graphics.Transform{})

	// A polyline, standing in for a graph trace.
	g.Polyline([]graphics.Point{
		{X: 30, Y: 340}, {X: 90, Y: 300}, {X: 150, Y: 355}, {X: 210, Y: 310},
		{X: 270, Y: 330}, {X: 330, Y: 290}, {X: 390, Y: 345},
	}, graphics.SolidStroke(color.NRGBA{120, 220, 255, 255}, 2), graphics.Transform{})

	// Text: styles, alignment, and decorations.
	labels := []struct {
		text string
		spec graphics.TextSpec
	}{
		{"Regular 18", graphics.TextSpec{Family: "Segoe UI", Size: 18}},
		{"Bold 18", graphics.TextSpec{Family: "Segoe UI", Size: 18, Bold: true}},
		{"Italic 18", graphics.TextSpec{Family: "Segoe UI", Size: 18, Italic: true}},
		{"Underline 18", graphics.TextSpec{Family: "Segoe UI", Size: 18, Underline: true}},
		{"Strikeout 18", graphics.TextSpec{Family: "Segoe UI", Size: 18, Strikeout: true}},
	}
	for i, label := range labels {
		spec := label.spec
		spec.Text = label.text
		g.DrawText(spec, 430, float64(280+i*26), graphics.TextOptions{
			Color: color.NRGBA{220, 220, 225, 255},
		})
	}

	// Wrapped text inside a bounded box, with the box drawn to show the fit.
	box := graphics.Rect{X: 30, Y: 380, W: 320, H: 80}
	g.StrokeRect(box, 0, graphics.SolidStroke(color.NRGBA{70, 70, 80, 255}, 1),
		graphics.Transform{})
	g.DrawText(graphics.TextSpec{
		Text:   "Wrapped text inside a bounded box, truncated with an ellipsis once it runs past the available height.",
		Family: "Segoe UI", Size: 15, Wrap: true, Ellipsis: true,
		Width: int(box.W), Height: int(box.H),
	}, box.X+6, box.Y+6, graphics.TextOptions{Color: color.NRGBA{190, 190, 200, 255}})

	// Right-aligned text, the layout sensor readouts use.
	g.DrawText(graphics.TextSpec{Text: "right aligned", Family: "Segoe UI", Size: 16},
		w-30, 430, graphics.TextOptions{
			Color: color.NRGBA{160, 200, 160, 255},
			Align: graphics.AlignRight,
		})
}

// starPoints returns the vertices of a star polygon.
func starPoints(cx, cy, outer, inner float64, points int) []graphics.Point {
	const quarterTurn = -math.Pi / 2 // start at the top

	out := make([]graphics.Point, 0, points*2)
	step := math.Pi / float64(points)
	for i := 0; i < points*2; i++ {
		radius := outer
		if i%2 == 1 {
			radius = inner
		}
		angle := quarterTurn + float64(i)*step
		out = append(out, graphics.Point{
			X: cx + radius*math.Cos(angle),
			Y: cy + radius*math.Sin(angle),
		})
	}
	return out
}

func writePNG(path string, g *graphics.Graphics) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create output directory %s: %w", dir, err)
		}
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()

	if err := png.Encode(f, g.Image()); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}

	abs, _ := filepath.Abs(path)
	fmt.Printf("wrote %s (%dx%d)\n", abs, g.Width(), g.Height())
	return nil
}
