// Package importer brings profiles across from an existing InfoPanel
// installation.
//
// The two applications store the same concepts in different formats -- .NET
// XML against JSON -- so this is a one-way translation, not a shared schema.
// It is deliberately permissive: a field it does not recognize is ignored and
// an item type it cannot map is reported and skipped, because a layout that
// imports with one element missing is far more useful than one that refuses to
// import at all.
package importer

import (
	"encoding/xml"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/config/store"
	"github.com/n-kisyov/wininfopanel/internal/logging"
	"github.com/n-kisyov/wininfopanel/internal/paths"
	"github.com/n-kisyov/wininfopanel/internal/sensor"
)

// Result reports what an import produced.
type Result struct {
	// SourceDir is the InfoPanel installation that was read.
	SourceDir string `json:"sourceDir"`

	// Profiles is how many profiles were imported.
	Profiles int `json:"profiles"`
	// Items is how many display items were imported across all profiles.
	Items int `json:"items"`
	// Assets is how many asset files were copied.
	Assets int `json:"assets"`

	// Skipped names item types that could not be mapped, with counts.
	Skipped map[string]int `json:"skipped,omitempty"`
	// Warnings describes problems that did not stop the import.
	Warnings []string `json:"warnings,omitempty"`
}

// Importer reads an InfoPanel installation into a wininfopanel store.
type Importer struct {
	log *slog.Logger

	// sourceDir is the InfoPanel data directory.
	sourceDir string
	// target receives the imported profiles.
	target *store.Store
}

// Options configures an import.
type Options struct {
	// SourceDir is InfoPanel's data directory. Empty uses the standard
	// %LOCALAPPDATA%\InfoPanel location.
	SourceDir string
	// Target is the store to import into.
	Target *store.Store
}

// New returns an importer.
func New(opts Options) (*Importer, error) {
	if opts.Target == nil {
		return nil, fmt.Errorf("an import target is required")
	}

	sourceDir := opts.SourceDir
	if sourceDir == "" {
		var err error
		if sourceDir, err = paths.InfoPanelLocalRoot(); err != nil {
			return nil, err
		}
	}

	return &Importer{
		log:       logging.For("config.importer"),
		sourceDir: sourceDir,
		target:    opts.Target,
	}, nil
}

// Available reports whether the source directory holds an InfoPanel
// installation worth importing.
func (i *Importer) Available() bool {
	_, err := os.Stat(filepath.Join(i.sourceDir, "profiles.xml"))
	return err == nil
}

// SourceDir returns the directory being imported from.
func (i *Importer) SourceDir() string { return i.sourceDir }

// Import reads every profile and its layout.
//
// Imported profiles get fresh IDs, so importing twice produces two independent
// copies rather than overwriting the first. That is the safer default: an
// import must never silently replace work the user has already done here.
func (i *Importer) Import() (Result, error) {
	result := Result{SourceDir: i.sourceDir, Skipped: make(map[string]int)}

	profilesPath := filepath.Join(i.sourceDir, "profiles.xml")
	data, err := os.ReadFile(profilesPath)
	if err != nil {
		return result, fmt.Errorf("read %s: %w", profilesPath, err)
	}

	var list xmlProfileList
	if err := xml.Unmarshal(data, &list); err != nil {
		return result, fmt.Errorf("parse %s: %w", profilesPath, err)
	}

	for _, source := range list.Profiles {
		if err := i.importProfile(source, &result); err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("profile %q: %v", source.Name, err))
			i.log.Warn("could not import profile", "name", source.Name, "error", err)
		}
	}

	if len(result.Skipped) == 0 {
		result.Skipped = nil
	}
	return result, nil
}

