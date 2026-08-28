package turing

import (
	"fmt"
	"strings"

	"github.com/n-kisyov/wininfopanel/internal/panels/usb"
)

// Revision names a panel's protocol generation.
//
// Turing panels sold under the same model name and size have shipped with at
// least four different controllers, and they do not speak a common protocol.
// The vendor and product identifiers are the only thing that distinguishes them
// before a connection is opened.
type Revision string

const (
	// RevisionC is the generation this package drives: the 800x480 5-inch
	// panel built around a Texas Instruments controller.
	RevisionC Revision = "C"

	// RevisionQinHeng is a later variant built around a QinHeng USB chip. It is
	// sold under the same Turing and TURZX names, and in round 2.8-inch panels
	// rebranded by SAMA, but its protocol is undocumented and answers none of
	// the published handshakes. Only the vendor's own Windows application
	// drives it.
	RevisionQinHeng Revision = "QinHeng"

	// RevisionUnknown is a serial device that looks like a panel but does not
	// match a known controller.
	RevisionUnknown Revision = "unknown"
)

// Known controller identifiers.
const (
	// vendorTI and productRevC identify a revision C panel.
	vendorTI    = 0x1CBE
	productRevC = 0x0028

	// vendorQinHeng and productQinHeng identify the unsupported variant.
	vendorQinHeng  = 0x1A86
	productQinHeng = 0xCA21

	// busDescription is the name every one of these panels reports for itself,
	// across revisions. It is what separates a panel from an ordinary serial
	// adapter, but it says nothing about which protocol the panel speaks.
	busDescription = "UsbMonitor"
)

// Candidate is an attached device that presents itself as a Turing panel.
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

	// Revision is the protocol generation, inferred from the identifiers.
	Revision Revision
	// Supported reports whether this package can drive the panel.
	Supported bool
}

// String renders a candidate for logs and CLI output.
func (c Candidate) String() string {
	return fmt.Sprintf("%s (%04X:%04X %s, revision %s)",
		c.PortName, c.VendorID, c.ProductID, c.BusDescription, c.Revision)
}

// Unsupported explains why a panel cannot be driven, or returns nil.
func (c Candidate) Unsupported() error {
	if c.Supported {
		return nil
	}

	if c.Revision == RevisionQinHeng {
		return fmt.Errorf("the panel on %s (%04X:%04X) is the QinHeng variant, "+
			"whose protocol is undocumented: it answers none of the published "+
			"handshakes and is driven only by the vendor's own application. "+
			"Supporting it needs a USB capture of that application talking to the panel",
			c.PortName, c.VendorID, c.ProductID)
	}
	return fmt.Errorf("the panel on %s (%04X:%04X, reported as %q) is not a revision C panel",
		c.PortName, c.VendorID, c.ProductID, c.BusDescription)
}

// Discover lists the attached devices that present themselves as panels,
// whether or not this package can drive them.
//
// An unsupported panel is reported rather than hidden: a panel that is plugged
// in and invisible looks like a wiring fault, and the distinction between
// "nothing attached" and "attached but unsupported" is the whole answer when
// someone asks why their screen is blank.
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

		revision, ok := identify(device)
		if revision == RevisionUnknown && !strings.EqualFold(device.BusDescription, busDescription) {
			continue
		}

		found = append(found, Candidate{
			PortName:       device.PortName,
			Serial:         device.Serial,
			VendorID:       device.VendorID,
			ProductID:      device.ProductID,
			BusDescription: device.BusDescription,
			Revision:       revision,
			Supported:      ok,
		})
	}
	return found, nil
}

// identify maps a device's identifiers to a protocol generation.
func identify(device usb.DeviceInfo) (Revision, bool) {
	switch {
	case device.VendorID == vendorTI && device.ProductID == productRevC:
		return RevisionC, true
	case device.VendorID == vendorQinHeng && device.ProductID == productQinHeng:
		return RevisionQinHeng, false
	default:
		return RevisionUnknown, false
	}
}

// OpenFirst connects to the first panel this package can drive.
//
// A panel that is attached but unsupported produces its own explanation rather
// than a timeout: the unsupported ones accept the handshake write and simply
// never answer, which is indistinguishable from a dead cable.
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
			return Open(opts)
		}
	}
	return nil, fmt.Errorf("turing: %w", candidates[0].Unsupported())
}
