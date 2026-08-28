package usb

import (
	"fmt"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	winusb = windows.NewLazySystemDLL("winusb.dll")

	procWinUsbInitialize    = winusb.NewProc("WinUsb_Initialize")
	procWinUsbFree          = winusb.NewProc("WinUsb_Free")
	procWinUsbWritePipe     = winusb.NewProc("WinUsb_WritePipe")
	procWinUsbReadPipe      = winusb.NewProc("WinUsb_ReadPipe")
	procWinUsbSetPipePolicy = winusb.NewProc("WinUsb_SetPipePolicy")
	procWinUsbFlushPipe     = winusb.NewProc("WinUsb_FlushPipe")
	procWinUsbResetPipe     = winusb.NewProc("WinUsb_ResetPipe")
)

// Pipe policy identifiers.
const (
	pipeTransferTimeout = 0x03
	rawIO               = 0x07
)

// Endpoint addresses. The high bit marks the direction: set means the device
// sends to the host, clear means the host sends to the device.
const (
	directionIn = 0x80
)

// Endpoint returns the address of a numbered endpoint in the given direction.
func Endpoint(number int, in bool) byte {
	address := byte(number)
	if in {
		address |= directionIn
	}
	return address
}

// Device is an open WinUSB connection.
//
// It is safe for concurrent use: a panel is typically written to by a render
// loop while being polled for status on another endpoint.
type Device struct {
	info DeviceInfo

	mu     sync.Mutex
	file   windows.Handle
	handle uintptr
	closed bool
}

// Open connects to a device by its interface path.
func Open(info DeviceInfo) (*Device, error) {
	pathPtr, err := windows.UTF16PtrFromString(info.Path)
	if err != nil {
		return nil, fmt.Errorf("encode device path: %w", err)
	}

	// FILE_FLAG_OVERLAPPED is required by WinUSB even though the calls here
	// are synchronous; without it the driver rejects the handle.
	file, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OVERLAPPED,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", info.Path, err)
	}

	var handle uintptr
	ret, _, err := procWinUsbInitialize.Call(uintptr(file), uintptr(unsafe.Pointer(&handle)))
	if ret == 0 {
		windows.CloseHandle(file)
		return nil, fmt.Errorf("initialize WinUSB for %s: %w "+
			"(the device may be bound to a different driver)", info.Description, err)
	}

	return &Device{info: info, file: file, handle: handle}, nil
}

// Info returns the device's enumeration metadata.
func (d *Device) Info() DeviceInfo { return d.info }

// SetTimeout bounds how long a transfer on an endpoint may take.
//
// Without it a device that stops responding blocks the calling goroutine
// indefinitely, which for a panel means the render loop stalls rather than
// dropping the device.
func (d *Device) SetTimeout(endpoint byte, timeout time.Duration) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return fmt.Errorf("device is closed")
	}

	value := uint32(timeout / time.Millisecond)
	ret, _, err := procWinUsbSetPipePolicy.Call(
		d.handle,
		uintptr(endpoint),
		pipeTransferTimeout,
		unsafe.Sizeof(value),
		uintptr(unsafe.Pointer(&value)),
	)
	if ret == 0 {
		return fmt.Errorf("set timeout on endpoint %#x: %w", endpoint, err)
	}
	return nil
}

// SetRawIO enables raw transfers on an endpoint, which removes the driver's
// buffering.
//
// Frame data is already assembled in whole buffers, so the extra copy is pure
// overhead at panel refresh rates.
func (d *Device) SetRawIO(endpoint byte, enabled bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return fmt.Errorf("device is closed")
	}

	var value uint8
	if enabled {
		value = 1
	}

	ret, _, err := procWinUsbSetPipePolicy.Call(
		d.handle,
		uintptr(endpoint),
		rawIO,
		unsafe.Sizeof(value),
		uintptr(unsafe.Pointer(&value)),
	)
	if ret == 0 {
		return fmt.Errorf("set raw IO on endpoint %#x: %w", endpoint, err)
	}
	return nil
}

// Write sends data to an endpoint, returning how many bytes were transferred.
func (d *Device) Write(endpoint byte, data []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return 0, fmt.Errorf("device is closed")
	}
	if len(data) == 0 {
		return 0, nil
	}

	var transferred uint32
	ret, _, err := procWinUsbWritePipe.Call(
		d.handle,
		uintptr(endpoint),
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
		uintptr(unsafe.Pointer(&transferred)),
		0, // synchronous
	)
	if ret == 0 {
		return int(transferred), fmt.Errorf("write %d bytes to endpoint %#x: %w",
			len(data), endpoint, err)
	}
	return int(transferred), nil
}

// WriteAll sends the whole buffer, continuing across partial transfers.
//
// A frame can be several hundred kilobytes, which the driver may split; a
// short write left unfinished would tear the image on the panel.
func (d *Device) WriteAll(endpoint byte, data []byte) error {
	for sent := 0; sent < len(data); {
		n, err := d.Write(endpoint, data[sent:])
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("write to endpoint %#x stalled after %d of %d bytes",
				endpoint, sent, len(data))
		}
		sent += n
	}
	return nil
}

// Read receives data from an endpoint.
func (d *Device) Read(endpoint byte, buffer []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return 0, fmt.Errorf("device is closed")
	}
	if len(buffer) == 0 {
		return 0, nil
	}

	var transferred uint32
	ret, _, err := procWinUsbReadPipe.Call(
		d.handle,
		uintptr(endpoint),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
		uintptr(unsafe.Pointer(&transferred)),
		0,
	)
	if ret == 0 {
		return int(transferred), fmt.Errorf("read from endpoint %#x: %w", endpoint, err)
	}
	return int(transferred), nil
}

// FlushPipe discards buffered data on an endpoint.
func (d *Device) FlushPipe(endpoint byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return fmt.Errorf("device is closed")
	}
	if ret, _, err := procWinUsbFlushPipe.Call(d.handle, uintptr(endpoint)); ret == 0 {
		return fmt.Errorf("flush endpoint %#x: %w", endpoint, err)
	}
	return nil
}

// ResetPipe clears a stalled endpoint.
//
// A panel that has been asked for more than it can take can leave its endpoint
// halted; resetting is what recovers it without a physical reconnect.
func (d *Device) ResetPipe(endpoint byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return fmt.Errorf("device is closed")
	}
	if ret, _, err := procWinUsbResetPipe.Call(d.handle, uintptr(endpoint)); ret == 0 {
		return fmt.Errorf("reset endpoint %#x: %w", endpoint, err)
	}
	return nil
}

// Close releases the device.
func (d *Device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return nil
	}
	d.closed = true

	procWinUsbFree.Call(d.handle)
	err := windows.CloseHandle(d.file)

	d.handle = 0
	d.file = 0

	if err != nil {
		return fmt.Errorf("close device handle: %w", err)
	}
	return nil
}
