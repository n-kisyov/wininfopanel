package winapi

import (
	"fmt"
	"image"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// LayeredWindow is a borderless, per-pixel-alpha desktop overlay.
//
// It is the equivalent of InfoPanel's WPF overlay windows. Rather than
// painting through a window procedure, the frame is pushed with
// UpdateLayeredWindow, which composites an RGBA bitmap directly onto the
// desktop -- that is what gives genuinely transparent edges around a rounded
// panel instead of a color-keyed approximation.
//
// A window belongs to the thread that created it, and Win32 delivers its
// messages only to that thread. Run therefore locks its goroutine to an OS
// thread and owns the window for its lifetime; every other method posts a
// message rather than touching the window directly.
type LayeredWindow struct {
	opts LayeredWindowOptions

	// hwnd is valid only between creation and destruction, on the window
	// thread.
	hwnd windows.Handle

	// The device context and DIB section backing the frame. They are recreated
	// whenever the window resizes.
	memDC     windows.Handle
	bitmap    windows.Handle
	oldBitmap windows.Handle
	pixels    []byte
	width     int
	height    int

	mu      sync.Mutex
	pending *image.RGBA
	closed  bool

	ready chan struct{}
	done  chan struct{}
}

// LayeredWindowOptions configures an overlay.
type LayeredWindowOptions struct {
	// Title is the window title. It is not displayed but identifies the window
	// to tools and to the taskbar's alt-tab list.
	Title string

	X, Y          int
	Width, Height int

	// Topmost keeps the overlay above other windows.
	Topmost bool
	// Draggable lets the user move the overlay by dragging anywhere on it.
	Draggable bool
	// ClickThrough makes the overlay ignore the mouse entirely, so it never
	// interrupts what is underneath.
	ClickThrough bool

	// OnRightClick fires when the overlay is right-clicked, which is how the
	// context menu is raised. It runs on the window thread.
	OnRightClick func()
	// OnMoved fires after a drag, with the new screen position, so the profile
	// can persist where the user put it.
	OnMoved func(x, y int)
	// OnClosed fires once the window has been destroyed.
	OnClosed func()
}

// moduleHandle returns this executable's module handle, which window creation
// needs. x/sys/windows exposes only the Ex form, so the null-name variant is
// spelled out here.
func moduleHandle() (windows.Handle, error) {
	var handle windows.Handle
	if err := windows.GetModuleHandleEx(0, nil, &handle); err != nil {
		return 0, fmt.Errorf("get module handle: %w", err)
	}
	return handle, nil
}

// classNameOnce registers the shared window class exactly once per process.
var (
	classNameOnce sync.Once
	classAtom     uintptr
	classErr      error
	className     = windows.StringToUTF16Ptr("WinInfoPanelOverlay")
)

// NewLayeredWindow prepares an overlay. It does not exist on screen until Run
// is called.
func NewLayeredWindow(opts LayeredWindowOptions) *LayeredWindow {
	if opts.Width < 1 {
		opts.Width = 1
	}
	if opts.Height < 1 {
		opts.Height = 1
	}
	return &LayeredWindow{
		opts:  opts,
		ready: make(chan struct{}),
		done:  make(chan struct{}),
	}
}

// Run creates the window and pumps its messages until Close is called or the
// window is destroyed.
//
// It blocks, and must be called on its own goroutine.
func (w *LayeredWindow) Run() error {
	// Win32 delivers messages to the creating thread only, so this goroutine
	// must stay on one OS thread for the window's whole life.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(w.done)

	if err := ensureWindowClass(); err != nil {
		return err
	}
	if err := w.create(); err != nil {
		return err
	}
	defer w.destroy()

	close(w.ready)
	return w.pump()
}

// Ready blocks until the window exists, so callers can push a first frame
// without racing creation.
func (w *LayeredWindow) Ready() <-chan struct{} { return w.ready }

// Done is closed once the window has been torn down.
func (w *LayeredWindow) Done() <-chan struct{} { return w.done }

// ensureWindowClass registers the overlay window class.
func ensureWindowClass() error {
	classNameOnce.Do(func() {
		instance, err := moduleHandle()
		if err != nil {
			classErr = err
			return
		}

		cursor, _, _ := procLoadCursorW.Call(0, uintptr(unsafe.Pointer(makeIntResource(idcArrow))))

		class := wndClassEx{
			Size:      uint32(unsafe.Sizeof(wndClassEx{})),
			WndProc:   windows.NewCallback(windowProc),
			Instance:  instance,
			Cursor:    windows.Handle(cursor),
			ClassName: className,
		}

		atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class)))
		if atom == 0 {
			classErr = fmt.Errorf("register window class: %w", err)
			return
		}
		classAtom = atom
	})
	return classErr
}

