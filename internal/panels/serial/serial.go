// Package serial drives a Windows COM port, the transport the Turing,
// Thermalright, and Thermaltake panel families use.
//
// It is the sibling of internal/panels/usb: that package talks to devices
// bound to WinUSB, this one to devices bound to a serial class driver. A panel
// is reachable through exactly one of them, decided by the driver Windows
// attached, not by anything about the panel itself.
//
// The comm API is already bound by x/sys/windows, so unlike the WinUSB side
// there is nothing to hand-bind here.
package serial

import (
	"fmt"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// DCB flag bits. The DCB packs its booleans into one word, and x/sys/windows
// exposes the word without naming the fields.
const (
	dcbBinary = 1 << 0
	// fDtrControl and fRtsControl are two-bit fields; 1 is _CONTROL_ENABLE,
	// which asserts the line and leaves it asserted.
	dcbDTRControlEnable = 1 << 4
	dcbRTSControlEnable = 1 << 12
)

// PurgeComm flags.
const (
	purgeTXClear = 0x0004
	purgeRXClear = 0x0008
)

// Serial framing constants, spelled out because x/sys/windows does not name
// them either.
const (
	noParity    = 0
	oneStopBit  = 0
	eightBits   = 8
	defaultBaud = 115200
)

// Options configures a port.
type Options struct {
	// BaudRate is the line rate. For a USB CDC device it is nominal -- the
	// throughput is the USB link's, not the number's -- but the device still
	// expects to be told, and refuses to talk if the line coding never arrives.
	BaudRate int

	// ReadTimeout bounds a single Read. A read that times out returns what it
	// has rather than failing, which is how a panel's variable-length status
	// replies are collected without knowing their length in advance.
	ReadTimeout time.Duration
	// WriteTimeout bounds a single Write.
	WriteTimeout time.Duration
}

// Port is an open serial port.
type Port struct {
	handle windows.Handle
	name   string
}

// Open opens a COM port by name, e.g. "COM3".
func Open(name string, opts Options) (*Port, error) {
	if name == "" {
		return nil, fmt.Errorf("serial: no port name given")
	}
	if opts.BaudRate <= 0 {
		opts.BaudRate = defaultBaud
	}
	if opts.ReadTimeout <= 0 {
		opts.ReadTimeout = time.Second
	}
	if opts.WriteTimeout <= 0 {
		opts.WriteTimeout = time.Second
	}

	// Ports past COM9 need the device-namespace form; it is valid for the low
	// numbers too, so it is always used rather than switched on.
	path := name
	if !strings.HasPrefix(path, `\\.\`) {
		path = `\\.\` + name
	}

	handle, err := windows.CreateFile(
		windows.StringToUTF16Ptr(path),
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0, // a port takes one owner at a time
		nil,
		windows.OPEN_EXISTING,
		0, // synchronous
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("serial: open %s: %w", name, err)
	}

	port := &Port{handle: handle, name: name}

	if err := port.configure(opts); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	return port, nil
}

// configure applies the line settings and timeouts.
func (p *Port) configure(opts Options) error {
	dcb := windows.DCB{
		BaudRate: uint32(opts.BaudRate),
		ByteSize: eightBits,
		Parity:   noParity,
		StopBits: oneStopBit,
		// Binary mode is mandatory on Windows -- non-binary is not supported --
		// and both handshake lines are asserted, which is what the panel waits
		// for before it will accept a command.
		Flags: dcbBinary | dcbDTRControlEnable | dcbRTSControlEnable,
	}
	dcb.DCBlength = uint32(unsafe.Sizeof(dcb))

	if err := windows.SetCommState(p.handle, &dcb); err != nil {
		return fmt.Errorf("serial: configure %s: %w", p.name, err)
	}

	timeouts := windows.CommTimeouts{
		ReadTotalTimeoutConstant:  uint32(opts.ReadTimeout.Milliseconds()),
		WriteTotalTimeoutConstant: uint32(opts.WriteTimeout.Milliseconds()),
	}
	if err := windows.SetCommTimeouts(p.handle, &timeouts); err != nil {
		return fmt.Errorf("serial: set timeouts on %s: %w", p.name, err)
	}
	return nil
}

// Name returns the port name, e.g. "COM3".
func (p *Port) Name() string { return p.name }

// Write sends bytes to the port.
func (p *Port) Write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}

	var written uint32
	if err := windows.WriteFile(p.handle, b, &written, nil); err != nil {
		return int(written), fmt.Errorf("serial: write to %s: %w", p.name, err)
	}
	return int(written), nil
}

// Read reads available bytes, returning 0 with no error when the read timed
// out having received nothing.
func (p *Port) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}

	var read uint32
	if err := windows.ReadFile(p.handle, b, &read, nil); err != nil {
		return int(read), fmt.Errorf("serial: read from %s: %w", p.name, err)
	}
	return int(read), nil
}

// Discard throws away whatever is queued in both directions, so a session
// starts against a known-empty port rather than against a previous owner's
// half-finished frame.
func (p *Port) Discard() error {
	if err := windows.PurgeComm(p.handle, purgeTXClear|purgeRXClear); err != nil {
		return fmt.Errorf("serial: purge %s: %w", p.name, err)
	}
	return nil
}

// Close releases the port.
func (p *Port) Close() error {
	if p.handle == windows.InvalidHandle || p.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(p.handle)
	p.handle = windows.InvalidHandle
	if err != nil {
		return fmt.Errorf("serial: close %s: %w", p.name, err)
	}
	return nil
}
