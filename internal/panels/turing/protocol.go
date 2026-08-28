// Package turing drives Turing Smart Screen panels, also sold under the TURZX
// and XuanFang names, over their USB serial port.
//
// This implements the revision C protocol, which covers the 2.1-inch and
// 5-inch panels. The protocol is undocumented; the wire format here is ported
// from TuringSmartScreenLib (github.com/usausa/turing-smart-screen, MIT), the
// library InfoPanel drives these panels with, and the model table from
// InfoPanel itself. The 3.5-inch panels speak an older, simpler protocol and
// the 8-inch ones a later protocol over raw USB; neither is handled here.
//
// The panel is a serial device, so unlike BeadaPanel it is reached through
// internal/panels/serial rather than WinUSB.
package turing

import "fmt"

// blockSize is the fixed length of every write to the panel.
//
// The panel reads in 250-byte blocks and ignores the last byte of each, so a
// block carries 249 bytes of payload followed by one pad byte. Writing any
// other length desynchronises it: the firmware counts blocks, not bytes.
const blockSize = 250

// payloadPerBlock is how much of a block the panel actually reads.
const payloadPerBlock = blockSize - 1

// readSize is the size of a status reply. The panel pads its replies, so the
// full buffer is read and the meaningful prefix taken from it.
const readSize = 1024

// bytesPerPixel is the size of one pixel on the wire: blue, green, red, and an
// alpha byte the panel requires but ignores.
const bytesPerPixel = 4

// Commands. Each is a fixed prefix; those that carry an argument append it
// immediately, and every one is written into the 250-byte block stream.
//
// The 0xef 0x69 in each is the family's signature.
var (
	cmdHello           = []byte{0x01, 0xef, 0x69, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0xc5, 0xd3}
	cmdSetBrightness   = []byte{0x7b, 0xef, 0x69, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00}
	cmdPreUpdateBitmap = []byte{0x86, 0xef, 0x69, 0x00, 0x00, 0x00, 0x01}
	cmdQueryStatus     = []byte{0xcf, 0xef, 0x69, 0x00, 0x00, 0x00, 0x01}

	// cmdDisplayBitmapPrefix is completed by the frame's size in 64-pixel
	// units, which differs per model.
	cmdDisplayBitmapPrefix = []byte{0xc8, 0xef, 0x69, 0x00}
)

// startDisplay opens a frame. It is written alone in a block padded with
// itself rather than with zeroes, so the panel sees 250 identical bytes.
const startDisplay = 0x2c

// Model describes one panel variant.
//
// Size is not negotiated: the panel neither reports nor accepts a resolution,
// and a frame of the wrong length desynchronises it. The identifiers are
// therefore the only thing that says how big a frame must be, which is why the
// table below is keyed on them.
type Model struct {
	Name          string
	Width, Height int

	VendorID  uint16
	ProductID uint16

	// Handshake is the prefix this model answers the hello command with.
	Handshake string
}

// The models this package drives. Values are InfoPanel's, which is the
// reference for what these panels actually enumerate as.
var (
	// Model5Inch is the 800x480 panel.
	Model5Inch = Model{
		Name: `Turing Smart Screen 5"`, Width: 800, Height: 480,
		VendorID: 0x1D6B, ProductID: 0x0106, Handshake: "chs_5inch",
	}
	// Model2Inch is the 480x480 square panel.
	Model2Inch = Model{
		Name: `Turing Smart Screen 2.1"`, Width: 480, Height: 480,
		VendorID: 0x1D6B, ProductID: 0x0121, Handshake: "chs_2inch",
	}
)

// FrameSize is the length of one encoded full-screen frame.
func (m Model) FrameSize() int { return m.Width * m.Height * bytesPerPixel }

// String names the model and its resolution.
func (m Model) String() string {
	return fmt.Sprintf("%s (%dx%d)", m.Name, m.Width, m.Height)
}

// displayBitmapCommand is the frame command for this model.
//
// It carries the frame's size in 64-pixel units -- 800*480/64 is 6000 on the
// 5-inch panel -- which is what makes the command model-specific rather than
// constant.
func (m Model) displayBitmapCommand() []byte {
	units := m.Width * m.Height / 64

	cmd := make([]byte, 0, len(cmdDisplayBitmapPrefix)+2)
	cmd = append(cmd, cmdDisplayBitmapPrefix...)
	return append(cmd, byte(units>>8), byte(units))
}
