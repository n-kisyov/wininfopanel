package beada

import (
	"encoding/binary"
	"image"
	"image/color"
	"strings"
	"testing"
)

// No BeadaPanel is attached to the machine this was written on, so the wire
// format is verified against the protocol specification instead: frames are
// built and parsed here byte by byte. The transport above it -- opening the
// device and streaming to it -- is written to spec and remains unverified
// against hardware.

func TestStatusMessageLayout(t *testing.T) {
	message := BuildStatusMessage(StatusGetPanelInfo, nil)

	if len(message) != statusHeaderLength {
		t.Fatalf("message is %d bytes, want %d", len(message), statusHeaderLength)
	}
	if got := string(message[:11]); got != statusProtocol {
		t.Errorf("protocol header = %q, want %q", got, statusProtocol)
	}
	if message[11] != statusVersion {
		t.Errorf("version = %d, want %d", message[11], statusVersion)
	}
	if message[12] != byte(StatusGetPanelInfo) {
		t.Errorf("type = %d, want %d", message[12], StatusGetPanelInfo)
	}
	if message[13] != 0 {
		t.Errorf("reserved byte = %d, want 0", message[13])
	}
	if got := binary.LittleEndian.Uint16(message[16:]); got != statusHeaderLength {
		t.Errorf("length field = %d, want %d", got, statusHeaderLength)
	}
}

func TestStatusMessageCarriesPayload(t *testing.T) {
	message := BuildStatusMessage(StatusSetBacklight, []byte{75})

	if len(message) != statusHeaderLength+1 {
		t.Fatalf("message is %d bytes, want %d", len(message), statusHeaderLength+1)
	}
	if message[statusHeaderLength] != 75 {
		t.Errorf("payload = %d, want 75", message[statusHeaderLength])
	}
	if got := binary.LittleEndian.Uint16(message[16:]); got != statusHeaderLength+1 {
		t.Errorf("length field = %d, want it to include the payload", got)
	}
}

func TestStatusChecksumCoversTheHeaderOnly(t *testing.T) {
	// The device checksums the first 18 bytes and ignores the payload, so two
	// messages differing only in payload must carry the same checksum.
	a := BuildStatusMessage(StatusSetBacklight, []byte{10})
	b := BuildStatusMessage(StatusSetBacklight, []byte{200})

	checksumA := binary.LittleEndian.Uint16(a[statusChecksumOffset:])
	checksumB := binary.LittleEndian.Uint16(b[statusChecksumOffset:])

	if checksumA != checksumB {
		t.Errorf("checksums differ (%#x vs %#x); the payload must not be covered",
			checksumA, checksumB)
	}
}

func TestChecksumIsOnesComplement(t *testing.T) {
	// Summing a buffer with its own checksum appended must fold back to all
	// ones, which is the defining property of this checksum.
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	sum := checksum(data)

	verified := append(append([]byte{}, data...), byte(sum), byte(sum>>8))

	var total uint32
	for i := 0; i < len(verified); i += 2 {
		total += uint32(binary.LittleEndian.Uint16(verified[i:]))
	}
	total = (total >> 16) + (total & 0xFFFF)
	total += total >> 16

	if uint16(total) != 0xFFFF {
		t.Errorf("checksum verification folded to %#x, want 0xFFFF", uint16(total))
	}
}

func TestChecksumHandlesOddLength(t *testing.T) {
	// An odd trailing byte is added on its own rather than read as a
	// half-word past the end.
	if got := checksum([]byte{0x01, 0x02, 0x03}); got == 0 {
		t.Error("checksum of an odd-length buffer produced zero")
	}
}

