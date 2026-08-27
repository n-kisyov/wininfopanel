package hwinfo

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/sensor"
)

// The tests build shared-memory images by hand so the wire format is verified
// without HWiNFO running. If HWiNFO ever changes its layout, these fail in a
// way that names the field.

type testGroup struct {
	id       uint32
	instance uint32
	name     string
	custom   string
}

type testElement struct {
	readingType ReadingType
	groupIndex  uint32
	id          uint32
	name        string
	custom      string
	unit        string
	value       float64
	min         float64
	max         float64
	avg         float64
}

// buildTable assembles a valid section image from groups and elements.
func buildTable(t *testing.T, sig string, groups []testGroup, elements []testElement) []byte {
	t.Helper()

	groupOffset := uint32(headerSize)
	readingOffset := groupOffset + uint32(len(groups))*groupSize
	total := int(readingOffset) + len(elements)*elementSize

	buf := make([]byte, total)

	// Header. The signature is a multi-character constant, so it is stored
	// such that its bytes read big-endian spell the text.
	if len(sig) != 4 {
		t.Fatalf("signature %q must be 4 characters", sig)
	}
	binary.LittleEndian.PutUint32(buf[0:], binary.BigEndian.Uint32([]byte(sig)))
	binary.LittleEndian.PutUint32(buf[4:], 1)  // version
	binary.LittleEndian.PutUint32(buf[8:], 0)  // revision
	binary.LittleEndian.PutUint64(buf[12:], 0) // poll time
	binary.LittleEndian.PutUint32(buf[20:], groupOffset)
	binary.LittleEndian.PutUint32(buf[24:], groupSize)
	binary.LittleEndian.PutUint32(buf[28:], uint32(len(groups)))
	binary.LittleEndian.PutUint32(buf[32:], readingOffset)
	binary.LittleEndian.PutUint32(buf[36:], elementSize)
	binary.LittleEndian.PutUint32(buf[40:], uint32(len(elements)))

	for i, g := range groups {
		rec := buf[int(groupOffset)+i*groupSize:]
		binary.LittleEndian.PutUint32(rec[0:], g.id)
		binary.LittleEndian.PutUint32(rec[4:], g.instance)
		copy(rec[8:8+sensorStringLen], g.name)
		copy(rec[8+sensorStringLen:8+2*sensorStringLen], g.custom)
	}

	for i, e := range elements {
		rec := buf[int(readingOffset)+i*elementSize:]
		binary.LittleEndian.PutUint32(rec[0:], uint32(e.readingType))
		binary.LittleEndian.PutUint32(rec[4:], e.groupIndex)
		binary.LittleEndian.PutUint32(rec[8:], e.id)
		copy(rec[12:12+sensorStringLen], e.name)
		copy(rec[12+sensorStringLen:12+2*sensorStringLen], e.custom)
		unitAt := 12 + 2*sensorStringLen
		copy(rec[unitAt:unitAt+readingStringLen], e.unit)
		valueAt := unitAt + readingStringLen
		binary.LittleEndian.PutUint64(rec[valueAt:], math.Float64bits(e.value))
		binary.LittleEndian.PutUint64(rec[valueAt+8:], math.Float64bits(e.min))
		binary.LittleEndian.PutUint64(rec[valueAt+16:], math.Float64bits(e.max))
		binary.LittleEndian.PutUint64(rec[valueAt+24:], math.Float64bits(e.avg))
	}

	return buf
}

