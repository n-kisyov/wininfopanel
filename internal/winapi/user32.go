package winapi

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// x/sys/windows covers kernel-level APIs but not the windowing ones, so the
// user32 and gdi32 entry points the overlay windows need are bound here.

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	gdi32  = windows.NewLazySystemDLL("gdi32.dll")

	procRegisterClassExW  = user32.NewProc("RegisterClassExW")
	procCreateWindowExW   = user32.NewProc("CreateWindowExW")
	procDestroyWindow     = user32.NewProc("DestroyWindow")
	procDefWindowProcW    = user32.NewProc("DefWindowProcW")
	procGetMessageW       = user32.NewProc("GetMessageW")
	procTranslateMessage  = user32.NewProc("TranslateMessage")
	procDispatchMessageW  = user32.NewProc("DispatchMessageW")
	procPostQuitMessage   = user32.NewProc("PostQuitMessage")
	procPostMessageW      = user32.NewProc("PostMessageW")
	procShowWindow        = user32.NewProc("ShowWindow")
	procUpdateLayeredWin  = user32.NewProc("UpdateLayeredWindow")
	procSetWindowPos      = user32.NewProc("SetWindowPos")
	procGetDC             = user32.NewProc("GetDC")
	procReleaseDC         = user32.NewProc("ReleaseDC")
	procLoadCursorW       = user32.NewProc("LoadCursorW")
	procSetWindowLongPtrW = user32.NewProc("SetWindowLongPtrW")
	procGetWindowLongPtrW = user32.NewProc("GetWindowLongPtrW")
	procGetWindowRect     = user32.NewProc("GetWindowRect")
	procEnumDisplayMons   = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW   = user32.NewProc("GetMonitorInfoW")
	procSetProcessDpiCtx  = user32.NewProc("SetProcessDpiAwarenessContext")

	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
)

// Window styles and messages, from winuser.h.
const (
	wsPopup   = 0x80000000
	wsVisible = 0x10000000

	wsExLayered     = 0x00080000
	wsExToolWindow  = 0x00000080
	wsExTopmost     = 0x00000008
	wsExNoActivate  = 0x08000000
	wsExTransparent = 0x00000020

	swHide           = 0
	swShowNoActivate = 4

	// Messages the overlay handles.
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmNCHitTest     = 0x0084
	wmNCLButtonDown = 0x00A1
	wmLButtonDown   = 0x0201
	wmRButtonUp     = 0x0205
	wmNCRButtonUp   = 0x00A5
	wmDisplayChange = 0x007E
	wmDPIChanged    = 0x02E0
	// wmAppRedraw asks the window thread to repaint. WM_APP is the first
	// message value reserved for applications.
	wmAppRedraw = 0x8000 + 1
	// wmAppClose asks the window thread to tear the window down.
	wmAppClose = 0x8000 + 2

	// Hit-test results.
	htClient      = 1
	htCaption     = 2
	htTransparent = -1

	// SetWindowPos flags.
	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoActivate = 0x0010
	swpNoZOrder   = 0x0004
	swpShowWindow = 0x0040

	// SetWindowPos ordering handles.
	hwndTopmost   = ^uintptr(0)     // (HWND)-1
	hwndNoTopmost = ^uintptr(1) + 1 // (HWND)-2

	// gwlpUserData is GWLP_USERDATA (-21), the per-window slot holding the Go
	// object pointer. It is stored as an untyped constant because the API
	// takes it as a signed index widened to pointer size.
	gwlpUserData = ^uintptr(20) // -21

	// UpdateLayeredWindow flags.
	ulwAlpha = 0x00000002
	// AC_SRC_OVER / AC_SRC_ALPHA for the blend function.
	acSrcOver  = 0x00
	acSrcAlpha = 0x01

	// CreateDIBSection usage.
	dibRGBColors = 0

	idcArrow   = 32512
	idcSizeAll = 32646
)

// point is the Win32 POINT.
type point struct {
	X, Y int32
}

// rect is the Win32 RECT.
type rect struct {
	Left, Top, Right, Bottom int32
}

// size is the Win32 SIZE.
type size struct {
	CX, CY int32
}

// msg is the Win32 MSG.
type msg struct {
	HWnd    windows.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

// wndClassEx is the Win32 WNDCLASSEXW.
type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

// blendFunction is the Win32 BLENDFUNCTION, describing per-pixel alpha
// compositing for UpdateLayeredWindow.
type blendFunction struct {
	BlendOp             byte
	BlendFlags          byte
	SourceConstantAlpha byte
	AlphaFormat         byte
}

// bitmapInfoHeader is the Win32 BITMAPINFOHEADER.
type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// bitmapInfo is the Win32 BITMAPINFO. The color table is unused for a 32-bit
// uncompressed bitmap but must be present for the struct to be the right size.
type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

// monitorInfoEx is the Win32 MONITORINFOEXW.
type monitorInfoEx struct {
	Size    uint32
	Monitor rect
	Work    rect
	Flags   uint32
	Device  [32]uint16
}

// dpiAwarenessPerMonitorV2 is DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2.
//
// Per-monitor v2 is what lets an overlay stay the right physical size when
// dragged between displays with different scaling, which is a routine setup
// for anyone running a small panel next to a main monitor.
const dpiAwarenessPerMonitorV2 = ^uintptr(3) //nolint:staticcheck // (DPI_AWARENESS_CONTEXT)-4

// SetPerMonitorDPIAware opts the process into per-monitor v2 DPI awareness.
//
// It must be called before any window is created. A failure is not fatal: the
// process simply runs with the system default, and overlays scale less
// gracefully across mixed-DPI displays.
func SetPerMonitorDPIAware() error {
	ret, _, err := procSetProcessDpiCtx.Call(dpiAwarenessPerMonitorV2)
	if ret == 0 {
		return err
	}
	return nil
}

func makeIntResource(id uint16) *uint16 {
	return (*uint16)(unsafe.Pointer(uintptr(id)))
}