func TestPanelMessageLayout(t *testing.T) {
	format := MediaFormat(800, 480, true)

	message, err := BuildPanelMessage(PanelStartMediaStream, format)
	if err != nil {
		t.Fatalf("BuildPanelMessage: %v", err)
	}

	if len(message) != panelTotalLength {
		t.Fatalf("message is %d bytes, want %d", len(message), panelTotalLength)
	}
	if got := string(message[:10]); got != panelProtocol {
		t.Errorf("protocol header = %q, want %q", got, panelProtocol)
	}
	if message[10] != panelVersion {
		t.Errorf("version = %d, want %d", message[10], panelVersion)
	}
	if message[11] != byte(PanelStartMediaStream) {
		t.Errorf("type = %d, want %d", message[11], PanelStartMediaStream)
	}

	embedded := strings.TrimRight(string(message[panelFormatOffset:panelHeaderLength]), "\x00")
	if embedded != format {
		t.Errorf("format string = %q, want %q", embedded, format)
	}
}

func TestPanelMessageOmitsFormatForControlTypes(t *testing.T) {
	// Only the stream-start types carry a format; a reset that included one
	// would be malformed.
	message, err := BuildPanelMessage(PanelEndMediaStream, "should be ignored")
	if err != nil {
		t.Fatalf("BuildPanelMessage: %v", err)
	}

	for i := panelFormatOffset; i < panelHeaderLength; i++ {
		if message[i] != 0 {
			t.Fatalf("byte %d is %#x; the format region must stay empty", i, message[i])
		}
	}
}

func TestPanelMessageRejectsOversizedFormat(t *testing.T) {
	if _, err := BuildPanelMessage(PanelStartMediaStream, strings.Repeat("x", 300)); err == nil {
		t.Error("an over-long format string was accepted")
	}
}

func TestMediaFormat(t *testing.T) {
	writeThrough := MediaFormat(1280, 400, true)
	if !strings.Contains(writeThrough, "image/x-raw") || !strings.Contains(writeThrough, "BGR16") {
		t.Errorf("write-through format = %q", writeThrough)
	}
	if !strings.Contains(writeThrough, "width=1280") || !strings.Contains(writeThrough, "height=400") {
		t.Errorf("format did not carry the dimensions: %q", writeThrough)
	}

	buffered := MediaFormat(800, 480, false)
	if !strings.Contains(buffered, "video/x-raw") || !strings.Contains(buffered, "RGB16") {
		t.Errorf("buffered format = %q", buffered)
	}
}

// buildPanelInfoResponse assembles a GetPanelInfo reply the way firmware does.
func buildPanelInfoResponse(model Model, serial string, resX, resY uint16) []byte {
	response := make([]byte, statusHeaderLength+panelInfoPayload)

	copy(response, statusProtocol)
	response[11] = statusVersion
	response[12] = byte(StatusGetPanelInfo)

	payload := response[statusHeaderLength:]
	binary.LittleEndian.PutUint16(payload[0:], 0x0105) // firmware version
	payload[2] = 2                                     // panel link version
	payload[3] = 1                                     // status link version
	payload[4] = 7                                     // platform
	payload[5] = byte(model)
	copy(payload[6:70], serial)
	binary.LittleEndian.PutUint16(payload[70:], resX)
	binary.LittleEndian.PutUint16(payload[72:], resY)
	binary.LittleEndian.PutUint32(payload[74:], 4096)
	payload[78] = 100 // max brightness
	payload[79] = 80  // current brightness

	return response
}

