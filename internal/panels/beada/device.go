package beada

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"sync"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/logging"
	"github.com/n-kisyov/wininfopanel/internal/panels/usb"
)

// Device is an open BeadaPanel.
//
// Frames are sent through Present; the caller renders at whatever rate it
// likes and the device drops nothing, since each Present is a complete frame.
type Device struct {
	log *slog.Logger

	usb  *usb.Device
	info PanelInfo

	mu        sync.Mutex
	streaming bool
	closed    bool

	// frame is reused between presents, since a panel's frame size never
	// changes while it is open and reallocating one per frame at 30fps is pure
	// garbage.
	frame []byte
}

// transferTimeout bounds a single USB transfer.
//
// A panel that stops accepting data must fail the transfer rather than block
// the render loop indefinitely.
const transferTimeout = 2 * time.Second

// Discover finds the attached BeadaPanels and identifies each one.
//
// A device that does not answer the identification query is skipped rather
// than reported: it is either not really a panel or is busy with another
// application, and neither is something to act on.
func Discover() ([]PanelInfo, error) {
	found, err := usb.FindByVIDPID(VendorID, ProductID)
	if err != nil {
		return nil, err
	}

	log := logging.For("panels.beada")

	var panels []PanelInfo
	for _, candidate := range found {
		device, err := usb.Open(candidate)
		if err != nil {
			log.Debug("could not open a candidate panel",
				"path", candidate.Path, "error", err)
			continue
		}

		info, err := identify(device)
		device.Close()

		if err != nil {
			log.Debug("device did not identify as a BeadaPanel",
				"path", candidate.Path, "error", err)
			continue
		}

		// Windows reports a serial number for the device; prefer the panel's
		// own if it gave one, since that is what is printed on the unit.
		if info.SerialNumber == "" {
			info.SerialNumber = candidate.Serial
		}
		panels = append(panels, info)
	}

	return panels, nil
}

// Open connects to a panel by its USB device info.
func Open(candidate usb.DeviceInfo) (*Device, error) {
	device, err := usb.Open(candidate)
	if err != nil {
		return nil, err
	}

	info, err := identify(device)
	if err != nil {
		device.Close()
		return nil, err
	}
	if info.SerialNumber == "" {
		info.SerialNumber = candidate.Serial
	}

	return &Device{
		log:  logging.For("panels.beada").With("model", info.ModelName, "serial", info.SerialNumber),
		usb:  device,
		info: info,
	}, nil
}

// OpenBySerial connects to the attached panel with a given serial number, or
// to the only attached panel when the serial is empty.
func OpenBySerial(serial string) (*Device, error) {
	found, err := usb.FindByVIDPID(VendorID, ProductID)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no BeadaPanel is attached")
	}

	if serial == "" {
		if len(found) > 1 {
			return nil, fmt.Errorf("%d panels are attached; specify which by serial number", len(found))
		}
		return Open(found[0])
	}

	for _, candidate := range found {
		device, err := Open(candidate)
		if err != nil {
			continue
		}
		if device.info.SerialNumber == serial {
			return device, nil
		}
		device.Close()
	}
	return nil, fmt.Errorf("no attached panel has serial number %q", serial)
}

// identify runs the StatusLink handshake.
func identify(device *usb.Device) (PanelInfo, error) {
	in := usb.Endpoint(ControlEndpoint, true)
	out := usb.Endpoint(ControlEndpoint, false)

	for _, endpoint := range []byte{in, out} {
		if err := device.SetTimeout(endpoint, transferTimeout); err != nil {
			return PanelInfo{}, err
		}
	}

	request := BuildStatusMessage(StatusGetPanelInfo, nil)
	if err := device.WriteAll(out, request); err != nil {
		return PanelInfo{}, fmt.Errorf("send identification request: %w", err)
	}

	response := make([]byte, 128)
	n, err := device.Read(in, response)
	if err != nil {
		return PanelInfo{}, fmt.Errorf("read identification response: %w", err)
	}

	return ParsePanelInfo(response[:n])
}

// Info returns the panel's identity.
func (d *Device) Info() PanelInfo { return d.info }

// SetBrightness sets the backlight level, as a percentage.
func (d *Device) SetBrightness(percent int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return fmt.Errorf("panel is closed")
	}

	value := ScaleBrightness(percent, d.info.PanelLinkVersion)
	message := BuildStatusMessage(StatusSetBacklight, []byte{value})

	return d.usb.WriteAll(usb.Endpoint(ControlEndpoint, false), message)
}

