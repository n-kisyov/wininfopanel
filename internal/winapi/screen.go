package winapi

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Screen describes one connected display.
type Screen struct {
	// DeviceName is the Windows display name, e.g. `\\.\DISPLAY1`. Profiles
	// store it so an overlay returns to the same physical monitor after a
	// reconnect, even if the desktop coordinates have shifted.
	DeviceName string

	// Bounds is the display's full extent in virtual desktop coordinates.
	Bounds Rectangle
	// WorkArea excludes the taskbar and other appbars.
	WorkArea Rectangle

	// Primary marks the display the desktop origin sits on.
	Primary bool
	// DPI is the display's effective dots per inch; 96 is unscaled.
	DPI int
}

// Rectangle is a screen region in virtual desktop coordinates.
type Rectangle struct {
	Left, Top, Right, Bottom int
}

// Width returns the region's width.
func (r Rectangle) Width() int { return r.Right - r.Left }

// Height returns the region's height.
func (r Rectangle) Height() int { return r.Bottom - r.Top }

// Contains reports whether a point falls inside the region.
func (r Rectangle) Contains(x, y int) bool {
	return x >= r.Left && x < r.Right && y >= r.Top && y < r.Bottom
}

func rectangleFrom(r rect) Rectangle {
	return Rectangle{Left: int(r.Left), Top: int(r.Top), Right: int(r.Right), Bottom: int(r.Bottom)}
}

// monitorInfoPrimary is MONITORINFOF_PRIMARY.
const monitorInfoPrimary = 0x00000001

// Screens enumerates the connected displays.
//
// The order follows Windows' own enumeration, which is stable for a given
// hardware arrangement but is not guaranteed across reconfigurations -- which
// is exactly why profiles reference a display by DeviceName rather than index.
func Screens() ([]Screen, error) {
	var (
		screens []Screen
		callErr error
	)

	callback := windows.NewCallback(func(monitor windows.Handle, _ windows.Handle,
		_ *rect, _ uintptr) uintptr {

		info := monitorInfoEx{Size: uint32(unsafe.Sizeof(monitorInfoEx{}))}
		ret, _, err := procGetMonitorInfoW.Call(uintptr(monitor), uintptr(unsafe.Pointer(&info)))
		if ret == 0 {
			callErr = fmt.Errorf("get monitor info: %w", err)
			return 1 // keep enumerating; one bad monitor should not lose the rest
		}

		screens = append(screens, Screen{
			DeviceName: windows.UTF16ToString(info.Device[:]),
			Bounds:     rectangleFrom(info.Monitor),
			WorkArea:   rectangleFrom(info.Work),
			Primary:    info.Flags&monitorInfoPrimary != 0,
			DPI:        monitorDPI(monitor),
		})
		return 1
	})

	ret, _, err := procEnumDisplayMons.Call(0, 0, callback, 0)
	if ret == 0 {
		return nil, fmt.Errorf("enumerate displays: %w", err)
	}
	if len(screens) == 0 && callErr != nil {
		return nil, callErr
	}
	return screens, nil
}

// PrimaryScreen returns the display holding the desktop origin.
func PrimaryScreen() (Screen, error) {
	screens, err := Screens()
	if err != nil {
		return Screen{}, err
	}
	for _, s := range screens {
		if s.Primary {
			return s, nil
		}
	}
	if len(screens) > 0 {
		return screens[0], nil
	}
	return Screen{}, fmt.Errorf("no displays found")
}

// ScreenAt returns the display containing a point, falling back to the primary
// when the point lies outside every display -- which happens routinely when a
// saved overlay position refers to a monitor that has since been unplugged.
func ScreenAt(x, y int) (Screen, error) {
	screens, err := Screens()
	if err != nil {
		return Screen{}, err
	}
	for _, s := range screens {
		if s.Bounds.Contains(x, y) {
			return s, nil
		}
	}
	return PrimaryScreen()
}

// ScreenByName returns the display with the given device name.
func ScreenByName(deviceName string) (Screen, bool) {
	screens, err := Screens()
	if err != nil {
		return Screen{}, false
	}
	for _, s := range screens {
		if s.DeviceName == deviceName {
			return s, true
		}
	}
	return Screen{}, false
}

const (
	// mdtEffectiveDPI is MDT_EFFECTIVE_DPI: the scaling actually in use,
	// rather than the panel's raw pixel density.
	mdtEffectiveDPI = 0
	// defaultDPI is the unscaled baseline Windows measures against.
	defaultDPI = 96
)

var (
	shcore               = windows.NewLazySystemDLL("shcore.dll")
	procGetDpiForMonitor = shcore.NewProc("GetDpiForMonitor")
)

// monitorDPI returns a display's effective DPI, defaulting to 96 when the
// query is unavailable.
func monitorDPI(monitor windows.Handle) int {
	var dpiX, dpiY uint32
	ret, _, _ := procGetDpiForMonitor.Call(
		uintptr(monitor),
		mdtEffectiveDPI,
		uintptr(unsafe.Pointer(&dpiX)),
		uintptr(unsafe.Pointer(&dpiY)),
	)
	if ret != 0 || dpiX == 0 {
		return defaultDPI
	}
	return int(dpiX)
}

// ClampToScreens moves a rectangle so it lies on a visible display.
//
// A saved overlay position can point off-screen after a monitor is unplugged
// or the layout changes; without this the panel would render somewhere the
// user cannot see and would look like it had failed to start.
func ClampToScreens(x, y, width, height int) (int, int) {
	screens, err := Screens()
	if err != nil || len(screens) == 0 {
		return x, y
	}

	// Already visible somewhere: leave it alone. Testing the top-left corner
	// plus a margin tolerates a panel hanging slightly off an edge, which is a
	// deliberate arrangement people do use.
	const margin = 32
	for _, s := range screens {
		if s.Bounds.Contains(x+margin, y+margin) {
			return x, y
		}
	}

	primary, err := PrimaryScreen()
	if err != nil {
		return x, y
	}
	work := primary.WorkArea

	nx := work.Left + (work.Width()-width)/2
	ny := work.Top + (work.Height()-height)/2
	if nx < work.Left {
		nx = work.Left
	}
	if ny < work.Top {
		ny = work.Top
	}
	return nx, ny
}
