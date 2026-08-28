package app

import (
	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/sensor"
)

// The starter panel's palette.
//
// It is deliberately dark. A profile's own default background is InfoPanel's
// opaque white, which is right for a panel someone is designing but wrong for
// the one profile the application invents for itself: on a first run it
// renders as a blank white rectangle floating on the desktop, which reads as a
// failure to start rather than as an empty canvas.
const (
	starterBackground = "#FF1E1E1E"
	starterPrimary    = "#E8E8E8"
	starterMuted      = "#8A8A8A"
	starterAccent     = "#4FC3F7"
	starterTrack      = "#2A2A2A"
)

// Starter panel geometry, in canvas pixels on the 800x480 default profile.
const (
	starterLeft   = 48
	starterRight  = 752
	starterBarH   = 12
	starterRowGap = 86
)

// starterLayout returns the display items a freshly created default profile
// starts with: a clock, and the two sensors every machine has.
//
// Every reading is bound to the built-in monitor rather than HWiNFO, so the
// panel is populated on a machine with nothing else installed. Until the
// desktop shell exists this is also the only in-app sign that the sensor and
// render pipelines are alive, so it is worth it being visibly live -- hence
// the seconds on the clock and the bars, which move.
func starterLayout() model.ItemList {
	clock := model.NewClockItem()
	clock.X, clock.Y = starterLeft-4, 28
	clock.FontSize = 46
	clock.Color = starterAccent
	clock.Format = "15:04:05"

	date := model.NewCalendarItem()
	date.X, date.Y = starterLeft, 104
	date.FontSize = 15
	date.Color = starterMuted
	date.Format = "Monday, 02 January 2006"

	items := model.ItemList{clock, date}
	items = append(items, starterRow("CPU", "cpu/load", 176)...)
	items = append(items, starterRow("Memory", "memory/load", 176+starterRowGap)...)
	items = append(items,
		starterDetail("Processor", "cpu/name", 356),
		starterDetail("Clock", "cpu/clock", 388),
		starterDetail("Memory in use", "memory/used", 420),
	)
	return items
}

// starterRow builds one labelled readout: the label on the left, the value
// right-aligned on the same line, and a bar spanning the panel underneath.
func starterRow(label, path string, y int) model.ItemList {
	name := starterText(label, starterLeft, y, 15, starterMuted)
	name.Uppercase = true

	// The value sits a little above the label's baseline because it is set
	// much larger, and the two should read as one line.
	value := model.NewSensorItem(label)
	value.X, value.Y = starterRight, y-10
	value.FontSize = 22
	value.Color = starterPrimary
	value.RightAlign = true
	value.Key = nativeKey(path)
	value.SensorName = label

	bar := model.NewBarItem(label)
	bar.X, bar.Y = starterLeft, y+38
	bar.Width, bar.Height = starterRight-starterLeft, starterBarH
	bar.Color = starterAccent
	bar.BackgroundColor = starterTrack
	bar.Frame = false
	bar.Gradient = false
	bar.CornerRadius = starterBarH / 2
	bar.Key = nativeKey(path)
	bar.SensorName = label

	return model.ItemList{name, value, bar}
}

// starterDetail is a small muted readout for the panel's footer lines, where
// the sensor's name carries as much as its value does.
func starterDetail(label, path string, y int) *model.SensorItem {
	item := model.NewSensorItem(label)
	item.X, item.Y = starterLeft, y
	item.FontSize = 14
	item.Color = starterMuted
	item.ShowName = true
	item.Key = nativeKey(path)
	item.SensorName = label
	return item
}

// starterText returns a plain label. A text item draws its own name, which is
// what makes the string both the content and the item's identity in the UI.
func starterText(text string, x, y, size int, color string) *model.TextItem {
	item := model.NewTextItem(text)
	item.X, item.Y = x, y
	item.FontSize = size
	item.Color = color
	return item
}

// nativeKey addresses a sensor published by the built-in monitor.
func nativeKey(path string) sensor.Key {
	return sensor.Key{Source: sensor.SourceNative, Path: path}
}