// importProfile converts one profile and its layout.
func (i *Importer) importProfile(source xmlProfile, result *Result) error {
	profile := convertProfile(source)

	if err := i.target.AddProfile(profile); err != nil {
		return err
	}
	result.Profiles++

	// A profile with no layout file is legitimate -- an empty panel -- so a
	// missing file is not an error.
	layoutPath := filepath.Join(i.sourceDir, "profiles", source.Guid+".xml")
	layoutData, err := os.ReadFile(layoutPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read layout: %w", err)
		}
		return nil
	}

	var list xmlItemList
	if err := xml.Unmarshal(layoutData, &list); err != nil {
		return fmt.Errorf("parse layout: %w", err)
	}

	items := i.convertItems(list.Items, result)
	if err := i.target.SetLayout(profile.ID, items); err != nil {
		return fmt.Errorf("save layout: %w", err)
	}
	result.Items += len(model.FlattenAll(items))

	if err := i.copyAssets(source.Guid, profile.ID, result); err != nil {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("profile %q assets: %v", source.Name, err))
	}
	return nil
}

// copyAssets duplicates a profile's images into the new asset directory.
//
// Assets are copied rather than referenced, so removing InfoPanel later does
// not empty the imported panels.
func (i *Importer) copyAssets(sourceGuid, targetID string, result *Result) error {
	sourceDir := filepath.Join(i.sourceDir, "assets", sourceGuid)

	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	targetDir, err := i.target.AssetsDir(targetID)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		data, err := os.ReadFile(filepath.Join(sourceDir, entry.Name()))
		if err != nil {
			return fmt.Errorf("read asset %s: %w", entry.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(targetDir, entry.Name()), data, 0o644); err != nil {
			return fmt.Errorf("write asset %s: %w", entry.Name(), err)
		}
		result.Assets++
	}
	return nil
}

// convertProfile maps an InfoPanel profile onto a wininfopanel one.
func convertProfile(source xmlProfile) *model.Profile {
	profile := model.NewProfile(source.Name, source.Width, source.Height)

	if source.BackgroundColor != "" {
		profile.BackgroundColor = source.BackgroundColor
	}
	if source.Color != "" {
		profile.Color = source.Color
	}
	if source.Font != "" {
		profile.Font = source.Font
	}
	if source.FontSize > 0 {
		profile.FontSize = source.FontSize
	}
	if source.FontScale > 0 {
		profile.FontScale = source.FontScale
	}
	if source.Width <= 0 {
		profile.Width = 800
	}
	if source.Height <= 0 {
		profile.Height = 480
	}

	profile.Active = source.Active
	profile.Topmost = source.Topmost
	profile.Drag = source.Drag
	profile.Resize = source.Resize
	profile.ShowFPS = source.ShowFps
	profile.Accelerated = source.OpenGL
	profile.WindowX = source.WindowX
	profile.WindowY = source.WindowY
	profile.TriggerProcessNames = source.TriggerProcessNames
	profile.StrictWindowMatching = source.StrictWindowMatching

	if source.TargetWindow != nil {
		profile.TargetWindow = &model.TargetWindow{
			X:          source.TargetWindow.X,
			Y:          source.TargetWindow.Y,
			Width:      source.TargetWindow.Width,
			Height:     source.TargetWindow.Height,
			DeviceName: source.TargetWindow.DeviceName,
		}
	}

	return profile
}

// convertItems maps a list of display items, recording any it cannot handle.
func (i *Importer) convertItems(source []xmlItem, result *Result) model.ItemList {
	items := make(model.ItemList, 0, len(source))

	for _, entry := range source {
		item := i.convertItem(entry, result)
		if item == nil {
			result.Skipped[entry.Type]++
			continue
		}
		items = append(items, item)
	}
	return items
}

// convertItem maps one display item, dispatching on its xsi:type.
func (i *Importer) convertItem(source xmlItem, result *Result) model.DisplayItem {
	// The attribute can carry a namespace prefix, e.g. "q1:TextDisplayItem".
	itemType := source.Type
	if colon := strings.LastIndex(itemType, ":"); colon >= 0 {
		itemType = itemType[colon+1:]
	}

	switch itemType {
	case "TextDisplayItem":
		return convertText(source)
	case "ClockDisplayItem":
		return convertClock(source)
	case "CalendarDisplayItem":
		return convertCalendar(source)
	case "SensorDisplayItem":
		return convertSensor(source)
	case "TableSensorDisplayItem":
		return convertTable(source)
	case "GraphDisplayItem":
		return convertGraph(source)
	case "BarDisplayItem":
		return convertBar(source)
	case "DonutDisplayItem":
		return convertDonut(source)
	case "GaugeDisplayItem":
		return convertGauge(source)
	case "ShapeDisplayItem":
		return convertShape(source)
	case "ImageDisplayItem":
		return convertImage(source)
	case "HttpImageDisplayItem":
		return convertHTTPImage(source)
	case "SensorImageDisplayItem":
		return convertSensorImage(source)
	case "GroupDisplayItem":
		group := model.NewGroupItem(source.Name)
		applyBase(&group.ItemBase, source)
		group.Items = i.convertItems(source.DisplayItems.Items, result)
		return group
	default:
		return nil
	}
}

