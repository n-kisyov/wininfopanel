package turing

import (
	"bytes"
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/n-kisyov/wininfopanel/internal/panels/usb"
)

// fakePanel is a scripted transport: it records what was written and replays
// canned replies.
type fakePanel struct {
	written bytes.Buffer
	replies [][]byte
	closed  bool
}

func (f *fakePanel) Write(b []byte) (int, error) { return f.written.Write(b) }

func (f *fakePanel) Read(b []byte) (int, error) {
	if len(f.replies) == 0 {
		return 0, nil // a timed-out read returns nothing, not an error
	}
	reply := f.replies[0]
	n := copy(b, reply)
	if n >= len(reply) {
		f.replies = f.replies[1:]
	} else {
		f.replies[0] = reply[n:]
	}
	return n, nil
}

func (f *fakePanel) Name() string { return "COM-TEST" }

func (f *fakePanel) Close() error { f.closed = true; return nil }

// respondWith queues a reply, byte by byte, so the handshake's read-until-null
// loop sees it the way the real port delivers it.
func (f *fakePanel) respondWith(s string) {
	for _, b := range []byte(s) {
		f.replies = append(f.replies, []byte{b})
	}
	f.replies = append(f.replies, []byte{0x00})
}

func TestBlockWriterEmitsWholeBlocksOnly(t *testing.T) {
	var sink bytes.Buffer
	w := newBlockWriter(&sink)

	// One byte more than a single block's payload, so exactly two blocks
	// should come out.
	if err := w.write(bytes.Repeat([]byte{0xAA}, payloadPerBlock+1), 0x00); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.flush(0x00); err != nil {
		t.Fatalf("flush: %v", err)
	}

	if got := sink.Len(); got != 2*blockSize {
		t.Fatalf("wrote %d bytes, want %d (two whole blocks)", got, 2*blockSize)
	}

	out := sink.Bytes()

	// The first block is full payload plus one pad byte.
	for i := 0; i < payloadPerBlock; i++ {
		if out[i] != 0xAA {
			t.Fatalf("block 1 byte %d = %#x, want 0xAA", i, out[i])
		}
	}
	if out[payloadPerBlock] != 0x00 {
		t.Errorf("block 1 pad byte = %#x, want 0x00", out[payloadPerBlock])
	}

	// The second carries the one leftover byte, then padding.
	if out[blockSize] != 0xAA {
		t.Errorf("block 2 byte 0 = %#x, want 0xAA", out[blockSize])
	}
	for i := blockSize + 1; i < 2*blockSize; i++ {
		if out[i] != 0x00 {
			t.Fatalf("block 2 byte %d = %#x, want padding", i, out[i])
		}
	}
}

func TestBlockWriterPadsWithTheGivenByte(t *testing.T) {
	// The block that opens a frame is padded with the start byte, not zeroes:
	// the panel expects 250 identical bytes there.
	var sink bytes.Buffer
	w := newBlockWriter(&sink)

	if err := w.writeByte(startDisplay, startDisplay); err != nil {
		t.Fatalf("writeByte: %v", err)
	}
	if err := w.flush(startDisplay); err != nil {
		t.Fatalf("flush: %v", err)
	}

	want := bytes.Repeat([]byte{startDisplay}, blockSize)
	if !bytes.Equal(sink.Bytes(), want) {
		t.Errorf("opening block = %#x..., want %d bytes of %#x",
			sink.Bytes()[:8], blockSize, startDisplay)
	}
}

func TestBlockWriterFlushOnEmptyWritesNothing(t *testing.T) {
	// An empty flush must not emit a block of pure padding: the panel would
	// read it as a frame of its own and lose sync.
	var sink bytes.Buffer
	w := newBlockWriter(&sink)

	if err := w.flush(0x00); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if sink.Len() != 0 {
		t.Errorf("flushing an empty buffer wrote %d bytes, want 0", sink.Len())
	}
}

func TestHelloRejectsAnotherRevision(t *testing.T) {
	panel := &fakePanel{}
	panel.respondWith("chs_35inch_v2")

	device := newDevice(panel)
	err := device.hello()
	if err == nil {
		t.Fatal("hello accepted a panel that is not revision C")
	}
	if !strings.Contains(err.Error(), "chs_35inch_v2") {
		t.Errorf("error does not report what the panel answered: %v", err)
	}
}

func TestHelloAcceptsRevisionC(t *testing.T) {
	panel := &fakePanel{}
	panel.respondWith("chs_5inch_v1.2.3")

	device := newDevice(panel)
	if err := device.hello(); err != nil {
		t.Fatalf("hello: %v", err)
	}
	if device.Firmware() != "chs_5inch_v1.2.3" {
		t.Errorf("Firmware() = %q, want the handshake reply", device.Firmware())
	}

	// The handshake is one command block.
	if got := panel.written.Len(); got != blockSize {
		t.Errorf("handshake wrote %d bytes, want one %d-byte block", got, blockSize)
	}
	if !bytes.HasPrefix(panel.written.Bytes(), cmdHello) {
		t.Error("handshake did not start with the hello command")
	}
}

