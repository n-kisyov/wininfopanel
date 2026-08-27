package graphics

import (
	"image/color"
	"testing"
)

func TestParseColorFormats(t *testing.T) {
	tests := []struct {
		in   string
		want color.NRGBA
	}{
		// Six digits are opaque RGB.
		{"#000000", color.NRGBA{0, 0, 0, 255}},
		{"#FFFFFF", color.NRGBA{255, 255, 255, 255}},
		{"808080", color.NRGBA{128, 128, 128, 255}},

		// Eight digits are alpha-first, matching .NET's ARGB convention.
		// Reading these as RGBA would make opaque green nearly transparent.
		{"#FF00FF00", color.NRGBA{0, 255, 0, 255}},
		{"#3C888DFF", color.NRGBA{136, 141, 255, 60}},
		{"#1A808080", color.NRGBA{128, 128, 128, 26}},
		{"#00000000", color.NRGBA{0, 0, 0, 0}},

		// Shorthand doubles each digit.
		{"#f0a", color.NRGBA{255, 0, 170, 255}},
		{"#8f0a", color.NRGBA{255, 0, 170, 136}},

		// Case and whitespace are tolerated.
		{"  #ff00ff00  ", color.NRGBA{0, 255, 0, 255}},
	}

	for _, tt := range tests {
		got, err := ParseColor(tt.in)
		if err != nil {
			t.Errorf("ParseColor(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseColor(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestParseColorRejectsMalformed(t *testing.T) {
	for _, in := range []string{"", "#", "#12345", "#GGGGGG", "#1234567", "not a color"} {
		if _, err := ParseColor(in); err == nil {
			t.Errorf("ParseColor(%q) succeeded, want an error", in)
		}
	}
}

func TestColorOrFallsBackOnBadInput(t *testing.T) {
	fallback := color.NRGBA{1, 2, 3, 4}

	if got := ColorOr("#not-a-color", fallback); got != fallback {
		t.Errorf("ColorOr on malformed input = %+v, want the fallback", got)
	}
	if got := ColorOr("", fallback); got != fallback {
		t.Errorf("ColorOr on empty input = %+v, want the fallback", got)
	}
	if got := ColorOr("#FF0000", fallback); got != (color.NRGBA{255, 0, 0, 255}) {
		t.Errorf("ColorOr on valid input = %+v, want the parsed color", got)
	}
}

func TestFormatColorRoundTrips(t *testing.T) {
	for _, in := range []string{"#FF00FF00", "#3C888DFF", "#00000000", "#1A808080"} {
		parsed, err := ParseColor(in)
		if err != nil {
			t.Fatalf("ParseColor(%q): %v", in, err)
		}
		if got := FormatColor(parsed); got != in {
			t.Errorf("round trip of %q produced %q", in, got)
		}
	}
}

func TestWithAlphaScalesOpacity(t *testing.T) {
	opaque := color.NRGBA{10, 20, 30, 200}

	if got := WithAlpha(opaque, 0); got.A != 0 {
		t.Errorf("alpha at opacity 0 = %d, want 0", got.A)
	}
	if got := WithAlpha(opaque, 1); got.A != 200 {
		t.Errorf("alpha at opacity 1 = %d, want it unchanged at 200", got.A)
	}
	if got := WithAlpha(opaque, 0.5); got.A != 100 {
		t.Errorf("alpha at opacity 0.5 = %d, want 100", got.A)
	}
	if got := WithAlpha(opaque, 2); got.A != 200 {
		t.Errorf("alpha at opacity above 1 = %d, want it clamped to 200", got.A)
	}

	// The color channels must survive untouched.
	got := WithAlpha(opaque, 0.5)
	if got.R != 10 || got.G != 20 || got.B != 30 {
		t.Errorf("WithAlpha changed the color channels: %+v", got)
	}
}
