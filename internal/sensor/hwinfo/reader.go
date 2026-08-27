package hwinfo

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/logging"
	"github.com/n-kisyov/wininfopanel/internal/sensor"
	"github.com/n-kisyov/wininfopanel/internal/winapi"
)

const (
	// DefaultInterval matches HWiNFO's own default polling rate. Reading
	// faster than HWiNFO writes just re-reads the same values.
	DefaultInterval = time.Second
	// MinInterval is the floor accepted by SetInterval.
	MinInterval = 100 * time.Millisecond
	// MaxInterval is the ceiling accepted by SetInterval.
	MaxInterval = time.Minute

	// maxRemoteConnections bounds the probe for remote HWiNFO instances.
	maxRemoteConnections = 32
	// reprobeEvery re-scans for newly attached remote machines this many poll
	// cycles apart, rather than on every cycle.
	reprobeEvery = 5
)

// Entry is one sensor reading with the group context needed to display it in a
// tree.
type Entry struct {
	Key sensor.Key `json:"key"`

	Type string `json:"type"`
	// Name is the user's custom label if HWiNFO has one, else the default.
	Name string `json:"name"`
	// NameDefault is HWiNFO's own label, kept so renamed sensors stay
	// identifiable.
	NameDefault string `json:"nameDefault"`
	Unit        string `json:"unit"`

	GroupName        string `json:"groupName"`
	GroupNameDefault string `json:"groupNameDefault"`

	Value float64 `json:"value"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Avg   float64 `json:"avg"`
}

// reading converts an entry into the value type display items consume.
func (e Entry) reading() sensor.Reading {
	return sensor.Reading{
		Name: e.Name,
		Unit: e.Unit,
		Now:  e.Value,
		Min:  e.Min,
		Max:  e.Max,
		Avg:  e.Avg,
	}
}

// Reader polls HWiNFO's shared memory and serves the results.
//
// It is safe for concurrent use: Run owns a single goroutine that writes, and
// Read and Entries take a read lock.
type Reader struct {
	log *slog.Logger

	mu        sync.RWMutex
	interval  time.Duration
	entries   map[sensor.Key]Entry
	order     []sensor.Key
	available bool
	lastErr   error
	pollTime  time.Time

	// connections are the open sections, keyed by remote index. -1 is local.
	connections map[int]*connection
}

// connection is one mapped HWiNFO section.
type connection struct {
	index   int
	section string
	mem     *winapi.SharedMemory
}

func (c *connection) close() {
	if c.mem != nil {
		c.mem.Close()
		c.mem = nil
	}
}

// New returns a reader that is not yet polling. Call Run to start it.
func New() *Reader {
	return &Reader{
		log:         logging.For("sensor.hwinfo"),
		interval:    DefaultInterval,
		entries:     make(map[sensor.Key]Entry),
		connections: make(map[int]*connection),
	}
}

// SetInterval changes the poll rate, clamped to the supported range.
func (r *Reader) SetInterval(d time.Duration) {
	if d < MinInterval {
		d = MinInterval
	}
	if d > MaxInterval {
		d = MaxInterval
	}

	r.mu.Lock()
	r.interval = d
	r.mu.Unlock()
}

// Interval returns the current poll rate.
func (r *Reader) Interval() time.Duration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.interval
}

// Available reports whether HWiNFO's shared memory was readable on the last
// poll. It is false when HWiNFO is not running, or is running without Shared
// Memory Support enabled.
func (r *Reader) Available() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.available
}

// LastError returns why the most recent poll failed, or nil.
func (r *Reader) LastError() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastErr
}

// Read implements sensor.Resolver.
func (r *Reader) Read(key sensor.Key) (sensor.Reading, bool) {
	if key.Source != sensor.SourceHWiNFO {
		return sensor.Reading{}, false
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.entries[key]
	if !ok {
		return sensor.Reading{}, false
	}
	return entry.reading(), true
}

// Entries returns every known sensor, in HWiNFO's own order.
//
// The order is stable across polls, which keeps the sensor tree from
// reshuffling under the user while they are picking a sensor.
func (r *Reader) Entries() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Entry, 0, len(r.order))
	for _, key := range r.order {
		if entry, ok := r.entries[key]; ok {
			out = append(out, entry)
		}
	}
	return out
}

// Run polls until ctx is cancelled, then releases every mapped section.
func (r *Reader) Run(ctx context.Context) error {
	defer r.closeAll()

	// Poll once immediately so the first frame has data rather than waiting a
	// full interval for it.
	r.poll(0)

	cycle := 0
	timer := time.NewTimer(r.Interval())
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			cycle++
			r.poll(cycle)
			// Re-read the interval each cycle: settings can change it while
			// the reader is running.
			timer.Reset(r.Interval())
		}
	}
}

// poll reads every open connection, reopening the local section when it is
// missing and periodically probing for new remote machines.
func (r *Reader) poll(cycle int) {
	r.ensureLocal()
	if cycle%reprobeEvery == 0 {
		r.probeRemotes()
	}

	r.mu.RLock()
	conns := make([]*connection, 0, len(r.connections))
	for _, c := range r.connections {
		conns = append(conns, c)
	}
	r.mu.RUnlock()

	sort.Slice(conns, func(i, j int) bool { return conns[i].index < conns[j].index })

	var (
		entries = make(map[sensor.Key]Entry)
		order   []sensor.Key
		failed  []int
		lastErr error
	)

	for _, c := range conns {
		got, keys, err := readConnection(c)
		if err != nil {
			lastErr = err
			failed = append(failed, c.index)
			continue
		}
		for k, v := range got {
			entries[k] = v
		}
		order = append(order, keys...)
	}

	// Drop connections that failed: the next ensureLocal or probeRemotes will
	// reopen them if HWiNFO comes back.
	for _, index := range failed {
		r.dropConnection(index)
	}

	r.mu.Lock()
	// A poll that read nothing must not blank a table that other connections
	// still populate; only replace when at least one connection succeeded.
	if len(entries) > 0 || len(conns) == len(failed) {
		r.entries = entries
		r.order = order
	}
	r.available = len(entries) > 0
	r.lastErr = lastErr
	r.pollTime = time.Now()
	r.mu.Unlock()
}

// ensureLocal opens the local section if it is not already mapped.
func (r *Reader) ensureLocal() {
	r.mu.RLock()
	_, open := r.connections[LocalIndex]
	r.mu.RUnlock()
	if open {
		return
	}

	mem, err := winapi.OpenSharedMemory(LocalSection)
	if err != nil {
		// HWiNFO not running is the ordinary case, not an error worth
		// reporting on every poll.
		r.log.Debug("HWiNFO shared memory is unavailable", "error", err)
		return
	}

	r.log.Info("connected to HWiNFO shared memory", "section", LocalSection, "bytes", mem.Size())
	r.mu.Lock()
	r.connections[LocalIndex] = &connection{index: LocalIndex, section: LocalSection, mem: mem}
	r.mu.Unlock()
}

// probeRemotes looks for sections published for remote machines.
//
// Probing stops at the first index that is absent: HWiNFO numbers remote
// connections consecutively, so a gap means there are no more.
func (r *Reader) probeRemotes() {
	for index := 0; index < maxRemoteConnections; index++ {
		r.mu.RLock()
		_, open := r.connections[index]
		r.mu.RUnlock()
		if open {
			continue
		}

		section := fmt.Sprintf("%s%d", RemoteSectionPrefix, index)
		mem, err := winapi.OpenSharedMemory(section)
		if err != nil {
			return
		}

		r.log.Info("connected to remote HWiNFO instance", "section", section, "index", index)
		r.mu.Lock()
		r.connections[index] = &connection{index: index, section: section, mem: mem}
		r.mu.Unlock()
	}
}

func (r *Reader) dropConnection(index int) {
	r.mu.Lock()
	c, ok := r.connections[index]
	if ok {
		delete(r.connections, index)
	}
	r.mu.Unlock()

	if ok {
		r.log.Info("closing HWiNFO connection", "section", c.section, "index", index)
		c.close()
	}
}

func (r *Reader) closeAll() {
	r.mu.Lock()
	conns := r.connections
	r.connections = make(map[int]*connection)
	r.available = false
	r.mu.Unlock()

	for _, c := range conns {
		c.close()
	}
}

// readConnection parses one mapped section into entries.
func readConnection(c *connection) (map[sensor.Key]Entry, []sensor.Key, error) {
	data := c.mem.Bytes()
	if data == nil {
		return nil, nil, fmt.Errorf("section %s is not mapped", c.section)
	}
	return ParseTable(data, c.index)
}

// ParseTable decodes a complete HWiNFO shared-memory image.
//
// It is exported so the format can be exercised against a captured section
// dump, without HWiNFO running.
func ParseTable(data []byte, remoteIndex int) (map[sensor.Key]Entry, []sensor.Key, error) {
	header, err := parseTableHeader(data)
	if err != nil {
		return nil, nil, err
	}

	groups := make(map[uint32]group, header.GroupCount)
	for i := uint32(0); i < header.GroupCount; i++ {
		start := uint64(header.GroupOffset) + uint64(i)*uint64(header.GroupSize)
		groups[i] = parseGroup(data[start : start+uint64(groupSize)])
	}

	entries := make(map[sensor.Key]Entry, header.ReadingCount)
	order := make([]sensor.Key, 0, header.ReadingCount)

	for i := uint32(0); i < header.ReadingCount; i++ {
		start := uint64(header.ReadingOffset) + uint64(i)*uint64(header.ReadingSize)
		el := parseElement(data[start : start+uint64(elementSize)])

		parent, ok := groups[el.GroupIndex]
		if !ok {
			// A reading whose group is missing cannot be addressed, since the
			// group supplies two thirds of the key.
			continue
		}

		key := sensor.Key{
			Source:      sensor.SourceHWiNFO,
			RemoteIndex: remoteIndex,
			ID:          parent.ID,
			Instance:    parent.Instance,
			EntryID:     el.ID,
		}

		if _, duplicate := entries[key]; duplicate {
			continue
		}

		entries[key] = Entry{
			Key:              key,
			Type:             el.Type.String(),
			Name:             preferCustom(el.NameCustom, el.NameDefault),
			NameDefault:      el.NameDefault,
			Unit:             el.Unit,
			GroupName:        preferCustom(parent.NameCustom, parent.NameDefault),
			GroupNameDefault: parent.NameDefault,
			Value:            el.Value,
			Min:              el.ValueMin,
			Max:              el.ValueMax,
			Avg:              el.ValueAvg,
		}
		order = append(order, key)
	}

	return entries, order, nil
}

// preferCustom returns the user's own label when HWiNFO has one.
func preferCustom(custom, fallback string) string {
	if custom != "" {
		return custom
	}
	return fallback
}
