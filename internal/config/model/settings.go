package model

// Theme selects the UI color scheme. Only Dark ships initially; the value is
// persisted so light and custom themes can be added without a migration.
type Theme string

const (
	ThemeDark  Theme = "dark"
	ThemeLight Theme = "light"
)

// Rotation is a display rotation in degrees, applied when sending frames to a
// panel whose physical orientation differs from its native scan order.
type Rotation int

const (
	Rotate0   Rotation = 0
	Rotate90  Rotation = 90
	Rotate180 Rotation = 180
	Rotate270 Rotation = 270
)

// PanelDevice is the configuration shared by every USB LCD panel family.
type PanelDevice struct {
	// ID is a stable identifier for this configuration entry.
	ID string `json:"id"`
	// SerialNumber identifies the physical device, so a panel keeps its
	// settings across reconnects and USB port changes.
	SerialNumber string `json:"serialNumber,omitempty"`
	// DevicePath is the last-seen OS path, a fallback for devices that do not
	// report a serial number.
	DevicePath string `json:"devicePath,omitempty"`

	// Model names the detected hardware model.
	Model string `json:"model,omitempty"`

	Enabled bool `json:"enabled"`
	// ProfileID selects which profile renders to this panel.
	ProfileID string `json:"profileId,omitempty"`

	Rotation Rotation `json:"rotation,omitempty"`
	// Brightness is 0-100.
	Brightness int `json:"brightness"`
}

// HotkeyBinding maps a global shortcut to an application action.
type HotkeyBinding struct {
	ID string `json:"id"`
	// Action names the command to run, e.g. "profile.toggle".
	Action string `json:"action"`
	// ProfileID scopes actions that operate on a specific profile.
	ProfileID string `json:"profileId,omitempty"`

	// Modifiers combines "ctrl", "alt", "shift", and "win".
	Modifiers []string `json:"modifiers,omitempty"`
	// Key is the main key, e.g. "F9".
	Key string `json:"key"`

	Enabled bool `json:"enabled"`
}

// WebServerSettings configures the built-in HTTP server, which serves the
// same UI remotely and exposes rendered panel frames.
type WebServerSettings struct {
	Enabled  bool   `json:"enabled"`
	ListenIP string `json:"listenIp"`
	Port     int    `json:"port"`
	// RefreshRate is the frame interval in milliseconds.
	RefreshRate int `json:"refreshRate"`
}

// Settings is the application's global configuration.
type Settings struct {
	// Version tracks the settings schema for future migrations.
	Version int `json:"version"`

	// UI window geometry and appearance.
	UIWidth   float64 `json:"uiWidth"`
	UIHeight  float64 `json:"uiHeight"`
	UIScale   float64 `json:"uiScale"`
	Theme     Theme   `json:"theme"`
	PaneOpen  bool    `json:"paneOpen"`
	AppFolder string  `json:"appFolder,omitempty"`

	// Startup behavior.
	AutoStart      bool `json:"autoStart,omitempty"`
	AutoStartDelay int  `json:"autoStartDelay"`
	StartMinimized bool `json:"startMinimized,omitempty"`
	MinimizeToTray bool `json:"minimizeToTray"`
	CloseToTray    bool `json:"closeToTray,omitempty"`

	// Design canvas.
	SelectedItemColor string  `json:"selectedItemColor"`
	ShowGridLines     bool    `json:"showGridLines"`
	GridLinesSpacing  float64 `json:"gridLinesSpacing"`
	GridLinesColor    string  `json:"gridLinesColor"`

	// Data sources.
	HWiNFOEnabled  bool `json:"hwinfoEnabled"`
	HWiNFOInterval int  `json:"hwinfoInterval"`
	NativeEnabled  bool `json:"nativeEnabled"`
	// NativeStorage polls storage sensors, which are slow enough to warrant
	// their own longer interval.
	NativeStorage         bool `json:"nativeStorage"`
	NativeStorageInterval int  `json:"nativeStorageInterval"`

	// Rendering.
	TargetFrameRate       int `json:"targetFrameRate"`
	TargetGraphUpdateRate int `json:"targetGraphUpdateRate"`

	// Autosave snapshots the layout while editing.
	AutosaveEnabled     bool `json:"autosaveEnabled,omitempty"`
	AutosaveIdleSeconds int  `json:"autosaveIdleSeconds"`

	// Program-specific panels swap profiles based on the foreground window.
	ProgramSpecificPanels bool `json:"programSpecificPanels,omitempty"`
	// HideOtherProfilesWhenTriggered hides normal profiles while a
	// program-specific one is showing.
	HideOtherProfilesWhenTriggered bool `json:"hideOtherProfilesWhenTriggered"`

	WebServer WebServerSettings `json:"webServer"`

	// USB panel device configurations, one list per family.
	BeadaPanels        []PanelDevice `json:"beadaPanels,omitempty"`
	TuringPanels       []PanelDevice `json:"turingPanels,omitempty"`
	ThermalrightPanels []PanelDevice `json:"thermalrightPanels,omitempty"`
	ThermaltakePanels  []PanelDevice `json:"thermaltakePanels,omitempty"`

	Hotkeys []HotkeyBinding `json:"hotkeys,omitempty"`
}

