package model

import (
	"testing"

	"github.com/n-kisyov/wininfopanel/internal/sensor"
)

// fixedResolver returns one reading for any key.
type fixedResolver struct {
	reading sensor.Reading
	ok      bool
}

func (f fixedResolver) Read(sensor.Key) (sensor.Reading, bool) { return f.reading, f.ok }

func ctxWith(r sensor.Reading) *EvalContext {
	return &EvalContext{Sensors: fixedResolver{reading: r, ok: true}}
}

// boundSensorItem returns a sensor item wired to a resolvable key.
func boundSensorItem(name string) *SensorItem {
	s := NewSensorItem(name)
	s.Key = sensor.Key{Source: sensor.SourceHWiNFO, ID: 1, Instance: 1, EntryID: 1}
	return s
}

func TestSensorItemUsesPerUnitPrecisionDefaults(t *testing.T) {
	// InfoPanel picks decimal places from the unit when precision is not
	// overridden: GB gets one, rates get two, volts get three.
	tests := []struct {
		unit  string
		value float64
		want  string
	}{
		{"GB", 15.678, "15.7GB"},
		{"MB/s", 1.23456, "1.23MB/s"},
		{"KB/s", 9.876, "9.88KB/s"},
		{"V", 1.23456, "1.235V"},
		{"°C", 65.7, "66°C"},
		{"%", 42.4, "42%"},
	}

	for _, tt := range tests {
		item := boundSensorItem("Test")
		got, _ := item.EvaluateTextAndColor(ctxWith(sensor.Reading{Now: tt.value, Unit: tt.unit}))
		if got != tt.want {
			t.Errorf("unit %q value %v: got %q, want %q", tt.unit, tt.value, got, tt.want)
		}
	}
}

func TestSensorItemOverridePrecisionFloorsAtZero(t *testing.T) {
	// With an explicit precision of zero InfoPanel floors rather than rounds.
	item := boundSensorItem("Test")
	item.OverridePrecision = true
	item.Precision = 0
	item.ShowUnit = false

	got, _ := item.EvaluateTextAndColor(ctxWith(sensor.Reading{Now: 65.9, Unit: "°C"}))
	if got != "65" {
		t.Errorf("got %q, want %q (explicit zero precision floors)", got, "65")
	}
}

func TestSensorItemOverridePrecisionDecimals(t *testing.T) {
	item := boundSensorItem("Test")
	item.OverridePrecision = true
	item.Precision = 2
	item.ShowUnit = false

	got, _ := item.EvaluateTextAndColor(ctxWith(sensor.Reading{Now: 65.456, Unit: "°C"}))
	if got != "65.46" {
		t.Errorf("got %q, want %q", got, "65.46")
	}
}

func TestSensorItemAppliesMultiplierAndOffset(t *testing.T) {
	item := boundSensorItem("Test")
	item.Multiplier = 2
	item.Offset = 10
	item.ShowUnit = false
	item.OverridePrecision = true

	got, _ := item.EvaluateTextAndColor(ctxWith(sensor.Reading{Now: 20}))
	if got != "50" { // 20*2 + 10
		t.Errorf("got %q, want %q", got, "50")
	}
}

func TestSensorItemDivideMode(t *testing.T) {
	item := boundSensorItem("Memory")
	item.Divide = true
	item.Multiplier = 1024
	item.ShowUnit = false
	item.OverridePrecision = true
	item.Precision = 1

	got, _ := item.EvaluateTextAndColor(ctxWith(sensor.Reading{Now: 8192}))
	if got != "8.0" {
		t.Errorf("got %q, want %q", got, "8.0")
	}
}

func TestSensorItemDivideByZeroFallsBackToOffsetOnly(t *testing.T) {
	// A zero divisor must not produce an infinity; InfoPanel skips the
	// division and applies only the offset.
	item := boundSensorItem("Test")
	item.Divide = true
	item.Multiplier = 0
	item.Offset = 5
	item.ShowUnit = false
	item.OverridePrecision = true

	got, _ := item.EvaluateTextAndColor(ctxWith(sensor.Reading{Now: 20}))
	if got != "25" {
		t.Errorf("got %q, want %q", got, "25")
	}
}

func TestSensorItemAbsoluteOffset(t *testing.T) {
	item := boundSensorItem("Delta")
	item.Multiplier = 1
	item.Offset = -100
	item.AbsoluteOffset = true
	item.ShowUnit = false
	item.OverridePrecision = true

	got, _ := item.EvaluateTextAndColor(ctxWith(sensor.Reading{Now: 40}))
	if got != "60" { // |40 - 100|
		t.Errorf("got %q, want %q", got, "60")
	}
}

