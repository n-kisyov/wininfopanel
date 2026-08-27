package font

import "testing"

func TestSplitFamilyStyle(t *testing.T) {
	tests := []struct {
		in         string
		wantFamily string
		wantStyle  Style
	}{
		{"Arial", "Arial", Style{}},
		{"Arial Bold", "Arial", Style{Bold: true}},
		{"Arial Italic", "Arial", Style{Italic: true}},
		{"Arial Bold Italic", "Arial", Style{Bold: true, Italic: true}},
		{"Arial Italic Bold", "Arial", Style{Bold: true, Italic: true}},
		{"Segoe UI Regular", "Segoe UI", Style{}},
		{"Times New Roman Bold Italic", "Times New Roman", Style{Bold: true, Italic: true}},

		// Families whose names end in a weight word are distinct faces in
		// Windows, not styles of a shorter family. Splitting them would merge
		// "Arial Black" into "Arial".
		{"Arial Black", "Arial Black", Style{}},
		{"Segoe UI Semibold", "Segoe UI Semibold", Style{}},
		{"Calibri Light", "Calibri Light", Style{}},

		// A name that is only a style word has no family to strip to.
		{"Bold", "Bold", Style{}},
	}

	for _, tt := range tests {
		family, style := splitFamilyStyle(tt.in)
		if family != tt.wantFamily || style != tt.wantStyle {
			t.Errorf("splitFamilyStyle(%q) = (%q, %+v), want (%q, %+v)",
				tt.in, family, style, tt.wantFamily, tt.wantStyle)
		}
	}
}

func TestSplitRegistryNames(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"Segoe UI (TrueType)", []string{"Segoe UI"}},
		{"Segoe UI Bold Italic (TrueType)", []string{"Segoe UI Bold Italic"}},
		{"Arial Bold, Arial Bold Italic (TrueType)", []string{"Arial Bold", "Arial Bold Italic"}},
		{"Cascadia Mono (OpenType)", []string{"Cascadia Mono"}},
		{"NoAnnotation", []string{"NoAnnotation"}},
	}

	for _, tt := range tests {
		got := splitRegistryNames(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("splitRegistryNames(%q) = %v, want %v", tt.in, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitRegistryNames(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
			}
		}
	}
}

func TestFamilyFromFilename(t *testing.T) {
	tests := []struct {
		path       string
		wantFamily string
		wantStyle  Style
	}{
		{`C:\Windows\Fonts\CascadiaCode-Bold.ttf`, "CascadiaCode", Style{Bold: true}},
		{`C:\Windows\Fonts\Roboto_Italic.ttf`, "Roboto", Style{Italic: true}},
		{`C:\Windows\Fonts\MyFont.ttf`, "MyFont", Style{}},
	}

	for _, tt := range tests {
		family, style, ok := familyFromFilename(tt.path)
		if !ok {
			t.Errorf("familyFromFilename(%q) reported failure", tt.path)
			continue
		}
		if family != tt.wantFamily || style != tt.wantStyle {
			t.Errorf("familyFromFilename(%q) = (%q, %+v), want (%q, %+v)",
				tt.path, family, style, tt.wantFamily, tt.wantStyle)
		}
	}
}

func TestClosestStyleDegradesGracefully(t *testing.T) {
	// A family shipping only Regular and Bold must satisfy a Bold Italic
	// request with Bold: a missing slant is less noticeable than a missing
	// weight.
	fam := &Family{Name: "Test", files: map[Style]string{
		{}:           "regular.ttf",
		{Bold: true}: "bold.ttf",
	}}

	if got := closestStyle(fam, Style{Bold: true, Italic: true}); got != (Style{Bold: true}) {
		t.Errorf("closestStyle for Bold Italic = %+v, want Bold", got)
	}
	if got := closestStyle(fam, Style{Italic: true}); got != (Style{}) {
		t.Errorf("closestStyle for Italic = %+v, want Regular", got)
	}
	if got := closestStyle(fam, Style{Bold: true}); got != (Style{Bold: true}) {
		t.Errorf("closestStyle for an available style = %+v, want it unchanged", got)
	}
}

func TestStyleString(t *testing.T) {
	tests := []struct {
		style Style
		want  string
	}{
		{Style{}, "Regular"},
		{Style{Bold: true}, "Bold"},
		{Style{Italic: true}, "Italic"},
		{Style{Bold: true, Italic: true}, "Bold Italic"},
	}
	for _, tt := range tests {
		if got := tt.style.String(); got != tt.want {
			t.Errorf("Style%+v.String() = %q, want %q", tt.style, got, tt.want)
		}
	}
}

func TestCacheResolvesInstalledFonts(t *testing.T) {
	// Exercises the real Windows font index. Segoe UI ships with every
	// supported version, so its absence means discovery is broken.
	c := NewCache()

	families, err := c.Families()
	if err != nil {
		t.Fatalf("Families: %v", err)
	}
	if len(families) == 0 {
		t.Fatal("no font families were discovered")
	}
	if !c.Has(DefaultFamily) {
		t.Errorf("%s was not found among %d families", DefaultFamily, len(families))
	}

	face, err := c.Face(DefaultFamily, Style{}, 20)
	if err != nil {
		t.Fatalf("Face(%s): %v", DefaultFamily, err)
	}
	if face == nil {
		t.Fatal("Face returned nil with no error")
	}
}

func TestCacheFallsBackForUnknownFamily(t *testing.T) {
	// A profile authored elsewhere can name a font this machine lacks; it must
	// still render.
	c := NewCache()

	face, err := c.Face("Definitely Not An Installed Font", Style{}, 16)
	if err != nil {
		t.Fatalf("Face on an unknown family: %v, want a fallback", err)
	}
	if face == nil {
		t.Fatal("Face returned nil for an unknown family")
	}
}

func TestCacheRejectsNonPositiveSize(t *testing.T) {
	c := NewCache()
	if _, err := c.Face(DefaultFamily, Style{}, 0); err == nil {
		t.Error("Face accepted a zero size")
	}
}

func TestCacheReturnsSameFaceForRepeatedRequests(t *testing.T) {
	c := NewCache()

	first, err := c.Face(DefaultFamily, Style{}, 18)
	if err != nil {
		t.Fatalf("Face: %v", err)
	}
	second, err := c.Face(DefaultFamily, Style{}, 18)
	if err != nil {
		t.Fatalf("Face: %v", err)
	}
	if first != second {
		t.Error("repeated requests built separate faces; the cache is not being hit")
	}
}