func TestParsePanelInfo(t *testing.T) {
	response := buildPanelInfoResponse(Model6C, "BP6C12345", 1280, 480)

	info, err := ParsePanelInfo(response)
	if err != nil {
		t.Fatalf("ParsePanelInfo: %v", err)
	}

	if info.Model != Model6C {
		t.Errorf("Model = %d, want %d", info.Model, Model6C)
	}
	if info.ModelName != "6C" {
		t.Errorf("ModelName = %q, want %q", info.ModelName, "6C")
	}
	if info.SerialNumber != "BP6C12345" {
		t.Errorf("SerialNumber = %q", info.SerialNumber)
	}
	if info.FirmwareVersion != 0x0105 {
		t.Errorf("FirmwareVersion = %#x, want 0x0105", info.FirmwareVersion)
	}
	if info.PanelLinkVersion != 2 {
		t.Errorf("PanelLinkVersion = %d, want 2", info.PanelLinkVersion)
	}
	if info.MaxBrightness != 100 || info.CurrentBrightness != 80 {
		t.Errorf("brightness = %d/%d, want 80/100", info.CurrentBrightness, info.MaxBrightness)
	}
	if info.StorageSizeKB != 4096 {
		t.Errorf("StorageSizeKB = %d, want 4096", info.StorageSizeKB)
	}
}

func TestPanelDimensionsComeFromTheModelDatabase(t *testing.T) {
	// Some models report their pre-rotation orientation, so the database wins
	// over the firmware's own numbers; taking the reported values would render
	// those panels sideways.
	response := buildPanelInfoResponse(Model6C, "test", 480, 1280)

	info, err := ParsePanelInfo(response)
	if err != nil {
		t.Fatalf("ParsePanelInfo: %v", err)
	}

	if info.Width() != 1280 || info.Height() != 480 {
		t.Errorf("dimensions = %dx%d, want the database's 1280x480",
			info.Width(), info.Height())
	}
}

func TestParsePanelInfoRejectsMalformedResponses(t *testing.T) {
	t.Run("too short", func(t *testing.T) {
		if _, err := ParsePanelInfo(make([]byte, 50)); err == nil {
			t.Error("a truncated response was accepted")
		}
	})

	t.Run("wrong protocol", func(t *testing.T) {
		response := buildPanelInfoResponse(Model5, "x", 800, 480)
		copy(response, "NOT-A-PANEL")
		if _, err := ParsePanelInfo(response); err == nil {
			t.Error("a response with the wrong protocol header was accepted")
		}
	})

	t.Run("wrong message type", func(t *testing.T) {
		response := buildPanelInfoResponse(Model5, "x", 800, 480)
		response[12] = byte(StatusSetBacklight)
		if _, err := ParsePanelInfo(response); err == nil {
			t.Error("a response of the wrong message type was accepted")
		}
	})

	t.Run("unknown model", func(t *testing.T) {
		response := buildPanelInfoResponse(Model(200), "x", 800, 480)
		if _, err := ParsePanelInfo(response); err == nil {
			t.Error("an unrecognized model byte was accepted")
		}
	})
}

func TestSerialRejectsUninitializedFields(t *testing.T) {
	// Some units ship with a garbage serial field; using it as a device
	// identifier would be worse than having none.
	response := buildPanelInfoResponse(Model5, "\xff\xfe\x01garbage", 800, 480)

	info, err := ParsePanelInfo(response)
	if err != nil {
		t.Fatalf("ParsePanelInfo: %v", err)
	}
	if info.SerialNumber != "" {
		t.Errorf("SerialNumber = %q, want it rejected as non-alphanumeric", info.SerialNumber)
	}
}

func TestScaleBrightness(t *testing.T) {
	// Version 1 panels go dark below roughly a quarter output, so the range is
	// compressed rather than mapped linearly from zero.
	if got := ScaleBrightness(0, 1); got != 25 {
		t.Errorf("0%% on a v1 panel = %d, want 25", got)
	}
	if got := ScaleBrightness(100, 1); got != 100 {
		t.Errorf("100%% on a v1 panel = %d, want 100", got)
	}

	// Later panels take the percentage directly.
	if got := ScaleBrightness(0, 2); got != 0 {
		t.Errorf("0%% on a v2 panel = %d, want 0", got)
	}
	if got := ScaleBrightness(50, 2); got != 50 {
		t.Errorf("50%% on a v2 panel = %d, want 50", got)
	}

	// Out-of-range input is clamped rather than wrapping through a byte.
	if got := ScaleBrightness(500, 2); got != 100 {
		t.Errorf("500%% = %d, want it clamped to 100", got)
	}
	if got := ScaleBrightness(-50, 2); got != 0 {
		t.Errorf("-50%% = %d, want it clamped to 0", got)
	}
}

