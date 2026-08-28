package importer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/config/store"
	"github.com/n-kisyov/wininfopanel/internal/sensor"
)

// The fixtures below are written the way .NET's XmlSerializer emits them:
// an ArrayOf... root, xsi:type attributes discriminating display item types,
// and lowercase-first boolean literals.

const profilesXML = `<?xml version="1.0" encoding="utf-8"?>
<ArrayOfProfile xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <Profile>
    <Guid>11111111-1111-1111-1111-111111111111</Guid>
    <Name>Main Panel</Name>
    <Width>800</Width>
    <Height>480</Height>
    <BackgroundColor>#FF101010</BackgroundColor>
    <Font>Segoe UI</Font>
    <FontSize>24</FontSize>
    <Color>#FFEEEEEE</Color>
    <FontScale>1.33</FontScale>
    <Active>true</Active>
    <Topmost>true</Topmost>
    <Drag>true</Drag>
    <Resize>false</Resize>
    <ShowFps>true</ShowFps>
    <WindowX>120</WindowX>
    <WindowY>340</WindowY>
    <TriggerProcessNames>game.exe</TriggerProcessNames>
    <StrictWindowMatching>true</StrictWindowMatching>
  </Profile>
  <Profile>
    <Guid>22222222-2222-2222-2222-222222222222</Guid>
    <Name>Second Panel</Name>
    <Width>480</Width>
    <Height>320</Height>
    <Active>false</Active>
  </Profile>
</ArrayOfProfile>`

const layoutXML = `<?xml version="1.0" encoding="utf-8"?>
<ArrayOfDisplayItem xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <DisplayItem xsi:type="TextDisplayItem">
    <Name>SYSTEM</Name><X>10</X><Y>20</Y>
    <Font>Consolas</Font><FontSize>18</FontSize><Bold>true</Bold>
    <Color>#FF00FF00</Color><RightAlign>true</RightAlign>
    <GlowEnabled>true</GlowEnabled><GlowRadius>6</GlowRadius><GlowColor>#FF0000FF</GlowColor>
  </DisplayItem>
  <DisplayItem xsi:type="ClockDisplayItem">
    <Name>Clock</Name><X>200</X><Y>20</Y>
    <Format>hh:mm:ss tt</Format>
  </DisplayItem>
  <DisplayItem xsi:type="CalendarDisplayItem">
    <Name>Date</Name><X>200</X><Y>50</Y>
    <Format>dd/MM/yyyy</Format>
  </DisplayItem>
  <DisplayItem xsi:type="SensorDisplayItem">
    <Name>CPU</Name><X>10</X><Y>60</Y>
    <SensorType>HwInfo</SensorType>
    <HwInfoRemoteIndex>-1</HwInfoRemoteIndex>
    <Id>100</Id><Instance>0</Instance><EntryId>7</EntryId>
    <ValueType>MAX</ValueType>
    <Threshold1>70</Threshold1><Threshold1Color>#FFFFA500</Threshold1Color>
    <Threshold2>90</Threshold2><Threshold2Color>#FFFF0000</Threshold2Color>
    <ShowUnit>true</ShowUnit><OverridePrecision>true</OverridePrecision><Precision>1</Precision>
    <MultiplicationModifier>1</MultiplicationModifier>
    <AbsoluteAddition>true</AbsoluteAddition>
  </DisplayItem>
  <DisplayItem xsi:type="GraphDisplayItem">
    <Name>History</Name><X>300</X><Y>100</Y>
    <Type>HISTOGRAM</Type>
    <Width>240</Width><Height>90</Height>
    <Thickness>3</Thickness><Step>5</Step>
    <Fill>true</Fill><FillColor>#405AC8FA</FillColor>
    <MinValue>0</MinValue><MaxValue>100</MaxValue>
    <SensorType>Libre</SensorType>
    <LibreSensorId>/amdcpu/0/load/0</LibreSensorId>
  </DisplayItem>
  <DisplayItem xsi:type="BarDisplayItem">
    <Name>Memory</Name><X>10</X><Y>140</Y>
    <Width>200</Width><Height>16</Height>
    <CornerRadius>8</CornerRadius>
    <Gradient>true</Gradient><GradientColor>#FF3B3B3B</GradientColor>
    <SensorType>Plugin</SensorType>
    <PluginSensorId>my-plugin/main/memory</PluginSensorId>
  </DisplayItem>
  <DisplayItem xsi:type="DonutDisplayItem">
    <Name>Load</Name><X>400</X><Y>200</Y>
    <Width>120</Width><Height>120</Height>
    <Thickness>16</Thickness><Span>270</Span>
  </DisplayItem>
  <DisplayItem xsi:type="ShapeDisplayItem">
    <Name>Badge</Name><X>600</X><Y>60</Y>
    <Type>Hexagon</Type>
    <Width>40</Width><Height>40</Height>
    <FillColor>#FF5AC8FA</FillColor>
    <StrokeWidth>2</StrokeWidth><StrokeColor>#FFFFFFFF</StrokeColor>
  </DisplayItem>
  <DisplayItem xsi:type="ImageDisplayItem">
    <Name>Logo</Name><X>700</X><Y>400</Y>
    <FilePath>logo.png</FilePath><RelativePath>true</RelativePath>
    <Scale>50</Scale><Cache>true</Cache>
  </DisplayItem>
  <DisplayItem xsi:type="GroupDisplayItem">
    <Name>Details</Name><X>500</X><Y>300</Y>
    <DisplayItems>
      <DisplayItem xsi:type="TextDisplayItem">
        <Name>Inner</Name><X>510</X><Y>310</Y>
      </DisplayItem>
    </DisplayItems>
  </DisplayItem>
  <DisplayItem xsi:type="SomeFutureDisplayItem">
    <Name>Unknown</Name><X>0</X><Y>0</Y>
  </DisplayItem>
</ArrayOfDisplayItem>`

