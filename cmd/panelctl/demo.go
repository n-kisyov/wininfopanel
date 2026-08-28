package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/config/store"
	"github.com/n-kisyov/wininfopanel/internal/render/draw"
	"github.com/n-kisyov/wininfopanel/internal/render/font"
	"github.com/n-kisyov/wininfopanel/internal/render/graphics"
	"github.com/n-kisyov/wininfopanel/internal/render/media"
	"github.com/n-kisyov/wininfopanel/internal/sensor"
	"github.com/n-kisyov/wininfopanel/internal/sensor/native"
)

// runPanel renders a real profile layout end to end: display items resolved
// against live sensors, drawn through the same path the overlay windows and
// USB panels use.
func runPanel(ctx context.Context, args []string) error {
	fs := newFlagSet("panel")
	out := fs.String("out", "panel.png", "PNG file to write")
	profilePath := fs.String("profile", "", "profile layout JSON to render; omitted renders a built-in demo")
	dataDir := fs.String("data-dir", "", "render a stored profile from this data directory")
	profileID := fs.String("profile-id", "", "profile to render from -data-dir; omitted renders the first")
	width := fs.Int("width", 800, "canvas width when using the demo layout")
	height := fs.Int("height", 480, "canvas height when using the demo layout")
	live := fs.Bool("live", true, "resolve sensors from the native monitor")
	design := fs.Bool("design", false, "draw the design grid")
	verbose := fs.Bool("v", false, "log engine activity to stderr")

	if err := fs.Parse(args); err != nil {
		return err
	}
	setupConsoleLogging(*verbose)

	profile, items, images, err := loadRenderable(*dataDir, *profileID, *profilePath, *width, *height)
	if err != nil {
		return err
	}

	var resolver sensor.Resolver = sensor.NopResolver{}
	history := draw.NewHistoryStore(50*time.Millisecond, draw.DefaultHistoryCapacity)

	if *live {
		monitor := native.New(native.Options{
			Interval:       500 * time.Millisecond,
			StorageEnabled: true,
		})
		pollCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() { monitor.Run(pollCtx) }()

		if err := waitFor(ctx, 5*time.Second, monitor.Available); err != nil {
			return fmt.Errorf("native monitor produced no sensors: %w", err)
		}
		resolver = monitor

		// Graphs need history to plot, so collect a few samples before
		// rendering rather than drawing an empty chart.
		if err := collectHistory(ctx, items, resolver, history, 60, 60*time.Millisecond); err != nil {
			return err
		}
	}

	g := graphics.New(profile.Width, profile.Height, graphics.Options{
		Fonts:     font.NewCache(),
		FontScale: profile.FontScale,
	})

	draw.Render(g, items, draw.Frame{
		Profile:        profile,
		Sensors:        resolver,
		History:        history,
		Images:         images,
		Smoothing:      draw.NewSmoother(0), // no easing: a still frame has nowhere to ease from
		Design:         *design,
		GridSpacing:    20,
		GridColor:      color.NRGBA{128, 128, 128, 26},
		SelectionColor: color.NRGBA{0, 255, 0, 255},
	})

	return writePNG(*out, g)
}

// loadRenderable resolves what to render and how to find its images.
//
// A stored profile is loaded through the config store so relative image paths
// resolve against its asset directory; a bare layout file has no profile to
// resolve against, so only absolute paths work there.
func loadRenderable(dataDir, profileID, profilePath string, width, height int) (
	*model.Profile, model.ItemList, *media.Loader, error) {

	if dataDir == "" {
		profile, items, err := loadLayout(profilePath, width, height)
		return profile, items, media.NewLoader(media.Options{}), err
	}

	configStore, err := store.Open(dataDir)
	if err != nil {
		return nil, nil, nil, err
	}

	profiles := configStore.Profiles()
	if len(profiles) == 0 {
		return nil, nil, nil, fmt.Errorf("no profiles in %s", dataDir)
	}

	profile := profiles[0]
	if profileID != "" {
		found, ok := configStore.Profile(profileID)
		if !ok {
			return nil, nil, nil, fmt.Errorf("profile %s not found in %s", profileID, dataDir)
		}
		profile = found
	}

	items, err := configStore.Layout(profile.ID)
	if err != nil {
		return nil, nil, nil, err
	}

	return profile, items, media.NewLoader(media.Options{AssetRoot: configStore.AssetsDir}), nil
}

// loadLayout reads a profile layout from disk, or builds the demo one.
func loadLayout(path string, width, height int) (*model.Profile, model.ItemList, error) {
	if path == "" {
		profile, items := demoLayout(width, height)
		return profile, items, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read profile layout: %w", err)
	}

	var items model.ItemList
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, nil, fmt.Errorf("parse profile layout: %w", err)
	}

	profile := model.NewProfile("Loaded", width, height)
	return profile, items, nil
}

