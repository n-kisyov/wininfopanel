package model

import "github.com/google/uuid"

// TargetWindow describes a region of a specific display that a profile's
// overlay should track, used to pin a panel to a game or application window.
type TargetWindow struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`

	// DeviceName is the Windows display device the region belongs to, e.g.
	// `\.\DISPLAY1`.
	DeviceName string `json:"deviceName,omitempty"`
}

// Profile is one panel layout: its canvas, its rendering options, and where it
// appears on screen.
//
// Display items live in a separate file per profile, so switching between
// profiles does not require loading every layout.
type Profile struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	Width  int `json:"width"`
	Height int `json:"height"`

	BackgroundColor string `json:"backgroundColor"`

	// Font, FontSize, and Color are the defaults new text items inherit.
	Font     string `json:"font,omitempty"`
	FontSize int    `json:"fontSize"`
	Color    string `json:"color"`
	// FontScale compensates for the difference between design units and
	// rendered pixels.
	FontScale float64 `json:"fontScale"`

	// Active shows the profile's overlay window.
	Active bool `json:"active"`
	// Topmost keeps the overlay above other windows.
	Topmost bool `json:"topmost,omitempty"`
	// Drag allows moving the overlay with the mouse.
	Drag bool `json:"drag"`
	// Resize allows resizing the overlay with the mouse.
	Resize bool `json:"resize"`
	// ShowFPS overlays a frame-rate counter.
	ShowFPS bool `json:"showFps,omitempty"`
	// Accelerated requests the GPU-backed presentation path.
	Accelerated bool `json:"accelerated,omitempty"`

	WindowX int `json:"windowX"`
	WindowY int `json:"windowY"`

	// TargetWindow pins the overlay to a region of a specific display.
	TargetWindow *TargetWindow `json:"targetWindow,omitempty"`

	// TriggerProcessNames lists executables that make this profile appear
	// when they come to the foreground, semicolon separated.
	TriggerProcessNames string `json:"triggerProcessNames,omitempty"`
	// StrictWindowMatching requires an exact process-name match rather than a
	// substring match.
	StrictWindowMatching bool `json:"strictWindowMatching,omitempty"`
}

// NewProfile returns a profile with InfoPanel's defaults for a new panel.
func NewProfile(name string, width, height int) *Profile {
	return &Profile{
		ID:              uuid.NewString(),
		Name:            name,
		Width:           width,
		Height:          height,
		BackgroundColor: "#FFFFFFFF",
		FontSize:        26,
		Color:           "#FF000000",
		FontScale:       1.33,
		Active:          true,
		Drag:            true,
		Resize:          true,
	}
}

// Copy returns an independent copy of the profile, keeping its identity.
//
// It is what a read accessor hands out. A plain `*p` would share TargetWindow
// with the original, so a caller editing what it was told is its own copy
// would be writing back into the source -- for the config store that means its
// live state, outside the lock and without persisting.
func (p *Profile) Copy() *Profile {
	c := *p
	if p.TargetWindow != nil {
		tw := *p.TargetWindow
		c.TargetWindow = &tw
	}
	return &c
}

// Clone returns a copy of the profile carrying a fresh ID.
//
// Copy is the same thing keeping the original's identity; building on it here
// is what stops the two drifting apart as fields are added.
func (p *Profile) Clone() *Profile {
	c := p.Copy()
	c.ID = uuid.NewString()
	return c
}

// Size returns the profile's canvas dimensions.
func (p *Profile) Size() Size {
	return Size{Width: float64(p.Width), Height: float64(p.Height)}
}

// Layout pairs a profile with its display items, which is how the render
// pipeline and the design canvas consume it.
type Layout struct {
	Profile *Profile
	Items   ItemList
}