// writeSource lays out a fake InfoPanel installation.
func writeSource(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "profiles.xml"), []byte(profilesXML), 0o644); err != nil {
		t.Fatal(err)
	}

	profilesDir := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	layoutPath := filepath.Join(profilesDir, "11111111-1111-1111-1111-111111111111.xml")
	if err := os.WriteFile(layoutPath, []byte(layoutXML), 0o644); err != nil {
		t.Fatal(err)
	}

	assetsDir := filepath.Join(dir, "assets", "11111111-1111-1111-1111-111111111111")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "logo.png"), []byte("not really a png"), 0o644); err != nil {
		t.Fatal(err)
	}

	return dir
}

func runImport(t *testing.T) (*store.Store, Result) {
	t.Helper()

	target, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open target store: %v", err)
	}

	imp, err := New(Options{SourceDir: writeSource(t), Target: target})
	if err != nil {
		t.Fatalf("new importer: %v", err)
	}
	if !imp.Available() {
		t.Fatal("Available reported no InfoPanel installation in the fixture")
	}

	result, err := imp.Import()
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	return target, result
}

func TestImportBringsAcrossProfiles(t *testing.T) {
	target, result := runImport(t)

	if result.Profiles != 2 {
		t.Errorf("imported %d profiles, want 2", result.Profiles)
	}

	profiles := target.Profiles()
	if len(profiles) != 2 {
		t.Fatalf("store holds %d profiles, want 2", len(profiles))
	}

	main := profiles[0]
	if main.Name != "Main Panel" {
		t.Errorf("Name = %q, want %q", main.Name, "Main Panel")
	}
	if main.Width != 800 || main.Height != 480 {
		t.Errorf("size = %dx%d, want 800x480", main.Width, main.Height)
	}
	if main.BackgroundColor != "#FF101010" {
		t.Errorf("BackgroundColor = %q", main.BackgroundColor)
	}
	if main.FontScale != 1.33 {
		t.Errorf("FontScale = %v, want 1.33", main.FontScale)
	}
	if !main.Active || !main.Topmost || !main.ShowFPS {
		t.Errorf("flags not carried across: %+v", main)
	}
	if main.WindowX != 120 || main.WindowY != 340 {
		t.Errorf("window position = %d,%d, want 120,340", main.WindowX, main.WindowY)
	}
	if main.TriggerProcessNames != "game.exe" || !main.StrictWindowMatching {
		t.Error("program-specific panel settings were not carried across")
	}
}

func TestImportAssignsFreshProfileIDs(t *testing.T) {
	// Importing must never collide with, or overwrite, work already here.
	target, _ := runImport(t)

	for _, profile := range target.Profiles() {
		if profile.ID == "11111111-1111-1111-1111-111111111111" {
			t.Error("an imported profile reused InfoPanel's own id")
		}
	}
}

func TestImportConvertsEveryKnownItemType(t *testing.T) {
	target, result := runImport(t)

	items, err := target.Layout(target.Profiles()[0].ID)
	if err != nil {
		t.Fatalf("read layout: %v", err)
	}

	want := []model.ItemKind{
		model.KindText, model.KindClock, model.KindCalendar, model.KindSensor,
		model.KindGraph, model.KindBar, model.KindDonut, model.KindShape,
		model.KindImage, model.KindGroup,
	}
	if len(items) != len(want) {
		t.Fatalf("imported %d items, want %d: %v", len(items), len(want), kindsOf(items))
	}
	for i, kind := range want {
		if items[i].Kind() != kind {
			t.Errorf("item %d is %q, want %q", i, items[i].Kind(), kind)
		}
	}

	// The unknown type must be reported rather than silently dropped.
	if result.Skipped["SomeFutureDisplayItem"] != 1 {
		t.Errorf("Skipped = %v, want one SomeFutureDisplayItem", result.Skipped)
	}
}