// applyBase copies the fields every display item shares.
//
// InfoPanel does not persist item identity, so a fresh ID comes from the
// constructor and is left alone here.
func applyBase(base *model.ItemBase, source xmlItem) {
	base.Name = source.Name
	base.X = source.X
	base.Y = source.Y
	base.Rotation = source.Rotation
	base.Hidden = source.Hidden
	base.Locked = source.IsLocked
}

// applyTextStyle copies the typography shared by text-derived items.
func applyTextStyle(style *model.TextStyle, source xmlItem) {
	style.Font = source.Font
	style.FontStyle = source.FontStyle
	if source.FontSize > 0 {
		style.FontSize = source.FontSize
	}
	style.Bold = source.Bold
	style.Italic = source.Italic
	style.Underline = source.Underline
	style.Strikeout = source.Strikeout
	if source.Color != "" {
		style.Color = source.Color
	}

	style.RightAlign = source.RightAlign
	style.CenterAlign = source.CenterAlign
	style.Uppercase = source.Uppercase
	style.Wrap = source.Wrap
	style.Ellipsis = source.Ellipsis
	style.Width = source.Width
	style.Height = source.Height

	style.Marquee = source.Marquee
	style.MarqueeSpeed = source.MarqueeSpeed
	style.MarqueeSpacing = source.MarqueeSpacing

	style.Glow = model.GlowSettings{
		Enabled:   source.GlowEnabled,
		Radius:    source.GlowRadius,
		Color:     source.GlowColor,
		BlendMode: source.GlowBlendMode,
	}
}

// applyBinding maps a sensor reference across the two sources' naming.
func applyBinding(binding *model.SensorBinding, source xmlItem) {
	binding.SensorName = source.SensorName
	binding.ValueType = convertValueType(source.ValueType)

	switch strings.ToLower(source.SensorType) {
	case "libre":
		// InfoPanel's LibreHardwareMonitor sensors map onto this project's
		// built-in monitor. The identifiers differ, so a binding survives the
		// import but will not resolve until the sensor is re-picked.
		binding.Key = sensor.Key{Source: sensor.SourceNative, Path: source.LibreSensorId}
	case "plugin":
		binding.Key = sensor.Key{Source: sensor.SourcePlugin, Path: source.PluginSensorId}
	default:
		binding.Key = sensor.Key{
			Source:      sensor.SourceHWiNFO,
			RemoteIndex: source.HwInfoRemoteIndex,
			ID:          source.Id,
			Instance:    source.Instance,
			EntryID:     source.EntryId,
		}
	}

	// InfoPanel's transform names describe the arithmetic; the multiplier
	// defaults to 1 when absent so an unset transform is the identity rather
	// than flattening every reading to zero.
	binding.Multiplier = source.MultiplicationModifier
	if binding.Multiplier == 0 && !source.DivisionToggle {
		binding.Multiplier = 1
	}
	binding.Divide = source.DivisionToggle
	binding.Offset = source.AdditionModifier
	binding.AbsoluteOffset = source.AbsoluteAddition
}

func convertValueType(name string) sensor.ValueType {
	switch strings.ToUpper(name) {
	case "MIN":
		return sensor.ValueMin
	case "MAX":
		return sensor.ValueMax
	case "AVERAGE", "AVG":
		return sensor.ValueAvg
	default:
		return sensor.ValueNow
	}
}
