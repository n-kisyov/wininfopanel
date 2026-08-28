package turing

import (
	"fmt"

	"github.com/n-kisyov/wininfopanel/internal/panels/usb"
)

// Panels sold under the Turing name that this package does not drive.
//
// They are recognised so that an attached panel is never reported as absent:
// "nothing found" and "found, but speaks another protocol" are different
// answers, and only the second one tells someone what to do next.
var unsupportedModels = []struct {
	name      string
	vendorID  uint16
	productID uint16
	reason    string
}{
	{
		name: `Turing Smart Screen 3.5"`, vendorID: 0x1A86, productID: 0x5722,
		reason: "it speaks the older revision A protocol, which is not implemented",
	},
	{
		name: `Turing Smart Screen 8" Rev 1.0`, vendorID: 0x0525, productID: 0xA4A7,
		reason: "it speaks a different protocol, which is not implemented",
	},
	{
		name: `Turing Smart Screen 8" Rev 1.1`, vendorID: 0x1CBE, productID: 0x0088,
		reason: "it is driven over raw USB rather than a serial port, which is not implemented",
	},
	{
		// Sold under the Turing and TURZX names and in round panels rebranded
		// by SAMA. Its protocol is published nowhere and it answers none of the
		// known handshakes.
		name: "QinHeng-based panel", vendorID: 0x1A86, productID: 0xCA21,
		reason: "its protocol is undocumented: it answers none of the published " +
			"handshakes and is driven only by the vendor's own application",
	},
}

// Candidate is an attached device that presents itself as a Turing panel.
type Candidate struct {
	// PortName is the COM port to open, e.g. "COM10".
	PortName string
	// Serial is the device's serial number, stable across reconnects, which is
	// what a saved panel configuration is keyed on.
	Serial string

	VendorID  uint16
	ProductID uint16

	// Model is the panel this device was identified as. It is the zero Model
	// when the panel is one this package cannot drive.
	Model Model
	// Supported reports whether this package can drive the panel.
	Supported bool
	// Detail names the panel and, when it cannot be driven, says why.
	Detail string
}

// String renders a candidate for logs and CLI output.
func (c Candidate) String() string {
	return fmt.Sprintf("%s (%04X:%04X) %s", c.PortName, c.VendorID, c.ProductID, c.Detail)
}

// Unsupported explains why a panel cannot be driven, or returns nil.
func (c Candidate) Unsupported() error {
	if c.Supported {
		return nil
	}
	return fmt.Errorf("the panel on %s (%04X:%04X) is a %s: %s",
		c.PortName, c.VendorID, c.ProductID, c.Detail, c.reason())
}

func (c Candidate) reason() string {
	for _, model := range unsupportedModels {
		if model.vendorID == c.VendorID && model.productID == c.ProductID {
			return model.reason
		}
	}
	return "it is not a model this package recognises"
}

// Discover lists the attached devices that are Turing panels, whether or not
// this package can drive them.
//
// Identification is by USB vendor and product identifier. The panels cannot be
// told apart any other way: several enumerate as generic Linux gadget serial
// devices and describe themselves as "Android", which is a property of the
// firmware's USB stack rather than anything about the panel.
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

		candidate, ok := identify(device)
		if !ok {
			continue
		}
		found = append(found, candidate)
	}
	return found, nil
}

// identify maps a serial device to a panel, reporting whether it is one at all.
func identify(device usb.DeviceInfo) (Candidate, bool) {
	candidate := Candidate{
		PortName:  device.PortName,
		Serial:    device.Serial,
		VendorID:  device.VendorID,
		ProductID: device.ProductID,
	}

	for _, model := range []Model{Model5Inch, Model2Inch} {
		if device.VendorID == model.VendorID && device.ProductID == model.ProductID {
			candidate.Model = model
			candidate.Supported = true
			candidate.Detail = model.String()
			return candidate, true
		}
	}

	for _, model := range unsupportedModels {
		if device.VendorID == model.vendorID && device.ProductID == model.productID {
			candidate.Detail = model.name
			return candidate, true
		}
	}
	return Candidate{}, false
}

// OpenFirst connects to the first panel this package can drive.
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

	for _, candidate := range candidates {
		if candidate.Supported {
			opts.PortName = candidate.PortName
			opts.Model = candidate.Model
			return Open(opts)
		}
	}
	return nil, fmt.Errorf("turing: %w", candidates[0].Unsupported())
}