// StartStream puts the panel into media streaming mode.
//
// It must be called before the first Present. The format declares the frame
// geometry, so the panel knows how much data makes one frame.
func (d *Device) StartStream() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return fmt.Errorf("panel is closed")
	}
	if d.streaming {
		return nil
	}

	width, height := d.info.Width(), d.info.Height()

	message, err := BuildPanelMessage(PanelStartMediaStream, MediaFormat(width, height, true))
	if err != nil {
		return err
	}

	data := usb.Endpoint(DataEndpoint, false)
	if err := d.usb.SetTimeout(data, transferTimeout); err != nil {
		return err
	}
	// Frames are already whole buffers, so the driver's own buffering is a
	// wasted copy at panel refresh rates.
	if err := d.usb.SetRawIO(data, true); err != nil {
		d.log.Debug("raw IO was refused; continuing with buffered transfers", "error", err)
	}

	if err := d.usb.WriteAll(data, message); err != nil {
		return fmt.Errorf("start media stream: %w", err)
	}

	d.streaming = true
	d.frame = make([]byte, width*height*2) // BGR16 is two bytes per pixel
	d.log.Info("stream started", "width", width, "height", height)
	return nil
}

// StopStream leaves streaming mode.
func (d *Device) StopStream() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed || !d.streaming {
		return nil
	}
	d.streaming = false

	message, err := BuildPanelMessage(PanelEndMediaStream, "")
	if err != nil {
		return err
	}
	return d.usb.WriteAll(usb.Endpoint(DataEndpoint, false), message)
}

// Present sends one frame.
//
// The image must match the panel's dimensions; it is converted to BGR16 in
// place into the reused buffer and written in a single transfer.
func (d *Device) Present(frame *image.RGBA) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return fmt.Errorf("panel is closed")
	}
	if !d.streaming {
		return fmt.Errorf("call StartStream before presenting frames")
	}

	width, height := d.info.Width(), d.info.Height()
	bounds := frame.Bounds()
	if bounds.Dx() != width || bounds.Dy() != height {
		return fmt.Errorf("frame is %dx%d, the panel expects %dx%d",
			bounds.Dx(), bounds.Dy(), width, height)
	}

	EncodeBGR16(d.frame, frame)

	if err := d.usb.WriteAll(usb.Endpoint(DataEndpoint, false), d.frame); err != nil {
		return fmt.Errorf("send frame: %w", err)
	}
	return nil
}

// Reset returns a stuck panel to a usable state.
//
// This is what the separate control channel buys: a panel whose display
// endpoint has wedged can still be reset, without the physical reconnect a
// single-channel device would need.
func (d *Device) Reset() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return fmt.Errorf("panel is closed")
	}
	d.streaming = false

	control := usb.Endpoint(ControlEndpoint, false)
	message := BuildStatusMessage(StatusPanelLinkReset, nil)

	if err := d.usb.WriteAll(control, message); err != nil {
		return fmt.Errorf("reset panel: %w", err)
	}

	// The data endpoint may still be halted from whatever wedged it.
	if err := d.usb.ResetPipe(usb.Endpoint(DataEndpoint, false)); err != nil {
		d.log.Debug("could not reset the data endpoint", "error", err)
	}
	return nil
}

// Close stops streaming and releases the device.
func (d *Device) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	streaming := d.streaming
	d.mu.Unlock()

	if streaming {
		// Best effort: a panel that has already gone away cannot be told to
		// stop, and failing here would mask the close itself.
		if err := d.StopStream(); err != nil {
			d.log.Debug("could not stop the stream cleanly", "error", err)
		}
	}

	d.mu.Lock()
	d.closed = true
	d.frame = nil
	d.mu.Unlock()

	return d.usb.Close()
}

// Run streams frames from a source until the context is cancelled.
//
// The source is called for each frame and may return nil to skip one, which is
// how a paused or unchanged panel avoids spending USB bandwidth.
func (d *Device) Run(ctx context.Context, frameRate int, source func() *image.RGBA) error {
	if frameRate < 1 {
		frameRate = 15
	}

	if err := d.StartStream(); err != nil {
		return err
	}
	defer d.StopStream()

	ticker := time.NewTicker(time.Second / time.Duration(frameRate))
	defer ticker.Stop()

	for {
		if frame := source(); frame != nil {
			if err := d.Present(frame); err != nil {
				return err
			}
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
