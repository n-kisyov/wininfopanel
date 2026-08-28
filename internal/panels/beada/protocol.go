// Package beada drives BeadaPanel USB LCD panels.
//
// BeadaPanel exposes two protocols on separate endpoints: StatusLink on
// endpoint 2 for identification, brightness, and control, and PanelLink on
// endpoint 1 for the pixel stream. Having them apart is what makes these
// panels recoverable -- a stuck display can still be reset over the control
// channel, which is not true of the single-channel Turing panels.
package beada

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// USB identifiers for every BeadaPanel model.
const (
	VendorID  = 0x4E58
	ProductID = 0x1001
)

// Endpoint numbers, per the device's own convention.
const (
	// ControlEndpoint carries StatusLink messages.
	ControlEndpoint = 2
	// DataEndpoint carries the PanelLink pixel stream.
	DataEndpoint = 1
)

// StatusLinkType identifies a control message.
type StatusLinkType byte

const (
	StatusGetPanelInfo   StatusLinkType = 1
	StatusPanelLinkReset StatusLinkType = 2
	StatusSetBacklight   StatusLinkType = 3
	StatusPushStorage    StatusLinkType = 4
	StatusGetTime        StatusLinkType = 5
	StatusSetTime        StatusLinkType = 6
)

// PanelLinkType identifies a display message.
type PanelLinkType byte

const (
	PanelLegacyCommand1   PanelLinkType = 1
	PanelLegacyCommand2   PanelLinkType = 2
	PanelResetDisplay     PanelLinkType = 3
	PanelClearScreen      PanelLinkType = 4
	PanelStartMediaStream PanelLinkType = 5
	PanelEndMediaStream   PanelLinkType = 6
)

// StatusLink frame layout.
const (
	statusProtocol     = "STATUS-LINK"
	statusHeaderLength = 20
	statusVersion      = 1
	// statusChecksumOffset is where the checksum sits, and also how many
	// leading bytes it covers.
	statusChecksumOffset = 18
)

// PanelLink frame layout.
const (
	panelProtocol     = "PANEL-LINK"
	panelHeaderLength = 268
	panelTotalLength  = 270
	panelVersion      = 1
	// panelFormatOffset is where the media format string begins.
	panelFormatOffset = 12
	// panelFormatMax bounds the format string.
	panelFormatMax = 256
)

// BuildStatusMessage assembles a StatusLink frame.
//
// The checksum covers the header only, not the payload, which is what the
// device expects.
func BuildStatusMessage(messageType StatusLinkType, payload []byte) []byte {
	total := statusHeaderLength + len(payload)
	buffer := make([]byte, total)

	copy(buffer, statusProtocol)
	buffer[11] = statusVersion
	buffer[12] = byte(messageType)
	buffer[13] = 0 // reserved
	binary.LittleEndian.PutUint16(buffer[14:], 0)
	binary.LittleEndian.PutUint16(buffer[16:], uint16(total))

	if len(payload) > 0 {
		copy(buffer[statusHeaderLength:], payload)
	}

	binary.LittleEndian.PutUint16(buffer[statusChecksumOffset:],
		checksum(buffer[:statusChecksumOffset]))

	return buffer
}

// BuildPanelMessage assembles a PanelLink frame.
//
// The frame is a fixed 270 bytes regardless of content; only the media stream
// messages carry a format string.
func BuildPanelMessage(messageType PanelLinkType, format string) ([]byte, error) {
	buffer := make([]byte, panelTotalLength)

	copy(buffer, panelProtocol)
	buffer[10] = panelVersion
	buffer[11] = byte(messageType)

	if messageType == PanelLegacyCommand1 || messageType == PanelStartMediaStream {
		if len(format) > panelFormatMax {
			return nil, fmt.Errorf("format string is %d bytes, the limit is %d",
				len(format), panelFormatMax)
		}
		copy(buffer[panelFormatOffset:], format)
	}

	binary.LittleEndian.PutUint16(buffer[panelHeaderLength:],
		checksum(buffer[:panelHeaderLength]))

	return buffer, nil
}

// MediaFormat describes the pixel stream a panel should expect.
//
// Write-through mode streams straight to the display controller and is what
// the panels support at full frame rate; the buffered form is the fallback the
// original keeps for panels that reject it.
func MediaFormat(width, height int, writeThrough bool) string {
	if writeThrough {
		return fmt.Sprintf("image/x-raw, format=BGR16, width=%d, height=%d, framerate=0/1",
			width, height)
	}
	return fmt.Sprintf("video/x-raw, format=RGB16, width=%d, height=%d, framerate=0/1",
		width, height)
}