func TestParseTableDecodesReadings(t *testing.T) {
	data := buildTable(t, "SiWH",
		[]testGroup{
			{id: 100, instance: 0, name: "CPU [#0]: AMD Ryzen"},
			{id: 200, instance: 1, name: "GPU [#0]: NVIDIA", custom: "My GPU"},
		},
		[]testElement{
			{readingType: ReadingTemperature, groupIndex: 0, id: 1, name: "Core 0", unit: "C",
				value: 65.5, min: 30, max: 90, avg: 55},
			{readingType: ReadingUsage, groupIndex: 0, id: 2, name: "Total CPU Usage", custom: "CPU Load",
				unit: "%", value: 42, min: 0, max: 100, avg: 30},
			{readingType: ReadingPower, groupIndex: 1, id: 1, name: "GPU Power", unit: "W",
				value: 220, min: 15, max: 350, avg: 180},
		})

	entries, order, err := ParseTable(data, LocalIndex)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("parsed %d entries, want 3", len(entries))
	}
	if len(order) != 3 {
		t.Fatalf("order has %d keys, want 3", len(order))
	}

	// A reading is addressed by its group's ID and instance plus its own ID.
	key := sensor.Key{Source: sensor.SourceHWiNFO, RemoteIndex: LocalIndex, ID: 100, Instance: 0, EntryID: 1}
	entry, ok := entries[key]
	if !ok {
		t.Fatalf("no entry for key %+v", key)
	}
	if entry.Name != "Core 0" {
		t.Errorf("Name = %q, want %q", entry.Name, "Core 0")
	}
	if entry.Unit != "C" {
		t.Errorf("Unit = %q, want %q", entry.Unit, "C")
	}
	if entry.Value != 65.5 || entry.Min != 30 || entry.Max != 90 || entry.Avg != 55 {
		t.Errorf("values = %v/%v/%v/%v, want 65.5/30/90/55", entry.Value, entry.Min, entry.Max, entry.Avg)
	}
	if entry.Type != "Temperature" {
		t.Errorf("Type = %q, want %q", entry.Type, "Temperature")
	}
	if entry.GroupName != "CPU [#0]: AMD Ryzen" {
		t.Errorf("GroupName = %q", entry.GroupName)
	}
}

func TestParseTablePrefersCustomNames(t *testing.T) {
	data := buildTable(t, "SiWH",
		[]testGroup{{id: 200, instance: 1, name: "GPU", custom: "My GPU"}},
		[]testElement{{groupIndex: 0, id: 2, name: "Total CPU Usage", custom: "CPU Load", unit: "%"}})

	entries, _, err := ParseTable(data, LocalIndex)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}

	key := sensor.Key{Source: sensor.SourceHWiNFO, RemoteIndex: LocalIndex, ID: 200, Instance: 1, EntryID: 2}
	entry := entries[key]
	if entry.Name != "CPU Load" {
		t.Errorf("Name = %q, want the custom label %q", entry.Name, "CPU Load")
	}
	if entry.NameDefault != "Total CPU Usage" {
		t.Errorf("NameDefault = %q, want the original preserved", entry.NameDefault)
	}
	if entry.GroupName != "My GPU" {
		t.Errorf("GroupName = %q, want the custom group label", entry.GroupName)
	}
}

func TestParseTableTagsRemoteIndex(t *testing.T) {
	data := buildTable(t, "SiWH",
		[]testGroup{{id: 1, instance: 0, name: "CPU"}},
		[]testElement{{groupIndex: 0, id: 1, name: "Temp", unit: "C", value: 50}})

	entries, _, err := ParseTable(data, 3)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}

	key := sensor.Key{Source: sensor.SourceHWiNFO, RemoteIndex: 3, ID: 1, Instance: 0, EntryID: 1}
	if _, ok := entries[key]; !ok {
		t.Errorf("remote readings were not tagged with their connection index; got keys %v", keysOf(entries))
	}
}

func TestParseTableRejectsDeadSection(t *testing.T) {
	data := buildTable(t, "DEAD", nil, nil)

	if _, _, err := ParseTable(data, LocalIndex); err == nil {
		t.Error("expected an error for a section marked DEAD")
	}
}

func TestParseTableRejectsTruncatedImage(t *testing.T) {
	if _, _, err := ParseTable(make([]byte, 10), LocalIndex); err == nil {
		t.Error("expected an error for an image shorter than the header")
	}
}

func TestParseTableRejectsOutOfBoundsArrays(t *testing.T) {
	data := buildTable(t, "SiWH",
		[]testGroup{{id: 1, name: "CPU"}},
		[]testElement{{groupIndex: 0, id: 1, unit: "C"}})

	// Claim far more readings than the image can hold. The header comes from
	// another process, so this must be rejected rather than read past the end.
	binary.LittleEndian.PutUint32(data[40:], 100000)

	if _, _, err := ParseTable(data, LocalIndex); err == nil {
		t.Error("expected an error when the reading array overruns the section")
	}
}