// create makes the window and its backing bitmap.
func (w *LayeredWindow) create() error {
	instance, err := moduleHandle()
	if err != nil {
		return err
	}

	// The overlay never takes focus and stays out of the taskbar: it is a
	// readout, not something to interact with as an application window.
	exStyle := uintptr(wsExLayered | wsExToolWindow | wsExNoActivate)
	if w.opts.Topmost {
		exStyle |= wsExTopmost
	}
	if w.opts.ClickThrough {
		exStyle |= wsExTransparent
	}

	hwnd, _, err := procCreateWindowExW.Call(
		exStyle,
		classAtom,
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(w.opts.Title))),
		uintptr(wsPopup),
		uintptr(int32(w.opts.X)),
		uintptr(int32(w.opts.Y)),
		uintptr(int32(w.opts.Width)),
		uintptr(int32(w.opts.Height)),
		0, 0,
		uintptr(instance),
		0,
	)
	if hwnd == 0 {
		return fmt.Errorf("create overlay window: %w", err)
	}
	w.hwnd = windows.Handle(hwnd)

	// Attach the Go object so the window procedure can find it.
	procSetWindowLongPtrW.Call(hwnd, gwlpUserData, uintptr(unsafe.Pointer(w)))

	if err := w.resizeSurface(w.opts.Width, w.opts.Height); err != nil {
		return err
	}

	procShowWindow.Call(hwnd, swShowNoActivate)
	return nil
}

// resizeSurface rebuilds the device context and DIB section behind the frame.
func (w *LayeredWindow) resizeSurface(width, height int) error {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	if w.memDC != 0 && w.width == width && w.height == height {
		return nil
	}

	w.releaseSurface()

	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return fmt.Errorf("get screen device context")
	}
	defer procReleaseDC.Call(0, screenDC)

	memDC, _, err := procCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		return fmt.Errorf("create memory device context: %w", err)
	}

	info := bitmapInfo{
		Header: bitmapInfoHeader{
			Size:  uint32(unsafe.Sizeof(bitmapInfoHeader{})),
			Width: int32(width),
			// A negative height makes the DIB top-down, matching the row order
			// of an image.RGBA. Without it every frame would appear flipped.
			Height:   int32(-height),
			Planes:   1,
			BitCount: 32,
		},
	}

	var bits unsafe.Pointer
	bitmap, _, err := procCreateDIBSection.Call(
		memDC,
		uintptr(unsafe.Pointer(&info)),
		dibRGBColors,
		uintptr(unsafe.Pointer(&bits)),
		0, 0,
	)
	if bitmap == 0 {
		procDeleteDC.Call(memDC)
		return fmt.Errorf("create DIB section: %w", err)
	}

	oldBitmap, _, _ := procSelectObject.Call(memDC, bitmap)

	w.memDC = windows.Handle(memDC)
	w.bitmap = windows.Handle(bitmap)
	w.oldBitmap = windows.Handle(oldBitmap)
	w.pixels = unsafe.Slice((*byte)(bits), width*height*4)
	w.width = width
	w.height = height
	return nil
}

// releaseSurface frees the device context and bitmap.
func (w *LayeredWindow) releaseSurface() {
	if w.memDC == 0 {
		return
	}
	if w.oldBitmap != 0 {
		procSelectObject.Call(uintptr(w.memDC), uintptr(w.oldBitmap))
	}
	if w.bitmap != 0 {
		procDeleteObject.Call(uintptr(w.bitmap))
	}
	procDeleteDC.Call(uintptr(w.memDC))

	w.memDC, w.bitmap, w.oldBitmap = 0, 0, 0
	w.pixels = nil
	w.width, w.height = 0, 0
}

// Present queues a frame and asks the window thread to paint it.
//
// It is safe to call from any goroutine and never blocks on rendering: a frame
// pushed while another is still being painted simply replaces it, so a slow
// compositor drops frames instead of stalling the render loop.
func (w *LayeredWindow) Present(frame *image.RGBA) {
	if frame == nil {
		return
	}

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.pending = frame
	hwnd := w.hwnd
	w.mu.Unlock()

	if hwnd != 0 {
		procPostMessageW.Call(uintptr(hwnd), wmAppRedraw, 0, 0)
	}
}

// paint composites the queued frame onto the desktop. It runs on the window
// thread.
func (w *LayeredWindow) paint() {
	w.mu.Lock()
	frame := w.pending
	w.pending = nil
	w.mu.Unlock()

	if frame == nil || w.hwnd == 0 {
		return
	}

	bounds := frame.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width == 0 || height == 0 {
		return
	}

	if err := w.resizeSurface(width, height); err != nil {
		return
	}

	// UpdateLayeredWindow expects premultiplied BGRA; image.RGBA is
	// premultiplied RGBA, so only the channel order has to change.
	copyRGBAToBGRA(w.pixels, frame.Pix, width, height, frame.Stride)

	position := point{X: int32(w.opts.X), Y: int32(w.opts.Y)}
	extent := size{CX: int32(width), CY: int32(height)}
	source := point{}
	blend := blendFunction{
		BlendOp:             acSrcOver,
		SourceConstantAlpha: 255,
		AlphaFormat:         acSrcAlpha,
	}

	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return
	}
	defer procReleaseDC.Call(0, screenDC)

	procUpdateLayeredWin.Call(
		uintptr(w.hwnd),
		screenDC,
		uintptr(unsafe.Pointer(&position)),
		uintptr(unsafe.Pointer(&extent)),
		uintptr(w.memDC),
		uintptr(unsafe.Pointer(&source)),
		0,
		uintptr(unsafe.Pointer(&blend)),
		ulwAlpha,
	)
}