// checksum computes the one's-complement 16-bit sum both protocols use.
//
// Words are little-endian, and an odd trailing byte is added on its own.
func checksum(data []byte) uint16 {
	var sum uint32

	for i := 0; i < len(data); i += 2 {
		var word uint16
		if i+1 < len(data) {
			word = binary.LittleEndian.Uint16(data[i:])
		} else {
			word = uint16(data[i])
		}
		sum += uint32(word)
	}

	// Fold the carries back in, twice: the first fold can itself carry.
	sum = (sum >> 16) + (sum & 0xFFFF)
	sum += sum >> 16

	return ^uint16(sum)
}

// PanelInfo is a panel's reported identity and capabilities.
type PanelInfo struct {
	FirmwareVersion   uint16 `json:"firmwareVersion"`
	PanelLinkVersion  byte   `json:"panelLinkVersion"`
	StatusLinkVersion byte   `json:"statusLinkVersion"`
	Platform          byte   `json:"platform"`

	Model     Model  `json:"model"`
	ModelName string `json:"modelName"`

	SerialNumber string `json:"serialNumber,omitempty"`

	// ResolutionX and ResolutionY are what the panel reports. The model
	// database is preferred where the two disagree, since firmware on some
	// models reports the pre-rotation orientation.
	ResolutionX uint16 `json:"resolutionX"`
	ResolutionY uint16 `json:"resolutionY"`

	StorageSizeKB uint32 `json:"storageSizeKb"`

	MaxBrightness     byte `json:"maxBrightness"`
	CurrentBrightness byte `json:"currentBrightness"`
}

// Width returns the width frames should be rendered at.
func (p PanelInfo) Width() int {
	if info, ok := Models[p.Model]; ok {
		return info.Width
	}
	return int(p.ResolutionX)
}

// Height returns the height frames should be rendered at.
func (p PanelInfo) Height() int {
	if info, ok := Models[p.Model]; ok {
		return info.Height
	}
	return int(p.ResolutionY)
}

// panelInfoPayload is the size of the GetPanelInfo response body.
const panelInfoPayload = 80

// ParsePanelInfo decodes a GetPanelInfo response.
func ParsePanelInfo(response []byte) (PanelInfo, error) {
	if len(response) < statusHeaderLength+panelInfoPayload {
		return PanelInfo{}, fmt.Errorf("response is %d bytes, need at least %d",
			len(response), statusHeaderLength+panelInfoPayload)
	}

	if protocol := string(response[:len(statusProtocol)]); protocol != statusProtocol {
		return PanelInfo{}, fmt.Errorf("unexpected protocol header %q", protocol)
	}
	if got := StatusLinkType(response[12]); got != StatusGetPanelInfo {
		return PanelInfo{}, fmt.Errorf("response is message type %d, expected %d",
			got, StatusGetPanelInfo)
	}

	payload := response[statusHeaderLength : statusHeaderLength+panelInfoPayload]

	info := PanelInfo{
		FirmwareVersion:   binary.LittleEndian.Uint16(payload[0:]),
		PanelLinkVersion:  payload[2],
		StatusLinkVersion: payload[3],
		Platform:          payload[4],
		Model:             Model(payload[5]),
		SerialNumber:      cleanSerial(payload[6:70]),
		ResolutionX:       binary.LittleEndian.Uint16(payload[70:]),
		ResolutionY:       binary.LittleEndian.Uint16(payload[72:]),
		StorageSizeKB:     binary.LittleEndian.Uint32(payload[74:]),
		MaxBrightness:     payload[78],
		CurrentBrightness: payload[79],
	}

	model, ok := Models[info.Model]
	if !ok {
		return PanelInfo{}, fmt.Errorf("unrecognized panel model %d", byte(info.Model))
	}
	info.ModelName = model.Name

	return info, nil
}

// cleanSerial trims a fixed-width serial field.
//
// A serial containing anything but letters and digits is treated as absent:
// some units ship with an uninitialized field, and a garbage value used as a
// device identifier would be worse than none.
func cleanSerial(field []byte) string {
	serial := strings.TrimSpace(strings.TrimRight(string(field), "\x00"))

	for _, r := range serial {
		isDigit := r >= '0' && r <= '9'
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !isDigit && !isLetter {
			return ""
		}
	}
	return serial
}

// ScaleBrightness converts a 0-100 percentage to the value the panel expects.
//
// PanelLink version 1 devices go dark below roughly a quarter output, so the
// range is compressed into 25-100 rather than mapping linearly from zero.
func ScaleBrightness(percent int, panelLinkVersion byte) byte {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}

	if panelLinkVersion == 1 {
		return byte(float64(percent)/100.0*75 + 25)
	}
	return byte(percent)
}