func TestSensorItemThresholdColors(t *testing.T) {
	item := boundSensorItem("CPU")
	item.Color = "#FFFFFF"
	item.Threshold1 = Threshold{Value: 70, Color: "#FFA500"}
	item.Threshold2 = Threshold{Value: 90, Color: "#FF0000"}

	tests := []struct {
		value float64
		want  string
	}{
		{50, "#FFFFFF"},
		{70, "#FFA500"},
		{89, "#FFA500"},
		{90, "#FF0000"},
		{120, "#FF0000"},
	}
	for _, tt := range tests {
		_, color := item.EvaluateTextAndColor(ctxWith(sensor.Reading{Now: tt.value}))
		if color != tt.want {
			t.Errorf("value %v: color = %q, want %q", tt.value, color, tt.want)
		}
	}
}

func TestSensorItemShowNameAndUnitOverride(t *testing.T) {
	item := boundSensorItem("CPU")
	item.ShowName = true
	item.OverrideUnit = true
	item.Unit = " degrees"
	item.OverridePrecision = true

	got, _ := item.EvaluateTextAndColor(ctxWith(sensor.Reading{Now: 65, Unit: "°C"}))
	if got != "CPU 65 degrees" {
		t.Errorf("got %q, want %q", got, "CPU 65 degrees")
	}
}

func TestSensorItemThousandsSeparator(t *testing.T) {
	item := boundSensorItem("Memory")
	item.ShowThousandsSeparator = true
	item.ShowUnit = false
	item.OverridePrecision = true

	got, _ := item.EvaluateTextAndColor(ctxWith(sensor.Reading{Now: 1234567}))
	if got != "1,234,567" {
		t.Errorf("got %q, want %q", got, "1,234,567")
	}
}

func TestSensorItemStringValuedSensorBypassesFormatting(t *testing.T) {
	item := boundSensorItem("Track")
	item.ShowUnit = false

	got, _ := item.EvaluateTextAndColor(ctxWith(sensor.Reading{Text: "Bohemian Rhapsody"}))
	if got != "Bohemian Rhapsody" {
		t.Errorf("got %q, want the raw text", got)
	}
}

func TestSensorItemUnresolvedSensorRendersDash(t *testing.T) {
	item := boundSensorItem("Missing")
	item.Color = "#123456"

	ctx := &EvalContext{Sensors: fixedResolver{ok: false}}
	text, color := item.EvaluateTextAndColor(ctx)
	if text != "-" {
		t.Errorf("text = %q, want %q", text, "-")
	}
	if color != "#123456" {
		t.Errorf("color = %q, want the item's base color", color)
	}
}

func TestSensorItemUppercase(t *testing.T) {
	item := boundSensorItem("Track")
	item.Uppercase = true
	item.ShowUnit = false

	got, _ := item.EvaluateTextAndColor(ctxWith(sensor.Reading{Text: "quiet"}))
	if got != "QUIET" {
		t.Errorf("got %q, want %q", got, "QUIET")
	}
}

func TestApplyThousandsSeparatorPreservesDecimals(t *testing.T) {
	tests := []struct{ in, want string }{
		{"1234567", "1,234,567"},
		{"1234.56", "1,234.56"},
		{"999", "999"},
		{"1000", "1,000"},
		{"-1234567", "-1,234,567"},
		{"-1234.5", "-1,234.5"},
	}
	for _, tt := range tests {
		if got := applyThousandsSeparator(tt.in); got != tt.want {
			t.Errorf("applyThousandsSeparator(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizedClampsToRange(t *testing.T) {
	b := NewSensorBinding()
	b.Key = sensor.Key{Source: sensor.SourceHWiNFO, ID: 1}
	// Disable the absolute-value step so the clamp is what is under test;
	// TestNormalizedAppliesAbsoluteOffsetBeforeClamping covers the default.
	b.AbsoluteOffset = false

	tests := []struct {
		value float64
		want  float64
	}{
		{-50, 0},
		{0, 0},
		{50, 0.5},
		{100, 1},
		{150, 1},
	}
	for _, tt := range tests {
		got, ok := b.Normalized(ctxWith(sensor.Reading{Now: tt.value}), 0, 100)
		if !ok {
			t.Fatalf("value %v: binding did not resolve", tt.value)
		}
		if got != tt.want {
			t.Errorf("value %v: normalized = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestNormalizedAppliesAbsoluteOffsetBeforeClamping(t *testing.T) {
	// AbsoluteOffset defaults to true, matching InfoPanel: a negative reading
	// is reflected into the positive range rather than clamping to the floor.
	b := NewSensorBinding()
	b.Key = sensor.Key{Source: sensor.SourceHWiNFO, ID: 1}

	got, ok := b.Normalized(ctxWith(sensor.Reading{Now: -50}), 0, 100)
	if !ok {
		t.Fatal("binding did not resolve")
	}
	if got != 0.5 {
		t.Errorf("normalized = %v, want 0.5 (|-50| of 100)", got)
	}
}