func TestImportedTextKeepsItsStyling(t *testing.T) {
	target, _ := runImport(t)
	items, _ := target.Layout(target.Profiles()[0].ID)

	text, ok := items[0].(*model.TextItem)
	if !ok {
		t.Fatalf("first item is %T, want a text item", items[0])
	}

	if text.Font != "Consolas" || text.FontSize != 18 || !text.Bold {
		t.Errorf("typography lost: %+v", text.TextStyle)
	}
	if text.Color != "#FF00FF00" {
		t.Errorf("Color = %q, want %q", text.Color, "#FF00FF00")
	}
	if !text.RightAlign {
		t.Error("RightAlign was not carried across")
	}
	if !text.Glow.Enabled || text.Glow.Radius != 6 || text.Glow.Color != "#FF0000FF" {
		t.Errorf("glow settings lost: %+v", text.Glow)
	}
	if text.X != 10 || text.Y != 20 {
		t.Errorf("position = %d,%d, want 10,20", text.X, text.Y)
	}
}

func TestImportedClockFormatIsTranslated(t *testing.T) {
	// InfoPanel stores a .NET format string; carrying it across untranslated
	// would render the letters themselves.
	target, _ := runImport(t)
	items, _ := target.Layout(target.Profiles()[0].ID)

	clock, ok := items[1].(*model.ClockItem)
	if !ok {
		t.Fatalf("second item is %T, want a clock", items[1])
	}
	if clock.Format != "03:04:05 PM" {
		t.Errorf("clock format = %q, want %q", clock.Format, "03:04:05 PM")
	}

	calendar, ok := items[2].(*model.CalendarItem)
	if !ok {
		t.Fatalf("third item is %T, want a calendar", items[2])
	}
	if calendar.Format != "02/01/2006" {
		t.Errorf("calendar format = %q, want %q", calendar.Format, "02/01/2006")
	}
}

func TestImportedSensorBindingAndFormatting(t *testing.T) {
	target, _ := runImport(t)
	items, _ := target.Layout(target.Profiles()[0].ID)

	item, ok := items[3].(*model.SensorItem)
	if !ok {
		t.Fatalf("fourth item is %T, want a sensor readout", items[3])
	}

	want := sensor.Key{Source: sensor.SourceHWiNFO, RemoteIndex: -1, ID: 100, Instance: 0, EntryID: 7}
	if item.Key != want {
		t.Errorf("Key = %+v, want %+v", item.Key, want)
	}
	if item.ValueType != sensor.ValueMax {
		t.Errorf("ValueType = %q, want %q", item.ValueType, sensor.ValueMax)
	}
	if item.Threshold1.Value != 70 || item.Threshold1.Color != "#FFFFA500" {
		t.Errorf("Threshold1 = %+v", item.Threshold1)
	}
	if item.Threshold2.Value != 90 || item.Threshold2.Color != "#FFFF0000" {
		t.Errorf("Threshold2 = %+v", item.Threshold2)
	}
	if !item.OverridePrecision || item.Precision != 1 {
		t.Errorf("precision settings lost: override=%v precision=%d",
			item.OverridePrecision, item.Precision)
	}
}

func TestLibreAndPluginBindingsMapToTheirSources(t *testing.T) {
	target, _ := runImport(t)
	items, _ := target.Layout(target.Profiles()[0].ID)

	graph, ok := items[4].(*model.GraphItem)
	if !ok {
		t.Fatalf("fifth item is %T, want a graph", items[4])
	}
	if graph.Key.Source != sensor.SourceNative {
		t.Errorf("a Libre binding mapped to %q, want the built-in monitor", graph.Key.Source)
	}
	if graph.Key.Path != "/amdcpu/0/load/0" {
		t.Errorf("Libre sensor path = %q", graph.Key.Path)
	}
	if graph.Type != model.GraphHistogram {
		t.Errorf("graph type = %q, want histogram", graph.Type)
	}
	if graph.Thickness != 3 || graph.Step != 5 {
		t.Errorf("graph geometry lost: thickness=%d step=%d", graph.Thickness, graph.Step)
	}

	bar, ok := items[5].(*model.BarItem)
	if !ok {
		t.Fatalf("sixth item is %T, want a bar", items[5])
	}
	if bar.Key.Source != sensor.SourcePlugin || bar.Key.Path != "my-plugin/main/memory" {
		t.Errorf("plugin binding = %+v", bar.Key)
	}
	if bar.CornerRadius != 8 || !bar.Gradient {
		t.Errorf("bar styling lost: radius=%d gradient=%v", bar.CornerRadius, bar.Gradient)
	}
}