// copyRGBAToBGRA swaps the red and blue channels into the DIB.
//
// Both formats are premultiplied and four bytes per pixel, so this is a
// channel reorder rather than a conversion. It is the hottest loop in the
// present path, hence the manual indexing.
func copyRGBAToBGRA(dst, src []byte, width, height, srcStride int) {
	dstStride := width * 4

	for y := 0; y < height; y++ {
		srcRow := src[y*srcStride:]
		dstRow := dst[y*dstStride:]

		for x := 0; x < width; x++ {
			i := x * 4
			dstRow[i+0] = srcRow[i+2] // B
			dstRow[i+1] = srcRow[i+1] // G
			dstRow[i+2] = srcRow[i+0] // R
			dstRow[i+3] = srcRow[i+3] // A
		}
	}
}

// Move repositions the overlay.
func (w *LayeredWindow) Move(x, y int) {
	w.opts.X, w.opts.Y = x, y
	if w.hwnd == 0 {
		return
	}
	procSetWindowPos.Call(uintptr(w.hwnd), 0,
		uintptr(int32(x)), uintptr(int32(y)), 0, 0,
		swpNoSize|swpNoZOrder|swpNoActivate)
}

// SetTopmost raises or lowers the overlay relative to other windows.
func (w *LayeredWindow) SetTopmost(topmost bool) {
	w.opts.Topmost = topmost
	if w.hwnd == 0 {
		return
	}

	insertAfter := hwndNoTopmost
	if topmost {
		insertAfter = hwndTopmost
	}
	procSetWindowPos.Call(uintptr(w.hwnd), insertAfter, 0, 0, 0, 0,
		swpNoMove|swpNoSize|swpNoActivate)
}

// Position returns the overlay's current screen position.
func (w *LayeredWindow) Position() (int, int) {
	if w.hwnd == 0 {
		return w.opts.X, w.opts.Y
	}

	var r rect
	if ret, _, _ := procGetWindowRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&r))); ret == 0 {
		return w.opts.X, w.opts.Y
	}
	return int(r.Left), int(r.Top)
}

// Close tears the window down. It is safe to call more than once, and from any
// goroutine.
func (w *LayeredWindow) Close() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	hwnd := w.hwnd
	w.mu.Unlock()

	if hwnd != 0 {
		procPostMessageW.Call(uintptr(hwnd), wmAppClose, 0, 0)
	}
}

// destroy releases the window and its resources. It runs on the window thread.
func (w *LayeredWindow) destroy() {
	w.releaseSurface()

	if w.hwnd != 0 {
		procDestroyWindow.Call(uintptr(w.hwnd))
		w.mu.Lock()
		w.hwnd = 0
		w.mu.Unlock()
	}
	if w.opts.OnClosed != nil {
		w.opts.OnClosed()
	}
}

// pump runs the message loop until the window is destroyed.
func (w *LayeredWindow) pump() error {
	var m msg
	for {
		ret, _, err := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		switch int32(ret) {
		case -1:
			return fmt.Errorf("message loop failed: %w", err)
		case 0:
			return nil // WM_QUIT
		}

		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// windowProc dispatches messages to the Go window object.
func windowProc(hwnd windows.Handle, message uint32, wParam, lParam uintptr) uintptr {
	stored, _, _ := procGetWindowLongPtrW.Call(uintptr(hwnd), gwlpUserData)
	if stored == 0 {
		ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
		return ret
	}
	w := (*LayeredWindow)(unsafe.Pointer(stored))

	switch message {
	case wmAppRedraw:
		w.paint()
		return 0

	case wmAppClose, wmClose:
		procPostQuitMessage.Call(0)
		return 0

	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0

	case wmNCHitTest:
		// Reporting the whole surface as the title bar is what lets the user
		// drag a window that has no title bar at all.
		if w.opts.Draggable {
			return htCaption
		}
		return htClient

	case wmNCLButtonDown:
		// Let the default handler run the drag, then report where it landed.
		ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
		if w.opts.OnMoved != nil {
			x, y := w.Position()
			w.opts.X, w.opts.Y = x, y
			w.opts.OnMoved(x, y)
		}
		return ret

	case wmRButtonUp, wmNCRButtonUp:
		if w.opts.OnRightClick != nil {
			w.opts.OnRightClick()
		}
		return 0
	}

	ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return ret
}