// SettingsVersion is the current settings schema version.
const SettingsVersion = 1

// DefaultSettings returns the settings a fresh installation starts with.
func DefaultSettings() *Settings {
	return &Settings{
		Version: SettingsVersion,

		UIWidth:  1300,
		UIHeight: 900,
		UIScale:  1.0,
		Theme:    ThemeDark,
		PaneOpen: true,

		AutoStartDelay: 5,
		MinimizeToTray: true,

		SelectedItemColor: "#FF00FF00",
		ShowGridLines:     true,
		GridLinesSpacing:  20,
		GridLinesColor:    "#1A808080",

		HWiNFOEnabled:         true,
		HWiNFOInterval:        1000,
		NativeEnabled:         true,
		NativeStorage:         true,
		NativeStorageInterval: 30,

		TargetFrameRate:       15,
		TargetGraphUpdateRate: 1000,

		AutosaveIdleSeconds: 3,

		HideOtherProfilesWhenTriggered: true,

		WebServer: WebServerSettings{
			ListenIP:    "127.0.0.1",
			Port:        8080,
			RefreshRate: 66,
		},
	}
}

// Normalize repairs values that are out of range or missing, so a hand-edited
// or partially-migrated settings file still produces a usable application.
func (s *Settings) Normalize() {
	if s.Version == 0 {
		s.Version = SettingsVersion
	}
	if s.UIWidth <= 0 {
		s.UIWidth = 1300
	}
	if s.UIHeight <= 0 {
		s.UIHeight = 900
	}
	if s.UIScale <= 0 {
		s.UIScale = 1
	}
	if s.Theme == "" {
		s.Theme = ThemeDark
	}
	if s.HWiNFOInterval < 100 {
		s.HWiNFOInterval = 1000
	}
	if s.NativeStorageInterval < 1 {
		s.NativeStorageInterval = 30
	}
	if s.TargetFrameRate < 1 {
		s.TargetFrameRate = 15
	}
	if s.TargetGraphUpdateRate < 1 {
		s.TargetGraphUpdateRate = 1000
	}
	if s.AutosaveIdleSeconds < 1 {
		s.AutosaveIdleSeconds = 3
	}
	if s.GridLinesSpacing <= 0 {
		s.GridLinesSpacing = 20
	}
	if s.WebServer.ListenIP == "" {
		s.WebServer.ListenIP = "127.0.0.1"
	}
	if s.WebServer.Port <= 0 || s.WebServer.Port > 65535 {
		s.WebServer.Port = 8080
	}
	if s.WebServer.RefreshRate < 16 {
		s.WebServer.RefreshRate = 66
	}
}
