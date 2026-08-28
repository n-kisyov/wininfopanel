// Package turing drives Turing Smart Screen panels, also sold under the TURZX
// brand, over their USB serial port.
//
// This implements revision C, the 800x480 5-inch panel. The protocol is
// undocumented; the wire format here is ported from TuringSmartScreenLib
// (github.com/usausa/turing-smart-screen, MIT), which is the same library
// InfoPanel drives these panels with, cross-checked against
// turing-smart-screen-python. Revisions A, B, and E speak different protocols
// and are not handled.
//
// The panel is a serial device, so unlike BeadaPanel it is reached through
// internal/panels/serial rather than WinUSB.
package turing

// The panel's fixed geometry. Revision C is a 5-inch landscape panel and does
// not report or negotiate its size.
const (
	Width  = 800
	Height = 480
)

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

// Commands. Each is a fixed prefix; those that carry an argument append it
// immediately, and every one is written into the 250-byte block stream.
//
// The 0xef 0x69 in each is the family's signature, which is also the
// terminator of a partial bitmap payload.
var (
	cmdHello           = []byte{0x01, 0xef, 0x69, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0xc5, 0xd3}
	cmdSetBrightness   = []byte{0x7b, 0xef, 0x69, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00}
	cmdPreUpdateBitmap = []byte{0x86, 0xef, 0x69, 0x00, 0x00, 0x00, 0x01}
	cmdQueryStatus     = []byte{0xcf, 0xef, 0x69, 0x00, 0x00, 0x00, 0x01}

	// cmdDisplayBitmap ends in the frame's size in 64-pixel units:
	// 800*480/64 = 6000 = 0x1770. It is therefore specific to the 5-inch
	// panel; the 2-inch and 8-inch models send their own totals here.
	cmdDisplayBitmap = []byte{0xc8, 0xef, 0x69, 0x00, 0x17, 0x70}
)

// startDisplay opens a frame. It is written alone in a block padded with
// itself rather than with zeroes, so the panel sees 250 identical bytes.
const startDisplay = 0x2c

// helloResponse is the prefix a revision C 5-inch panel answers the hello
// command with. Other revisions answer with their own model string, which is
// how a mismatched panel is caught at open time rather than after a frame has
// been sent to it.
const helloResponse = "chs_5inch"

// bytesPerPixel is the size of one pixel on the wire: blue, green, red, and an
// alpha byte the panel requires but ignores.
const bytesPerPixel = 4

// FrameSize is the length of one encoded full-screen frame.
const FrameSize = Width * Height * bytesPerPixel