func TestImportedShapeAndDonut(t *testing.T) {
	target, _ := runImport(t)
	items, _ := target.Layout(target.Profiles()[0].ID)

	donut, ok := items[6].(*model.DonutItem)
	if !ok {
		t.Fatalf("seventh item is %T, want a donut", items[6])
	}
	if donut.Thickness != 16 || donut.Span != 270 {
		t.Errorf("donut geometry = thickness %d span %d, want 16 and 270", donut.Thickness, donut.Span)
	}

	shape, ok := items[7].(*model.ShapeItem)
	if !ok {
		t.Fatalf("eighth item is %T, want a shape", items[7])
	}
	if shape.Type != model.ShapeHexagon {
		t.Errorf("shape type = %q, want hexagon", shape.Type)
	}
	if !shape.Stroke || shape.StrokeWidth != 2 {
		t.Errorf("shape stroke lost: %v width %d", shape.Stroke, shape.StrokeWidth)
	}
}

func TestImportedGroupKeepsItsChildren(t *testing.T) {
	target, _ := runImport(t)
	items, _ := target.Layout(target.Profiles()[0].ID)

	group, ok := items[9].(*model.GroupItem)
	if !ok {
		t.Fatalf("tenth item is %T, want a group", items[9])
	}
	if len(group.Items) != 1 {
		t.Fatalf("group holds %d children, want 1", len(group.Items))
	}
	if group.Items[0].Base().Name != "Inner" {
		t.Errorf("child name = %q, want %q", group.Items[0].Base().Name, "Inner")
	}
}

func TestImportCopiesAssets(t *testing.T) {
	// Assets are copied rather than referenced, so removing InfoPanel later
	// does not empty the imported panels.
	target, result := runImport(t)

	if result.Assets != 1 {
		t.Errorf("copied %d assets, want 1", result.Assets)
	}

	dir, err := target.AssetsDir(target.Profiles()[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "logo.png")); err != nil {
		t.Errorf("asset was not copied into the profile's directory: %v", err)
	}
}

func TestProfileWithNoLayoutFileImportsCleanly(t *testing.T) {
	// The second fixture profile has no layout file, which is a legitimate
	// empty panel rather than an error.
	target, result := runImport(t)

	if len(result.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", result.Warnings)
	}

	items, err := target.Layout(target.Profiles()[1].ID)
	if err != nil {
		t.Fatalf("read layout: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("a profile with no layout file imported %d items", len(items))
	}
}

func TestAvailableIsFalseWithoutAnInstallation(t *testing.T) {
	target, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	imp, err := New(Options{SourceDir: t.TempDir(), Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if imp.Available() {
		t.Error("Available reported an installation in an empty directory")
	}
}

func TestConvertDateFormat(t *testing.T) {
	tests := []struct{ dotnet, want string }{
		{"hh:mm:ss tt", "03:04:05 PM"},
		{"HH:mm:ss", "15:04:05"},
		{"dd/MM/yyyy", "02/01/2006"},
		{"yyyy-MM-dd", "2006-01-02"},
		{"dddd, dd MMMM yyyy", "Monday, 02 January 2006"},
		{"ddd MMM d", "Mon Jan 2"},
		{"h:mm tt", "3:04 PM"},
		{"HH:mm:ss.fff", "15:04:05.000"},

		// Quoted runs are literal text in .NET notation.
		{"HH'h'mm", "15h04"},
		{`HH\hmm`, "15h04"},

		// Separators and unknown characters pass through.
		{"HH:mm | dd", "15:04 | 02"},
		{"", ""},
	}

	for _, tt := range tests {
		if got := convertDateFormat(tt.dotnet); got != tt.want {
			t.Errorf("convertDateFormat(%q) = %q, want %q", tt.dotnet, got, tt.want)
		}
	}
}

func TestConvertDateFormatPrefersLongerSpecifiers(t *testing.T) {
	// "yyyy" must not be consumed as two "yy" tokens.
	if got := convertDateFormat("yyyy"); got != "2006" {
		t.Errorf("convertDateFormat(\"yyyy\") = %q, want %q", got, "2006")
	}
	if got := convertDateFormat("MMMM"); got != "January" {
		t.Errorf("convertDateFormat(\"MMMM\") = %q, want %q", got, "January")
	}
}

func kindsOf(items model.ItemList) []model.ItemKind {
	out := make([]model.ItemKind, len(items))
	for i, item := range items {
		out[i] = item.Kind()
	}
	return out
}
