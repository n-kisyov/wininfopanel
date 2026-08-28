package turing

import (
	"fmt"
	"image"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/logging"
	"github.com/n-kisyov/wininfopanel/internal/panels/serial"
)

// Device is an open connection to a Turing panel.
//
// It is not safe for concurrent use: a frame is a long sequence of blocks and
// two writers would interleave into nonsense. Callers that render from more
// than one goroutine serialise on the mutex Device already holds.
type Device struct {
	log  *slog.Logger
	port transport
	out  *blockWriter

	mu sync.Mutex

	// firmware is the model string the panel answered hello with.
	firmware string
	closed   bool
}

// transport is the panel's byte channel. serial.Port satisfies it; a test
// supplies a scripted one.
type transport interface {
	io.ReadWriter
	Name() string
	Close() error
}

// Options configures the connection.
type Options struct {
	// PortName is the COM port the panel is attached to, e.g. "COM3".
	PortName string

	// ReadTimeout bounds waiting for a status reply.
	ReadTimeout time.Duration
	// WriteTimeout bounds a single block write.
	WriteTimeout time.Duration
}

// Open connects to a panel and completes the handshake.
//
// A panel that answers with another model's string is rejected here rather
// than driven blind: the revisions share a signature but not a wire format.
func Open(opts Options) (*Device, error) {
	if opts.PortName == "" {
		return nil, fmt.Errorf("turing: no serial port given")
	}

	port, err := serial.Open(opts.PortName, serial.Options{
		BaudRate:     115200,
		ReadTimeout:  opts.ReadTimeout,
		WriteTimeout: opts.WriteTimeout,
	})
	if err != nil {
		return nil, err
	}

	device := newDevice(port)

	// Whatever a previous owner left queued would otherwise be read as this
	// session's handshake reply.
	if err := port.Discard(); err != nil {
		port.Close()
		return nil, err
	}

	if err := device.hello(); err != nil {
		port.Close()
		return nil, err
	}

	device.log.Info("panel opened", "firmware", device.firmware, "size", fmt.Sprintf("%dx%d", Width, Height))
	return device, nil
}

// newDevice wires a device around an open transport.
func newDevice(port transport) *Device {
	return &Device{
		log:  logging.For("panels.turing").With("port", port.Name()),
		port: port,
		out:  newBlockWriter(port),
	}
}

// hello performs the handshake and records the panel's reply.
func (d *Device) hello() error {
	if err := d.out.write(cmdHello, 0x00); err != nil {
		return err
	}
	if err := d.out.flush(0x00); err != nil {
		return err
	}

	response, err := d.readUntilNull()
	if err != nil {
		return err
	}

	d.firmware = response
	if !strings.HasPrefix(response, helloResponse) {
		return fmt.Errorf("turing: panel on %s answered %q, want a %q panel "+
			"(revisions A, B, and E speak a different protocol)",
			d.port.Name(), response, helloResponse)
	}
	return nil
}

// Firmware returns the model and version string from the handshake.
func (d *Device) Firmware() string { return d.firmware }

// Port returns the serial port name the panel is on.
func (d *Device) Port() string { return d.port.Name() }

// SetBrightness sets the backlight, 0 to 100.
func (d *Device) SetBrightness(percent int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}

	if err := d.out.write(cmdSetBrightness, 0x00); err != nil {
		return err
	}
	if err := d.out.writeByte(byte(percent), 0x00); err != nil {
		return err
	}
	return d.out.flush(0x00)
}

// Clear fills the panel with one colour.
func (d *Device) Clear(r, g, b uint8) error {
	frame := make([]byte, FrameSize)
	for i := 0; i < len(frame); i += bytesPerPixel {
		frame[i+0] = b
		frame[i+1] = g
		frame[i+2] = r
		frame[i+3] = 0xff
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sendFrame(frame)
}

// DisplayImage sends one full-screen frame.
//
// The image must be exactly the panel's size. Partial updates exist in the
// protocol but are not implemented: a panel driven by this project is repainted
// whole on every tick anyway, so the block accounting they require would buy
// nothing.
func (d *Device) DisplayImage(img *image.RGBA) error {
	if img == nil {
		return fmt.Errorf("turing: no image given")
	}
	bounds := img.Bounds()
	if bounds.Dx() != Width || bounds.Dy() != Height {
		return fmt.Errorf("turing: image is %dx%d, panel is %dx%d",
			bounds.Dx(), bounds.Dy(), Width, Height)
	}

	frame := make([]byte, FrameSize)
	EncodeRGBA(frame, img)

	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sendFrame(frame)
}

// DisplayFrame sends an already-encoded frame, for callers reusing a buffer
// across repaints rather than allocating one per tick.
func (d *Device) DisplayFrame(frame []byte) error {
	if len(frame) != FrameSize {
		return fmt.Errorf("turing: frame is %d bytes, want %d", len(frame), FrameSize)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sendFrame(frame)
}

// sendFrame writes one whole-screen update. The caller holds the mutex.
//
// The order matters and is not obvious: the pixels go out before the command
// that commits them, and the panel only answers once it has taken the frame.
func (d *Device) sendFrame(frame []byte) error {
	if d.closed {
		return fmt.Errorf("turing: panel on %s is closed", d.port.Name())
	}

	// Opening block: the start byte, padded with itself.
	if err := d.out.writeByte(startDisplay, startDisplay); err != nil {
		return err
	}
	if err := d.out.flush(startDisplay); err != nil {
		return err
	}

	if err := d.out.write(cmdDisplayBitmap, 0x00); err != nil {
		return err
	}
	if err := d.out.flush(0x00); err != nil {
		return err
	}

	if err := d.out.write(frame, 0x00); err != nil {
		return err
	}
	if err := d.out.flush(0x00); err != nil {
		return err
	}

	// The panel acknowledges each of these; the replies are drained rather
	// than parsed, since a full-frame update has no partial-resend path to
	// take if it fails.
	if err := d.command(cmdPreUpdateBitmap); err != nil {
		return err
	}
	return d.command(cmdQueryStatus)
}

// command writes one command block and drains the reply.
func (d *Device) command(cmd []byte) error {
	if err := d.out.write(cmd, 0x00); err != nil {
		return err
	}
	if err := d.out.flush(0x00); err != nil {
		return err
	}
	_, err := d.readResponse()
	return err
}

// readResponse reads one status reply, tolerating a short read: the panel pads
// its replies inconsistently and a timeout with data in hand is a normal end.
func (d *Device) readResponse() (string, error) {
	buffer := make([]byte, readSize)

	n, err := d.port.Read(buffer)
	if err != nil && n == 0 {
		return "", err
	}
	return string(buffer[:n]), nil
}

// readUntilNull reads the handshake reply, which is terminated by a zero byte
// rather than being a fixed length.
func (d *Device) readUntilNull() (string, error) {
	var out []byte
	buffer := make([]byte, 1)

	for len(out) < readSize {
		n, err := d.port.Read(buffer)
		if err != nil {
			return string(out), err
		}
		if n == 0 {
			break // timed out; take what arrived
		}
		if buffer[0] == 0x00 {
			break
		}
		out = append(out, buffer[0])
	}
	return string(out), nil
}

// Close releases the panel.
func (d *Device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return nil
	}
	d.closed = true
	return d.port.Close()
}
