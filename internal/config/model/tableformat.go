package model

import (
	"strconv"
	"strings"
)

// TableColumn is one column selected by a table format string.
type TableColumn struct {
	// Index is the source column's position in the plugin's table.
	Index int
	// Width is the column's rendered width in pixels.
	Width int
}

// ParseTableFormat reads InfoPanel's table format syntax,
// "columnIndex:width|columnIndex:width", into an ordered column list.
//
// Malformed segments are skipped rather than failing: a table with a typo in
// its format should still render the columns that parsed.
func ParseTableFormat(format string) []TableColumn {
	if strings.TrimSpace(format) == "" {
		return nil
	}

	var cols []TableColumn
	for _, segment := range strings.Split(format, "|") {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}

		idxText, widthText, ok := strings.Cut(segment, ":")
		if !ok {
			continue
		}

		index, err := strconv.Atoi(strings.TrimSpace(idxText))
		if err != nil || index < 0 {
			continue
		}
		width, err := strconv.Atoi(strings.TrimSpace(widthText))
		if err != nil || width <= 0 {
			continue
		}

		cols = append(cols, TableColumn{Index: index, Width: width})
	}
	return cols
}

// FormatTableColumns renders a column list back to format syntax.
func FormatTableColumns(cols []TableColumn) string {
	parts := make([]string, 0, len(cols))
	for _, c := range cols {
		parts = append(parts, strconv.Itoa(c.Index)+":"+strconv.Itoa(c.Width))
	}
	return strings.Join(parts, "|")
}

// TableFormatWidth totals the widths a format string declares.
func TableFormatWidth(format string) int {
	total := 0
	for _, c := range ParseTableFormat(format) {
		total += c.Width
	}
	return total
}
