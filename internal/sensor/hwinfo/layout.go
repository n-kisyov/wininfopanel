// Package hwinfo reads HWiNFO's sensor table out of the shared-memory section
// it publishes when "Shared Memory Support" is enabled.
//
// The format is a fixed header describing two arrays: sensor groups ("headers",
// e.g. one per motherboard chip or GPU) and readings ("elements", the
// individual values). Each reading names its group by index. Nothing here
// writes to the section; HWiNFO owns it.
package hwinfo

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

const (
	// LocalSection is the section HWiNFO publishes for this machine.
	LocalSection = `Global\HWiNFO_SENS_SM2`
	// RemoteSectionPrefix is followed by a zero-based index for each remote
	// machine HWiNFO is aggregating.
	RemoteSectionPrefix = `Global\HWiNFO_SENS_SM2_REMOTE_`

	// LocalIndex marks readings from this machine, distinguishing them from
	// remote connections which are numbered from zero.
	LocalIndex = -1
)

// Field widths, fixed by the shared-memory format.
const (
	sensorStringLen  = 128
	readingStringLen = 16

	headerSize  = 44  // HWINFO_MEM
	groupSize   = 264 // 4 + 4 + 128 + 128
	elementSize = 316 // 4 + 4 + 4 + 128 + 128 + 16 + 8*4
)

// ReadingType classifies what a reading measures.
type ReadingType int

const (
	ReadingNone ReadingType = iota
	ReadingTemperature
	ReadingVoltage
	ReadingFan
	ReadingCurrent
	ReadingPower
	ReadingClock
	ReadingUsage
	ReadingOther
)

// String returns a display label for the reading type.
func (t ReadingType) String() string {
	switch t {
	case ReadingNone:
		return "None"
	case ReadingTemperature:
		return "Temperature"
	case ReadingVoltage:
		return "Voltage"
	case ReadingFan:
		return "Fan"
	case ReadingCurrent:
		return "Current"
	case ReadingPower:
		return "Power"
	case ReadingClock:
		return "Clock"
	case ReadingUsage:
		return "Usage"
	case ReadingOther:
		return "Other"
	default:
		return "Unknown"
	}
}

// tableHeader is the section's leading descriptor, locating the group and
// reading arrays. Sizes are read from here rather than assumed, so a future
// HWiNFO revision that grows a record still parses.
type tableHeader struct {
	Signature     uint32
	Version       uint32
	Revision      uint32
	PollTime      int64
	GroupOffset   uint32
	GroupSize     uint32
	GroupCount    uint32
	ReadingOffset uint32
	ReadingSize   uint32
	ReadingCount  uint32
}

// signatureDead marks a section HWiNFO has torn down. The signature is a
// multi-character constant, so its bytes read in big-endian order spell the
// text: a live table reads "SiWH", a retired one "DEAD".
//
// Only "DEAD" is treated as invalid, matching HWiNFO's own contract. Being
// stricter would risk rejecting a future signature revision outright, and the
// array bounds below already catch a section that is not really a sensor
// table.
const signatureDead = "DEAD"

// parseTableHeader decodes the section header.
func parseTableHeader(data []byte) (tableHeader, error) {
	var h tableHeader
	if len(data) < headerSize {
		return h, fmt.Errorf("shared memory is %d bytes, too small for the %d-byte header", len(data), headerSize)
	}

	h.Signature = binary.LittleEndian.Uint32(data[0:])
	h.Version = binary.LittleEndian.Uint32(data[4:])
	h.Revision = binary.LittleEndian.Uint32(data[8:])
	h.PollTime = int64(binary.LittleEndian.Uint64(data[12:]))
	h.GroupOffset = binary.LittleEndian.Uint32(data[20:])
	h.GroupSize = binary.LittleEndian.Uint32(data[24:])
	h.GroupCount = binary.LittleEndian.Uint32(data[28:])
	h.ReadingOffset = binary.LittleEndian.Uint32(data[32:])
	h.ReadingSize = binary.LittleEndian.Uint32(data[36:])
	h.ReadingCount = binary.LittleEndian.Uint32(data[40:])

	return h, h.validate(len(data))
}

