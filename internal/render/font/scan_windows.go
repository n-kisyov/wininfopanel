package font

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// Windows records installed fonts in the registry, mapping a display name to
// a font file:
//
//	"Segoe UI (TrueType)"             -> "segoeui.ttf"
//	"Segoe UI Bold Italic (TrueType)" -> "segoeuiz.ttf"
//	"Cascadia Mono (TrueType)"        -> "CascadiaMono.ttf"
//
// Those display names are what Windows and WPF report, so they are what
// InfoPanel profiles contain -- which makes the registry the right index to
// build from. Reading each font file's internal name table would be more
// authoritative but would mean opening several hundred files at startup.

const fontsRegistryKey = `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Fonts`

// fontFileExtensions are the formats the sfnt parser can read.
var fontFileExtensions = map[string]bool{
	".ttf": true,
	".otf": true,
	".ttc": true,
}

// scanLocked builds the font index. Caller holds c.mu.
//
// Machine-wide fonts are indexed first so they win over per-user copies of the
// same face.
func (c *Cache) scanLocked() error {
	systemDir := filepath.Join(os.Getenv("SystemRoot"), "Fonts")
	userDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Windows", "Fonts")

	c.scanRegistryLocked(registry.LOCAL_MACHINE, systemDir)
	c.scanRegistryLocked(registry.CURRENT_USER, userDir)

	// The registry can be incomplete -- a font dropped into the directory
	// without being installed will not appear there -- so sweep the
	// directories as a backstop. Files the registry already named are skipped:
	// a filename stem like "ANTQUAB" is a far worse family name than the
	// registry's "Book Antiqua Bold", and adding both would clutter the font
	// picker with cryptic duplicates.
	c.scanDirectoryLocked(systemDir)
	c.scanDirectoryLocked(userDir)

	c.sortLocked()

	if len(c.families) == 0 {
		return fmt.Errorf("no fonts found in %s or %s", systemDir, userDir)
	}
	return nil
}

// scanRegistryLocked indexes the fonts recorded under one registry hive.
// Caller holds c.mu.
func (c *Cache) scanRegistryLocked(hive registry.Key, defaultDir string) {
	key, err := registry.OpenKey(hive, fontsRegistryKey, registry.READ)
	if err != nil {
		// A missing per-user font key is normal on a machine with no
		// user-installed fonts.
		return
	}
	defer key.Close()

	names, err := key.ReadValueNames(0)
	if err != nil {
		return
	}

	for _, displayName := range names {
		fileName, _, err := key.GetStringValue(displayName)
		if err != nil || fileName == "" {
			continue
		}

		path := fileName
		if !filepath.IsAbs(path) {
			path = filepath.Join(defaultDir, fileName)
		}
		if !fontFileExtensions[strings.ToLower(filepath.Ext(path))] {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}

		// One registry entry can name several faces sharing a file, e.g.
		// "Arial Bold, Arial Bold Italic (TrueType)".
		for _, entry := range splitRegistryNames(displayName) {
			family, style := splitFamilyStyle(entry)
			if family == "" {
				continue
			}
			c.addLocked(family, style, path)
		}
	}
}

// scanDirectoryLocked indexes font files directly, inferring names from
// filenames. Caller holds c.mu.
func (c *Cache) scanDirectoryLocked(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if !fontFileExtensions[strings.ToLower(filepath.Ext(path))] {
			continue
		}
		if c.indexedPaths[strings.ToLower(path)] {
			continue
		}

		family, style, ok := familyFromFilename(path)
		if !ok {
			continue
		}
		c.addLocked(family, style, path)
	}
}

// splitRegistryNames separates the comma-joined face names one registry entry
// can carry, and strips the trailing format annotation.
func splitRegistryNames(displayName string) []string {
	// The "(TrueType)" suffix applies to the whole entry, so remove it once
	// before splitting.
	if open := strings.LastIndex(displayName, " ("); open >= 0 &&
		strings.HasSuffix(displayName, ")") {
		displayName = displayName[:open]
	}

	parts := strings.Split(displayName, "&")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		for _, name := range strings.Split(part, ",") {
			if name = strings.TrimSpace(name); name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}

// styleSuffixes maps a trailing word sequence to the style it denotes.
//
// Order matters: the two-word forms must be tested before the single words, or
// "Arial Bold Italic" would be read as family "Arial Bold", style Italic.
var styleSuffixes = []struct {
	suffix string
	style  Style
}{
	{"bold italic", Style{Bold: true, Italic: true}},
	{"italic bold", Style{Bold: true, Italic: true}},
	{"bold oblique", Style{Bold: true, Italic: true}},
	{"bold", Style{Bold: true}},
	{"italic", Style{Italic: true}},
	{"oblique", Style{Italic: true}},
	{"regular", Style{}},
}

// splitFamilyStyle separates a face name into its family and style.
//
// Only exact trailing style words are stripped, so families whose names end in
// a weight word -- "Arial Black", "Segoe UI Semibold" -- stay intact. Windows
// registers those as separate families, and treating "Black" as a style would
// merge two distinct faces.
func splitFamilyStyle(name string) (string, Style) {
	trimmed := strings.TrimSpace(name)
	lower := strings.ToLower(trimmed)

	for _, candidate := range styleSuffixes {
		suffix := " " + candidate.suffix
		if !strings.HasSuffix(lower, suffix) {
			continue
		}
		family := strings.TrimSpace(trimmed[:len(trimmed)-len(suffix)])
		if family == "" {
			break
		}
		return family, candidate.style
	}

	return trimmed, Style{}
}

// familyFromFilename infers a family and style from a font file's name, used
// for files the registry does not record.
//
// This is a fallback and is necessarily approximate: "segoeuiz.ttf" carries no
// recoverable family name. Registry entries take precedence because addLocked
// keeps the first file registered for a style.
func familyFromFilename(path string) (string, Style, bool) {
	base := filepath.Base(path)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		return "", Style{}, false
	}

	// Filenames commonly join words with separators, e.g.
	// "CascadiaCode-Bold.ttf" or "Roboto_Italic.ttf".
	normalized := strings.NewReplacer("-", " ", "_", " ").Replace(stem)
	family, style := splitFamilyStyle(normalized)
	if family == "" {
		return "", Style{}, false
	}
	return family, style, true
}
