package importer

import "strings"

// Clock and calendar items store a .NET custom date/time format string;
// wininfopanel stores a Go reference-time layout. The two describe the same
// thing with entirely different notation, so imported formats are translated
// rather than carried across verbatim -- an untranslated "dd/MM/yyyy" would
// render literally as the letters themselves.

// dateFormatTokens maps .NET format specifiers to Go layout fragments.
//
// Order matters: longer specifiers must be tried first, or "yyyy" would be
// consumed as two "yy" tokens.
var dateFormatTokens = []struct {
	dotnet string
	golang string
}{
	// Year.
	{"yyyy", "2006"},
	{"yyy", "2006"},
	{"yy", "06"},
	{"y", "06"},

	// Month. .NET uses uppercase M for months, lowercase m for minutes.
	{"MMMM", "January"},
	{"MMM", "Jan"},
	{"MM", "01"},
	{"M", "1"},

	// Day.
	{"dddd", "Monday"},
	{"ddd", "Mon"},
	{"dd", "02"},
	{"d", "2"},

	// Hour. Uppercase H is 24-hour, lowercase h is 12-hour.
	{"HH", "15"},
	{"H", "15"},
	{"hh", "03"},
	{"h", "3"},

	// Minute.
	{"mm", "04"},
	{"m", "4"},

	// Second.
	{"ss", "05"},
	{"s", "5"},

	// Fractional seconds. Go spells these as a decimal point followed by
	// zeros, and the point has to be part of the token.
	{".fff", ".000"},
	{".ff", ".00"},
	{".f", ".0"},
	{"fff", "000"},
	{"ff", "00"},
	{"f", "0"},

	// Meridiem. .NET's "tt" is the full designator; a single "t" is its first
	// letter, which Go cannot express, so it widens to the full one.
	{"tt", "PM"},
	{"t", "PM"},

	// Time zone.
	{"zzz", "-07:00"},
	{"zz", "-07"},
	{"z", "-07"},
	{"K", "Z07:00"},
}

// convertDateFormat translates a .NET format string into a Go layout.
//
// Text inside single or double quotes is .NET's literal escape and is copied
// through unquoted; a backslash escapes the character after it. Anything that
// matches no specifier -- separators, spaces, punctuation -- passes through
// unchanged.
func convertDateFormat(format string) string {
	if format == "" {
		return ""
	}

	var out strings.Builder
	out.Grow(len(format) + 8)

	for i := 0; i < len(format); {
		c := format[i]

		// Backslash escapes the next character as a literal.
		if c == '\\' && i+1 < len(format) {
			out.WriteByte(format[i+1])
			i += 2
			continue
		}

		// Quoted runs are literal text.
		if c == '\'' || c == '"' {
			end := strings.IndexByte(format[i+1:], c)
			if end < 0 {
				// Unterminated quote: take the rest as literal rather than
				// dropping it.
				out.WriteString(format[i+1:])
				break
			}
			out.WriteString(format[i+1 : i+1+end])
			i += end + 2
			continue
		}

		if token, width := matchDateToken(format[i:]); width > 0 {
			out.WriteString(token)
			i += width
			continue
		}

		out.WriteByte(c)
		i++
	}

	return out.String()
}

// matchDateToken returns the Go fragment for the specifier starting at the
// beginning of s, and how many bytes it consumed.
func matchDateToken(s string) (string, int) {
	for _, token := range dateFormatTokens {
		if strings.HasPrefix(s, token.dotnet) {
			return token.golang, len(token.dotnet)
		}
	}
	return "", 0
}