func TestSendFrameIsAWholeNumberOfBlocks(t *testing.T) {
	panel := &fakePanel{}
	device := newDevice(panel)

	if err := device.sendFrame(make([]byte, FrameSize)); err != nil {
		t.Fatalf("sendFrame: %v", err)
	}

	// Every byte reaching the panel must belong to a complete block; a partial
	// one desynchronises the firmware for good, not just for this frame.
	if got := panel.written.Len(); got%blockSize != 0 {
		t.Errorf("frame wrote %d bytes, not a multiple of the %d-byte block", got, blockSize)
	}

	// The frame opens with a full block of the start byte.
	opening := panel.written.Bytes()[:blockSize]
	if !bytes.Equal(opening, bytes.Repeat([]byte{startDisplay}, blockSize)) {
		t.Error("frame did not open with a block of the start byte")
	}

	// The display command follows it.
	if !bytes.HasPrefix(panel.written.Bytes()[blockSize:], cmdDisplayBitmap) {
		t.Error("the display command does not follow the opening block")
	}
}

func TestDisplayImageRejectsTheWrongSize(t *testing.T) {
	device := newDevice(&fakePanel{})

	err := device.DisplayImage(image.NewRGBA(image.Rect(0, 0, 320, 240)))
	if err == nil {
		t.Fatal("DisplayImage accepted an image that is not the panel's size")
	}
	if !strings.Contains(err.Error(), "320x240") {
		t.Errorf("error does not name the offending size: %v", err)
	}
}

func TestEncodeRGBAProducesOpaqueBGRA(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, Width, Height))

	// A transparent pixel must still reach the panel opaque: it composites
	// nothing, so the overlay's alpha would only darken the frame.
	img.SetRGBA(0, 0, color.RGBA{R: 0x10, G: 0x20, B: 0x30, A: 0x00})
	img.SetRGBA(1, 0, color.RGBA{R: 0xFF, G: 0x00, B: 0x00, A: 0xFF})

	frame := make([]byte, FrameSize)
	EncodeRGBA(frame, img)

	if got := frame[0:4]; !bytes.Equal(got, []byte{0x30, 0x20, 0x10, 0xff}) {
		t.Errorf("pixel 0 = %#x, want B,G,R,255 = 30 20 10 ff", got)
	}
	if got := frame[4:8]; !bytes.Equal(got, []byte{0x00, 0x00, 0xFF, 0xff}) {
		t.Errorf("pixel 1 = %#x, want B,G,R,255 = 00 00 ff ff", got)
	}
}

func TestEncodeRGBACoversEveryPixel(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, Width, Height))
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 1, G: 2, B: 3, A: 255})
		}
	}

	frame := make([]byte, FrameSize)
	EncodeRGBA(frame, img)

	// A stride bug shows up as untouched bytes at the end of each row.
	for i := 0; i < len(frame); i += bytesPerPixel {
		if frame[i] != 3 || frame[i+1] != 2 || frame[i+2] != 1 || frame[i+3] != 0xff {
			t.Fatalf("pixel at byte %d = %#x, want 03 02 01 ff", i, frame[i:i+4])
		}
	}
}

func TestIdentifySeparatesTheRevisions(t *testing.T) {
	revision, supported := identify(deviceInfo(vendorTI, productRevC, "UsbMonitor"))
	if revision != RevisionC || !supported {
		t.Errorf("revision C = (%q, %v), want (%q, true)", revision, supported, RevisionC)
	}

	// The QinHeng variant must be recognised and refused, not driven blind: it
	// accepts every write and answers nothing.
	revision, supported = identify(deviceInfo(vendorQinHeng, productQinHeng, "UsbMonitor"))
	if revision != RevisionQinHeng {
		t.Errorf("QinHeng variant identified as %q, want %q", revision, RevisionQinHeng)
	}
	if supported {
		t.Error("the QinHeng variant is reported as supported, but its protocol is undocumented")
	}

	revision, supported = identify(deviceInfo(0x1A86, 0x7523, "USB Serial"))
	if revision != RevisionUnknown || supported {
		t.Errorf("a plain serial adapter = (%q, %v), want (%q, false)", revision, supported, RevisionUnknown)
	}
}

func TestUnsupportedExplainsTheQinHengVariant(t *testing.T) {
	candidate := Candidate{
		PortName: "COM3", VendorID: vendorQinHeng, ProductID: productQinHeng,
		Revision: RevisionQinHeng,
	}

	err := candidate.Unsupported()
	if err == nil {
		t.Fatal("an unsupported panel reported no reason")
	}
	if !strings.Contains(err.Error(), "COM3") {
		t.Errorf("the reason does not name the port: %v", err)
	}

	if (Candidate{Supported: true}).Unsupported() != nil {
		t.Error("a supported panel reported a reason not to drive it")
	}
}

// deviceInfo builds a usb.DeviceInfo for the detection tests.
func deviceInfo(vid, pid uint16, busDescription string) usb.DeviceInfo {
	return usb.DeviceInfo{
		VendorID:       vid,
		ProductID:      pid,
		BusDescription: busDescription,
		PortName:       "COM9",
	}
}