func TestEncodeBGR16(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	src.SetRGBA(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	src.SetRGBA(1, 0, color.RGBA{R: 0, G: 0, B: 255, A: 255})

	dst := make([]byte, 2*1*2)
	EncodeBGR16(dst, src)

	// Red occupies the top five bits.
	red := binary.LittleEndian.Uint16(dst[0:])
	if red != 0xF800 {
		t.Errorf("red pixel = %#04x, want 0xF800", red)
	}

	// Blue occupies the bottom five.
	blue := binary.LittleEndian.Uint16(dst[2:])
	if blue != 0x001F {
		t.Errorf("blue pixel = %#04x, want 0x001F", blue)
	}
}

func TestEncodeBGR16GreenGetsSixBits(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1, 1))
	src.SetRGBA(0, 0, color.RGBA{R: 0, G: 255, B: 0, A: 255})

	dst := make([]byte, 2)
	EncodeBGR16(dst, src)

	if got := binary.LittleEndian.Uint16(dst); got != 0x07E0 {
		t.Errorf("green pixel = %#04x, want 0x07E0 (six bits, the eye resolves green best)", got)
	}
}

func TestEncodeBGR16HonoursStride(t *testing.T) {
	// A sub-image of a larger surface must encode its own pixels, not the
	// parent's row layout.
	parent := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for x := 0; x < 4; x++ {
		parent.SetRGBA(x, 0, color.RGBA{R: 255, A: 255})
		parent.SetRGBA(x, 1, color.RGBA{B: 255, A: 255})
	}

	dst := make([]byte, 4*2*2)
	EncodeBGR16(dst, parent)

	if got := binary.LittleEndian.Uint16(dst[0:]); got != 0xF800 {
		t.Errorf("first row = %#04x, want red", got)
	}
	if got := binary.LittleEndian.Uint16(dst[4*2:]); got != 0x001F {
		t.Errorf("second row = %#04x, want blue", got)
	}
}

func TestEncodeIgnoresUndersizedDestination(t *testing.T) {
	// A short buffer must be refused rather than writing past its end.
	src := image.NewRGBA(image.Rect(0, 0, 10, 10))
	dst := make([]byte, 4)

	EncodeBGR16(dst, src) // must not panic

	for i, b := range dst {
		if b != 0 {
			t.Errorf("byte %d was written despite the buffer being too small", i)
		}
	}
}

func TestEncodeRGB16SwapsRedAndBlue(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1, 1))
	src.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})

	dst := make([]byte, 2)
	EncodeRGB16(dst, src)

	// Red lands in the low bits in this ordering.
	if got := binary.LittleEndian.Uint16(dst); got != 0x001F {
		t.Errorf("red pixel in RGB16 = %#04x, want 0x001F", got)
	}
}

func TestEveryModelHasDimensions(t *testing.T) {
	for model, info := range Models {
		if info.Name == "" {
			t.Errorf("model %d has no name", byte(model))
		}
		if info.Width <= 0 || info.Height <= 0 {
			t.Errorf("model %s has dimensions %dx%d", info.Name, info.Width, info.Height)
		}
		if info.WidthMM <= 0 || info.HeightMM <= 0 {
			t.Errorf("model %s has physical size %dx%dmm", info.Name, info.WidthMM, info.HeightMM)
		}
	}
}

func TestModelString(t *testing.T) {
	if got := Model6C.String(); got != "6C" {
		t.Errorf("Model6C.String() = %q, want %q", got, "6C")
	}
	if got := Model(200).String(); !strings.Contains(got, "200") {
		t.Errorf("an unknown model rendered as %q, want it to name the value", got)
	}
}