// collectHistory samples every graphed sensor repeatedly so a one-shot render
// still has a trace to draw.
func collectHistory(ctx context.Context, items model.ItemList, resolver sensor.Resolver,
	history *draw.HistoryStore, samples int, spacing time.Duration) error {

	var graphs []*model.GraphItem
	for _, item := range model.FlattenAll(items) {
		if graph, ok := item.(*model.GraphItem); ok {
			graphs = append(graphs, graph)
		}
	}
	if len(graphs) == 0 {
		return nil
	}

	for i := 0; i < samples; i++ {
		now := time.Now()
		for _, graph := range graphs {
			if value, ok := graph.Value(&model.EvalContext{Sensors: resolver}); ok {
				history.Sample(graph.Key, value, now)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(spacing):
		}
	}
	return nil
}

// demoLayout builds a panel exercising every display item type against real
// sensors, so one command shows the whole pipeline working.
func demoLayout(width, height int) (*model.Profile, model.ItemList) {
	profile := model.NewProfile("Demo", width, height)
	profile.BackgroundColor = "#FF14161C"
	profile.FontScale = 1.0

	const (
		labelColor = "#FF9AA4B8"
		valueColor = "#FFEAEAF0"
		accent     = "#FF5AC8FA"
		accentWarm = "#FFFF9F5A"
	)

	nativeKey := func(path string) sensor.Key {
		return sensor.Key{Source: sensor.SourceNative, Path: path}
	}

	items := model.ItemList{}

	// Title and clock.
	title := model.NewTextItem("SYSTEM MONITOR")
	title.X, title.Y = 24, 18
	title.FontSize, title.Bold, title.Color = 26, true, valueColor
	title.Glow = model.GlowSettings{Enabled: true, Radius: 8, Color: "#FF1E5AA0"}
	items = append(items, title)

	clock := model.NewClockItem()
	clock.X, clock.Y = width-24, 22
	clock.FontSize, clock.Color, clock.RightAlign = 22, accent, true
	items = append(items, clock)

	date := model.NewCalendarItem()
	date.Format = "Mon 02 Jan 2006"
	date.X, date.Y = width-24, 52
	date.FontSize, date.Color, date.RightAlign = 13, labelColor, true
	items = append(items, date)

	// Readouts with bars, one row per metric.
	rows := []struct {
		label string
		path  string
		unit  string
		color string
	}{
		{"CPU", "cpu/load", "%", accent},
		{"MEMORY", "memory/load", "%", accentWarm},
		{"DISK C:", "disk/C_/load", "%", "#FF7ED957"},
	}

	y := 100
	for _, r := range rows {
		label := model.NewTextItem(r.label)
		label.X, label.Y = 24, y
		label.FontSize, label.Color, label.Bold = 13, labelColor, true
		items = append(items, label)

		readout := model.NewSensorItem(r.label)
		readout.Key = nativeKey(r.path)
		readout.X, readout.Y = 250, y-6
		readout.FontSize, readout.Color, readout.RightAlign = 24, valueColor, true
		readout.OverridePrecision, readout.Precision = true, 0
		readout.Unit, readout.OverrideUnit = r.unit, true
		items = append(items, readout)

		bar := model.NewBarItem(r.label)
		bar.Key = nativeKey(r.path)
		bar.X, bar.Y = 24, y+26
		bar.Width, bar.Height = 226, 10
		bar.CornerRadius = 5
		bar.Color, bar.GradientColor = r.color, "#FF2A3140"
		bar.BackgroundColor, bar.Background = "#FF232833", true
		bar.Frame = false
		items = append(items, bar)

		y += 62
	}

	// Donut gauges.
	cpuDonut := model.NewDonutItem("CPU")
	cpuDonut.Key = nativeKey("cpu/load")
	cpuDonut.X, cpuDonut.Y = 300, 100
	cpuDonut.Width, cpuDonut.Height = 110, 110
	cpuDonut.Thickness, cpuDonut.Span = 14, 270
	cpuDonut.Color, cpuDonut.BackgroundColor = accent, "#FF232833"
	cpuDonut.Frame = false
	items = append(items, cpuDonut)

	cpuDonutLabel := model.NewSensorItem("CPU")
	cpuDonutLabel.Key = nativeKey("cpu/load")
	cpuDonutLabel.X, cpuDonutLabel.Y = 355, 138
	cpuDonutLabel.FontSize, cpuDonutLabel.Color = 22, valueColor
	cpuDonutLabel.CenterAlign = true
	cpuDonutLabel.OverridePrecision, cpuDonutLabel.Precision = true, 0
	cpuDonutLabel.Unit, cpuDonutLabel.OverrideUnit = "%", true
	items = append(items, cpuDonutLabel)

	memDonut := model.NewDonutItem("Memory")
	memDonut.Key = nativeKey("memory/load")
	memDonut.X, memDonut.Y = 430, 100
	memDonut.Width, memDonut.Height = 110, 110
	memDonut.Thickness, memDonut.Span = 14, 270
	memDonut.Color, memDonut.BackgroundColor = accentWarm, "#FF232833"
	memDonut.Frame = false
	items = append(items, memDonut)

	memDonutLabel := model.NewSensorItem("RAM")
	memDonutLabel.Key = nativeKey("memory/used")
	memDonutLabel.X, memDonutLabel.Y = 485, 138
	memDonutLabel.FontSize, memDonutLabel.Color = 20, valueColor
	memDonutLabel.CenterAlign = true
	memDonutLabel.OverridePrecision, memDonutLabel.Precision = true, 1
	memDonutLabel.Unit, memDonutLabel.OverrideUnit = "G", true
	items = append(items, memDonutLabel)

	// A live graph of CPU load.
	graph := model.NewGraphItem("CPU History", model.GraphLine)
	graph.Key = nativeKey("cpu/load")
	graph.X, graph.Y = 300, 240
	graph.Width, graph.Height = 240, 90
	graph.Step, graph.Thickness = 4, 2
	graph.Color, graph.FillColor = accent, "#405AC8FA"
	graph.BackgroundColor, graph.FrameColor = "#FF1A1E27", "#FF2E3646"
	items = append(items, graph)

	graphLabel := model.NewTextItem("CPU LOAD")
	graphLabel.X, graphLabel.Y = 300, 222
	graphLabel.FontSize, graphLabel.Color, graphLabel.Bold = 11, labelColor, true
	items = append(items, graphLabel)

	// Shapes, showing the primitive set.
	shapes := []model.ShapeType{
		model.ShapeHexagon, model.ShapeStar, model.ShapeTriangle, model.ShapePlus, model.ShapeArrow,
	}
	for i, shapeType := range shapes {
		shape := model.NewShapeItem(string(shapeType), shapeType)
		shape.X, shape.Y = 580+i*42, 100
		shape.Width, shape.Height = 34, 34
		shape.FillColor, shape.GradientColor = accent, "#FF1E3A5F"
		shape.Gradient, shape.GradientAngle = true, 90
		shape.Stroke = false
		items = append(items, shape)
	}

	// A grouped block of text readouts.
	group := model.NewGroupItem("Details")
	details := []struct {
		label string
		path  string
		unit  string
		prec  int
	}{
		{"Threads", "cpu/threads", "", 0},
		{"Processes", "system/processes", "", 0},
		{"RAM Free", "memory/available", " GB", 1},
		{"Swap", "memory/swap/load", "%", 0},
	}
	dy := 200
	for _, d := range details {
		label := model.NewTextItem(d.label)
		label.X, label.Y = 580, dy
		label.FontSize, label.Color = 12, labelColor
		group.Add(label)

		value := model.NewSensorItem(d.label)
		value.Key = nativeKey(d.path)
		value.X, value.Y = width-24, dy
		value.FontSize, value.Color, value.RightAlign = 12, valueColor, true
		value.OverridePrecision, value.Precision = true, d.prec
		value.Unit, value.OverrideUnit = d.unit, true
		group.Add(value)

		dy += 22
	}
	items = append(items, group)

	// Marquee text along the bottom, and the OS name.
	marquee := model.NewTextItem("wininfopanel - a Go port of InfoPanel - rendering live sensors through the same pipeline that drives desktop overlays and USB LCD panels")
	marquee.X, marquee.Y = 24, height-34
	marquee.FontSize, marquee.Color = 13, labelColor
	marquee.Width, marquee.Marquee = width-48, true
	marquee.MarqueeSpeed, marquee.MarqueeSpacing = 40, 60
	marquee.Wrap = false
	items = append(items, marquee)

	osName := model.NewSensorItem("OS")
	osName.Key = nativeKey("system/os")
	osName.X, osName.Y = 24, height-58
	osName.FontSize, osName.Color, osName.ShowUnit = 12, labelColor, false
	items = append(items, osName)

	cpuName := model.NewSensorItem("CPU")
	cpuName.Key = nativeKey("cpu/name")
	cpuName.X, cpuName.Y = width-24, height-58
	cpuName.FontSize, cpuName.Color, cpuName.RightAlign = 12, labelColor, true
	cpuName.ShowUnit = false
	items = append(items, cpuName)

	return profile, items
}