// signatureText renders the signature as the four ASCII characters it encodes.
//
// The field is a multi-character constant, so the bytes spell the text in
// big-endian order.
func (h tableHeader) signatureText() string {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], h.Signature)
	return string(b[:])
}

// validate rejects a header that does not describe a usable table, so a
// half-initialized or torn-down section is caught before indexing into it.
func (h tableHeader) validate(dataLen int) error {
	if h.signatureText() == signatureDead {
		return fmt.Errorf("HWiNFO shared memory is marked dead")
	}

	if h.GroupSize < groupSize {
		return fmt.Errorf("group record is %d bytes, expected at least %d", h.GroupSize, groupSize)
	}
	if h.ReadingSize < elementSize {
		return fmt.Errorf("reading record is %d bytes, expected at least %d", h.ReadingSize, elementSize)
	}

	if end, ok := arrayEnd(h.GroupOffset, h.GroupSize, h.GroupCount); !ok || end > uint64(dataLen) {
		return fmt.Errorf("group array (offset %d, %d records of %d bytes) does not fit in %d bytes",
			h.GroupOffset, h.GroupCount, h.GroupSize, dataLen)
	}
	if end, ok := arrayEnd(h.ReadingOffset, h.ReadingSize, h.ReadingCount); !ok || end > uint64(dataLen) {
		return fmt.Errorf("reading array (offset %d, %d records of %d bytes) does not fit in %d bytes",
			h.ReadingOffset, h.ReadingCount, h.ReadingSize, dataLen)
	}
	return nil
}

// arrayEnd computes offset + size*count in 64-bit arithmetic, reporting false
// if the result overflows. The header comes from another process, so its
// values are untrusted input.
func arrayEnd(offset, size, count uint32) (uint64, bool) {
	end := uint64(offset) + uint64(size)*uint64(count)
	if end < uint64(offset) {
		return 0, false
	}
	return end, true
}

// group is one sensor group: a chip, device, or subsystem that owns readings.
type group struct {
	ID          uint32
	Instance    uint32
	NameDefault string
	NameCustom  string
}

func parseGroup(rec []byte) group {
	return group{
		ID:          binary.LittleEndian.Uint32(rec[0:]),
		Instance:    binary.LittleEndian.Uint32(rec[4:]),
		NameDefault: cString(rec[8 : 8+sensorStringLen]),
		NameCustom:  cString(rec[8+sensorStringLen : 8+2*sensorStringLen]),
	}
}

// element is one reading within a group.
type element struct {
	Type ReadingType
	// GroupIndex selects the owning group in the group array.
	GroupIndex uint32
	// ID is the reading's identifier, unique within its group.
	ID          uint32
	NameDefault string
	NameCustom  string
	Unit        string
	Value       float64
	ValueMin    float64
	ValueMax    float64
	ValueAvg    float64
}

func parseElement(rec []byte) element {
	const (
		namesAt = 12
		unitAt  = namesAt + 2*sensorStringLen
		valueAt = unitAt + readingStringLen
	)

	return element{
		Type:        ReadingType(binary.LittleEndian.Uint32(rec[0:])),
		GroupIndex:  binary.LittleEndian.Uint32(rec[4:]),
		ID:          binary.LittleEndian.Uint32(rec[8:]),
		NameDefault: cString(rec[namesAt : namesAt+sensorStringLen]),
		NameCustom:  cString(rec[namesAt+sensorStringLen : unitAt]),
		Unit:        cString(rec[unitAt : unitAt+readingStringLen]),
		Value:       readFloat(rec[valueAt:]),
		ValueMin:    readFloat(rec[valueAt+8:]),
		ValueMax:    readFloat(rec[valueAt+16:]),
		ValueAvg:    readFloat(rec[valueAt+24:]),
	}
}

// readFloat decodes a little-endian float64, mapping NaN and infinities to
// zero so a sensor HWiNFO could not read does not poison arithmetic downstream.
func readFloat(b []byte) float64 {
	v := math.Float64frombits(binary.LittleEndian.Uint64(b))
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// cString reads a NUL-terminated string from a fixed-width field.
//
// HWiNFO writes single-byte characters in the system code page; anything
// outside ASCII is passed through as-is, which is what the original does too.
func cString(b []byte) string {
	if i := indexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return strings.TrimSpace(string(b))
}

func indexByte(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}
