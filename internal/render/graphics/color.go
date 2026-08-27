package graphics

import (
	"fmt"
	"image/color"
	"strings"
)

// ParseColor reads a hex color string in any of the forms InfoPanel profiles
// use.
//
// Accepted lengths, with or without a leading "#":
//
//	RGB       -> #RRGGBB with each digit doubled, fully opaque
//	RGBA      -> as above with an alpha digit
//	RRGGBB    -> fully opaque
//	AARRGGBB  -> alpha first
//
// The eight-digit form is alpha-first, not alpha-last: that is what .NET's
// ARGB convention produces, and profiles written by InfoPanel are full of
// values like "#FF00FF00" meaning opaque green. Reading those as RGBA would
// silently turn most colors transparent.
func ParseColor(s string) (color.NRGBA, error) {
	text := strings.TrimSpace(s)
	text = strings.TrimPrefix(text, "#")

	if text == "" {
		return color.NRGBA{}, fmt.Errorf("empty color string")
	}

	switch len(text) {
	case 3, 4:
		// Shorthand: each digit is doubled, so "f0a" means "ff00aa".
		var expanded strings.Builder
		expanded.Grow(len(text) * 2)
		for _, r := range text {
			expanded.WriteRune(r)
			expanded.WriteRune(r)
		}
		text = expanded.String()
	case 6, 8:
	default:
		return color.NRGBA{}, fmt.Errorf("color %q must have 3, 4, 6, or 8 hex digits", s)
	}

	value, err := parseHex(text)
	if err != nil {
		return color.NRGBA{}, fmt.Errorf("color %q: %w", s, err)
	}

	if len(text) == 8 {
		return color.NRGBA{
			A: uint8(value >> 24),
			R: uint8(value >> 16),
			G: uint8(value >> 8),
			B: uint8(value),
		}, nil
	}
	return color.NRGBA{
		R: uint8(value >> 16),
		G: uint8(value >> 8),
		B: uint8(value),
		A: 0xFF,
	}, nil
}

// MustParseColor is ParseColor for values known good at compile time.
func MustParseColor(s string) color.NRGBA {
	c, err := ParseColor(s)
	if err != nil {
		panic(err)
	}
	return c
}

// ColorOr parses s, falling back to a default when it is empty or malformed.
//
// Rendering uses this everywhere: a hand-edited profile with one bad color
// should draw in a fallback shade, not abort the frame.
func ColorOr(s string, fallback color.NRGBA) color.NRGBA {
	c, err := ParseColor(s)
	if err != nil {
		return fallback
	}
	return c
}

// FormatColor renders a color as "#AARRGGBB", the form profiles are written
// in. The alpha channel is always emitted so a round trip is lossless.
func FormatColor(c color.NRGBA) string {
	return fmt.Sprintf("#%02X%02X%02X%02X", c.A, c.R, c.G, c.B)
}

// WithAlpha scales a color's alpha by opacity, clamped to 0..1.
func WithAlpha(c color.NRGBA, opacity float64) color.NRGBA {
	switch {
	case opacity <= 0:
		c.A = 0
	case opacity >= 1:
	default:
		c.A = uint8(float64(c.A)*opacity + 0.5)
	}
	return c
}

// parseHex decodes a hex string without allocating through strconv.
func parseHex(s string) (uint32, error) {
	var value uint32
	for _, r := range s {
		var digit uint32
		switch {
		case r >= '0' && r <= '9':
			digit = uint32(r - '0')
		case r >= 'a' && r <= 'f':
			digit = uint32(r-'a') + 10
		case r >= 'A' && r <= 'F':
			digit = uint32(r-'A') + 10
		default:
			return 0, fmt.Errorf("invalid hex digit %q", r)
		}
		value = value<<4 | digit
	}
	return value, nil
}
