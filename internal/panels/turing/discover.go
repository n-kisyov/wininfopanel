package turing

import (
	"fmt"
	"strings"

	"github.com/n-kisyov/wininfopanel/internal/panels/usb"
)

// Known identifiers for a revision C panel.
//
// The panel is a CDC-ACM device, so Windows binds it to usbser.sys and
// describes it as a generic "USB Serial Device". Only the vendor and product
// identifiers and the name the device reports for itself tell it apart from any
// other serial adapter on the machine.
const (
	vendorQinHeng = 0x1A86
	productRevC   = 0xCA21

	// busDescription is what the panel calls itself over the bus.
	busDescription = "UsbMonitor"
)

// Candidate is a serial device that looks like a Turing panel.
type Candidate struct {
	// PortName is the COM port to open, e.g. "COM3".
	PortName string
	// Serial is the device's serial number, stable across reconnects, which is
	// what a saved panel configuration is keyed on.
	Serial string

	VendorID  uint16
	ProductID uint16

	// BusDescription is the name the device reports for itself.
	BusDescription string
}

// String renders a candidate for logs and CLI output.
func (c Candidate) String() string {
	return fmt.Sprintf("%s (%04X:%04X %s, serial %s)",
		c.PortName, c.VendorID, c.ProductID, c.BusDescription, c.Serial)
}

// Discover lists the attached panels that look like revision C.
//
// It matches on the vendor and product identifiers, falling back to the
// self-reported name: the identifiers are shared with other QinHeng serial
// devices, and the name alone is not unique either, but a device answering to
// both is a panel. The handshake in Open is what finally settles it.
func Discover() ([]Candidate, error) {
	devices, err := usb.Enumerate()
	if err != nil {
		return nil, err
	}

	var found []Candidate
	for _, device := range devices {
		if !device.IsSerial() {
			continue
		}
		if !looksLikePanel(device) {
			continue
		}

		found = append(found, Candidate{
			PortName:       device.PortName,
			Serial:         device.Serial,
			VendorID:       device.VendorID,
			ProductID:      device.ProductID,
			BusDescription: device.BusDescription,
		})
	}
	return found, nil
}

// looksLikePanel reports whether a serial device is worth handshaking with.
func looksLikePanel(device usb.DeviceInfo) bool {
	if device.VendorID == vendorQinHeng && device.ProductID == productRevC {
		return true
	}
	return strings.EqualFold(device.BusDescription, busDescription)
}

// OpenFirst connects to the first panel found, which is what a single-panel
// machine wants and what the CLI defaults to.
func OpenFirst(opts Options) (*Device, error) {
	if opts.PortName != "" {
		return Open(opts)
	}

	candidates, err := Discover()
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("turing: no panel found; " +
			"run 'panelctl usb list' to see the attached serial devices")
	}

	opts.PortName = candidates[0].PortName
	return Open(opts)
}