func TestParseTableRejectsUndersizedRecords(t *testing.T) {
	data := buildTable(t, "SiWH",
		[]testGroup{{id: 1, name: "CPU"}},
		[]testElement{{groupIndex: 0, id: 1}})

	binary.LittleEndian.PutUint32(data[36:], 4) // reading record size

	if _, _, err := ParseTable(data, LocalIndex); err == nil {
		t.Error("expected an error when the reading record is smaller than the known layout")
	}
}

func TestParseTableSkipsReadingsWithMissingGroup(t *testing.T) {
	data := buildTable(t, "SiWH",
		[]testGroup{{id: 1, instance: 0, name: "CPU"}},
		[]testElement{
			{groupIndex: 0, id: 1, name: "Good", unit: "C", value: 1},
			{groupIndex: 99, id: 2, name: "Orphan", unit: "C", value: 2},
		})

	entries, _, err := ParseTable(data, LocalIndex)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("parsed %d entries, want 1: a reading with no group cannot be addressed", len(entries))
	}
}

func TestParseTableNormalizesNonFiniteValues(t *testing.T) {
	data := buildTable(t, "SiWH",
		[]testGroup{{id: 1, instance: 0, name: "CPU"}},
		[]testElement{{groupIndex: 0, id: 1, name: "Broken", unit: "C",
			value: math.NaN(), min: math.Inf(-1), max: math.Inf(1), avg: 0}})

	entries, _, err := ParseTable(data, LocalIndex)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}

	key := sensor.Key{Source: sensor.SourceHWiNFO, RemoteIndex: LocalIndex, ID: 1, Instance: 0, EntryID: 1}
	entry := entries[key]
	if entry.Value != 0 || entry.Min != 0 || entry.Max != 0 {
		t.Errorf("non-finite readings survived: %v/%v/%v, want all zero", entry.Value, entry.Min, entry.Max)
	}
}

func TestParseTableTrimsPaddedStrings(t *testing.T) {
	data := buildTable(t, "SiWH",
		[]testGroup{{id: 1, instance: 0, name: "CPU"}},
		[]testElement{{groupIndex: 0, id: 1, name: "Core 0", unit: "C"}})

	entries, _, err := ParseTable(data, LocalIndex)
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}

	key := sensor.Key{Source: sensor.SourceHWiNFO, RemoteIndex: LocalIndex, ID: 1, Instance: 0, EntryID: 1}
	if got := entries[key].Name; got != "Core 0" {
		t.Errorf("Name = %q (len %d), want %q with the NUL padding stripped", got, len(got), "Core 0")
	}
}

func TestReaderReadRejectsForeignSources(t *testing.T) {
	r := New()
	if _, ok := r.Read(sensor.Key{Source: sensor.SourceNative, Path: "cpu/temp"}); ok {
		t.Error("the HWiNFO reader resolved a native sensor key")
	}
}

func TestReaderSetIntervalClamps(t *testing.T) {
	r := New()

	r.SetInterval(time.Millisecond)
	if got := r.Interval(); got != MinInterval {
		t.Errorf("Interval = %v, want it clamped up to %v", got, MinInterval)
	}

	r.SetInterval(time.Hour)
	if got := r.Interval(); got != MaxInterval {
		t.Errorf("Interval = %v, want it clamped down to %v", got, MaxInterval)
	}
}

func TestReaderIsUnavailableBeforeAnyPoll(t *testing.T) {
	r := New()
	if r.Available() {
		t.Error("a reader that has not polled reported itself available")
	}
	if len(r.Entries()) != 0 {
		t.Error("a reader that has not polled returned entries")
	}
}

func keysOf(entries map[sensor.Key]Entry) []sensor.Key {
	out := make([]sensor.Key, 0, len(entries))
	for k := range entries {
		out = append(out, k)
	}
	return out
}
