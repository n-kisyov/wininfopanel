package model

import (
	"math"
	"strconv"
	"strings"

	"github.com/n-kisyov/wininfopanel/internal/sensor"
)

// SensorBinding ties a display item to one sensor and describes how to turn
// that sensor's raw value into a number for display.
type SensorBinding struct {
	Key sensor.Key `json:"key"`

	// SensorName is the sensor's label captured when the binding was made,
	// shown in the UI so a layout stays readable when the sensor is
	// unavailable. It is not called Name: every item embedding a binding also
	// embeds ItemBase, and two Name fields at the same depth would make the
	// selector ambiguous.
	SensorName string `json:"sensorName,omitempty"`

	// ValueType selects current, minimum, maximum, or average.
	ValueType sensor.ValueType `json:"valueType,omitempty"`

	// Multiplier scales the reading. When Divide is set it becomes a divisor
	// instead, which is how InfoPanel expresses unit conversions such as
	// bytes to gigabytes.
	Multiplier float64 `json:"multiplier"`
	// Divide reinterprets Multiplier as a divisor.
	Divide bool `json:"divide,omitempty"`
	// Offset is added after scaling.
	Offset float64 `json:"offset,omitempty"`
	// AbsoluteOffset takes the absolute value of the final result.
	AbsoluteOffset bool `json:"absoluteOffset,omitempty"`
}

// NewSensorBinding returns a binding with the identity transform applied.
func NewSensorBinding() SensorBinding {
	return SensorBinding{ValueType: sensor.ValueNow, Multiplier: 1, AbsoluteOffset: true}
}

// Binding implements SensorBound.
func (b *SensorBinding) Binding() *SensorBinding { return b }

// IsBound reports whether the binding points at a sensor.
func (b *SensorBinding) IsBound() bool {
	return b.Key.Source != "" && (b.Key.Path != "" || b.Key.ID != 0 || b.Key.EntryID != 0 || b.Key.Instance != 0)
}

// Read resolves the bound sensor's current reading.
func (b *SensorBinding) Read(ctx *EvalContext) (sensor.Reading, bool) {
	if !b.IsBound() {
		return sensor.Reading{}, false
	}
	return ctx.sensors().Read(b.Key)
}

// Apply runs a raw reading through the binding's transform.
//
// Multiplication and division are mutually exclusive, and a zero multiplier in
// divide mode is skipped rather than producing an infinity — matching
// InfoPanel's guard.
func (b *SensorBinding) Apply(r sensor.Reading) float64 {
	v := r.Select(b.ValueType)

	if b.Divide {
		if b.Multiplier != 0 {
			v = v/b.Multiplier + b.Offset
		} else {
			v += b.Offset
		}
	} else {
		multiplier := b.Multiplier
		if multiplier == 0 && b.Offset == 0 {
			// An unset binding (zero value from an older profile) should not
			// silently flatten every reading to zero.
			multiplier = 1
		}
		v = v*multiplier + b.Offset
	}

	if b.AbsoluteOffset {
		v = math.Abs(v)
	}
	return v
}

// Value resolves and transforms in one step.
func (b *SensorBinding) Value(ctx *EvalContext) (float64, bool) {
	r, ok := b.Read(ctx)
	if !ok {
		return 0, false
	}
	return b.Apply(r), true
}

// Normalized maps the bound value onto 0..1 across the given range, clamped.
// Used by bars, donuts, graphs, and gauges.
func (b *SensorBinding) Normalized(ctx *EvalContext, min, max float64) (float64, bool) {
	v, ok := b.Value(ctx)
	if !ok {
		return 0, false
	}
	if max == min {
		return 0, true
	}
	n := (v - min) / (max - min)
	return math.Max(0, math.Min(1, n)), true
}

// defaultPrecisionForUnit reproduces InfoPanel's per-unit decimal defaults,
// used when an item does not override precision explicitly.
func defaultPrecisionForUnit(unit string) int {
	switch strings.ToLower(unit) {
	case "gb":
		return 1
	case "kb/s", "mb/s", "mbar/min", "mbar":
		return 2
	case "v":
		return 3
	default:
		return 0
	}
}

// formatValue renders v with the given number of decimal places.
//
// At zero precision InfoPanel floors when precision was overridden explicitly
// but rounds when falling back to the unit default; floor is passed through so
// both behaviors are reproducible.
func formatValue(v float64, precision int, floor bool) string {
	if precision <= 0 {
		if floor {
			v = math.Floor(v)
		}
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'f', precision, 64)
}

// applyThousandsSeparator regroups an already-formatted number with commas,
// preserving its decimal places.
func applyThousandsSeparator(s string) string {
	neg := strings.HasPrefix(s, "-")
	body := strings.TrimPrefix(s, "-")

	intPart, fracPart, hasFrac := strings.Cut(body, ".")
	if len(intPart) > 3 {
		var b strings.Builder
		lead := len(intPart) % 3
		if lead > 0 {
			b.WriteString(intPart[:lead])
		}
		for i := lead; i < len(intPart); i += 3 {
			if b.Len() > 0 {
				b.WriteByte(',')
			}
			b.WriteString(intPart[i : i+3])
		}
		intPart = b.String()
	}

	out := intPart
	if hasFrac {
		out += "." + fracPart
	}
	if neg {
		out = "-" + out
	}
	return out
}
