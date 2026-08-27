package native

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// Sensor paths are stable identifiers stored in saved profiles, so they must
// not change once released. The shape is "group/subject/measure", e.g.
// "cpu/0/load" or "network/Ethernet/download".

const bytesPerGB = 1024 * 1024 * 1024

// Reading type labels, matching HWiNFO's vocabulary so both sensor trees read
// the same way.
const (
	typeUsage   = "Usage"
	typeClock   = "Clock"
	typeOther   = "Other"
	typeCurrent = "Current"
)

// cpuCollector reports processor load, overall and per core.
type cpuCollector struct {
	// warmed guards the first per-core sample: gopsutil's first non-blocking
	// call has no previous snapshot to diff against and returns zeros.
	warmed bool
}

func newCPUCollector() *cpuCollector { return &cpuCollector{} }

func (c *cpuCollector) name() string { return "cpu" }

func (c *cpuCollector) collect(_ context.Context, out *sampleSet) error {
	// Non-blocking: percentages are computed against the previous call, which
	// is exactly the poll interval and avoids stalling the collector.
	total, err := cpu.Percent(0, false)
	if err != nil {
		return fmt.Errorf("read cpu load: %w", err)
	}
	perCore, err := cpu.Percent(0, true)
	if err != nil {
		return fmt.Errorf("read per-core cpu load: %w", err)
	}

	if !c.warmed {
		// Publish nothing this cycle rather than a fabricated zero load.
		c.warmed = true
		return nil
	}

	const group = "CPU"
	if len(total) > 0 {
		out.add("cpu/load", group, "Total CPU Usage", typeUsage, "%", total[0])
	}
	for i, v := range perCore {
		out.add(fmt.Sprintf("cpu/%d/load", i), group, fmt.Sprintf("Core %d Usage", i), typeUsage, "%", v)
	}

	if counts, err := cpu.Counts(true); err == nil {
		out.add("cpu/threads", group, "Thread Count", typeOther, "", float64(counts))
	}
	if counts, err := cpu.Counts(false); err == nil {
		out.add("cpu/cores", group, "Core Count", typeOther, "", float64(counts))
	}

	// Reported clock is the nominal rate from the CPU description; actual
	// per-core frequency needs MSR access, which arrives in a later phase.
	if info, err := cpu.Info(); err == nil && len(info) > 0 {
		if info[0].Mhz > 0 {
			out.add("cpu/clock", group, "CPU Clock", typeClock, " MHz", info[0].Mhz)
		}
		out.addText("cpu/name", group, "CPU Name", strings.TrimSpace(info[0].ModelName))
	}

	// Load average is emulated on Windows and only meaningful after a warmup
	// period, so a failure here is unremarkable.
	if avg, err := load.Avg(); err == nil {
		out.add("cpu/load/1m", group, "Load Average (1m)", typeOther, "", avg.Load1)
	}

	return nil
}

// memoryCollector reports physical and virtual memory use.
type memoryCollector struct{}

func newMemoryCollector() *memoryCollector { return &memoryCollector{} }

func (m *memoryCollector) name() string { return "memory" }

func (m *memoryCollector) collect(_ context.Context, out *sampleSet) error {
	vm, err := mem.VirtualMemory()
	if err != nil {
		return fmt.Errorf("read memory: %w", err)
	}

	const group = "Memory"
	out.add("memory/load", group, "Memory Usage", typeUsage, "%", vm.UsedPercent)
	out.add("memory/used", group, "Memory Used", typeOther, "GB", float64(vm.Used)/bytesPerGB)
	out.add("memory/available", group, "Memory Available", typeOther, "GB", float64(vm.Available)/bytesPerGB)
	out.add("memory/total", group, "Memory Total", typeOther, "GB", float64(vm.Total)/bytesPerGB)

	if sm, err := mem.SwapMemory(); err == nil && sm.Total > 0 {
		out.add("memory/swap/load", group, "Swap Usage", typeUsage, "%", sm.UsedPercent)
		out.add("memory/swap/used", group, "Swap Used", typeOther, "GB", float64(sm.Used)/bytesPerGB)
		out.add("memory/swap/total", group, "Swap Total", typeOther, "GB", float64(sm.Total)/bytesPerGB)
	}

	return nil
}

// networkCollector reports per-interface throughput.
//
// gopsutil exposes cumulative byte counters, so rates are differences between
// consecutive polls divided by the elapsed time.
type networkCollector struct {
	previous map[string]net.IOCountersStat
	lastAt   time.Time
}

func newNetworkCollector() *networkCollector {
	return &networkCollector{previous: make(map[string]net.IOCountersStat)}
}

func (n *networkCollector) name() string { return "network" }

func (n *networkCollector) collect(_ context.Context, out *sampleSet) error {
	counters, err := net.IOCounters(true)
	if err != nil {
		return fmt.Errorf("read network counters: %w", err)
	}

	now := time.Now()
	elapsed := now.Sub(n.lastAt).Seconds()
	first := n.lastAt.IsZero()
	n.lastAt = now

	const group = "Network"
	for _, c := range counters {
		// Skip interfaces that have never carried traffic: a machine can have
		// a dozen virtual adapters that would only clutter the sensor tree.
		if c.BytesSent == 0 && c.BytesRecv == 0 {
			continue
		}

		previous, seen := n.previous[c.Name]
		n.previous[c.Name] = c

		safeName := sanitizePathSegment(c.Name)
		out.add("network/"+safeName+"/sent", group, c.Name+" Total Sent", typeOther, "GB",
			float64(c.BytesSent)/bytesPerGB)
		out.add("network/"+safeName+"/received", group, c.Name+" Total Received", typeOther, "GB",
			float64(c.BytesRecv)/bytesPerGB)

		// Rates need a previous sample to diff against.
		if first || !seen || elapsed <= 0 {
			continue
		}
		out.add("network/"+safeName+"/upload", group, c.Name+" Upload Rate", typeCurrent, "KB/s",
			perSecondKB(c.BytesSent, previous.BytesSent, elapsed))
		out.add("network/"+safeName+"/download", group, c.Name+" Download Rate", typeCurrent, "KB/s",
			perSecondKB(c.BytesRecv, previous.BytesRecv, elapsed))
	}

	return nil
}

// perSecondKB converts a counter delta into kilobytes per second.
//
// Counters can reset when an adapter is disabled and re-enabled; a decrease is
// reported as zero rather than as a large negative rate.
func perSecondKB(current, previous uint64, elapsed float64) float64 {
	if current < previous {
		return 0
	}
	return float64(current-previous) / 1024 / elapsed
}

// diskCollector reports volume capacity and throughput.
type diskCollector struct {
	previous map[string]disk.IOCountersStat
	lastAt   time.Time
}

func newDiskCollector() *diskCollector {
	return &diskCollector{previous: make(map[string]disk.IOCountersStat)}
}

func (d *diskCollector) name() string { return "disk" }

func (d *diskCollector) collect(_ context.Context, out *sampleSet) error {
	partitions, err := disk.Partitions(false)
	if err != nil {
		return fmt.Errorf("enumerate volumes: %w", err)
	}

	const group = "Storage"
	for _, p := range partitions {
		usage, err := disk.Usage(p.Mountpoint)
		if err != nil {
			// A drive with no media (an empty card reader) is normal.
			continue
		}

		label := strings.TrimSuffix(p.Mountpoint, `\`)
		safeName := sanitizePathSegment(label)

		out.add("disk/"+safeName+"/load", group, label+" Usage", typeUsage, "%", usage.UsedPercent)
		out.add("disk/"+safeName+"/used", group, label+" Used", typeOther, "GB", float64(usage.Used)/bytesPerGB)
		out.add("disk/"+safeName+"/free", group, label+" Free", typeOther, "GB", float64(usage.Free)/bytesPerGB)
		out.add("disk/"+safeName+"/total", group, label+" Total", typeOther, "GB", float64(usage.Total)/bytesPerGB)
	}

	d.collectThroughput(out)
	return nil
}

// collectThroughput adds per-device read and write rates.
//
// Failure here is not fatal: capacity sensors are the more important half, and
// IO counters are unavailable on some configurations.
func (d *diskCollector) collectThroughput(out *sampleSet) {
	counters, err := disk.IOCounters()
	if err != nil {
		return
	}

	now := time.Now()
	elapsed := now.Sub(d.lastAt).Seconds()
	first := d.lastAt.IsZero()
	d.lastAt = now

	// Map iteration order is random; sort so the sensor tree is stable.
	names := make([]string, 0, len(counters))
	for name := range counters {
		names = append(names, name)
	}
	sort.Strings(names)

	const group = "Storage"
	for _, name := range names {
		c := counters[name]
		previous, seen := d.previous[name]
		d.previous[name] = c

		if first || !seen || elapsed <= 0 {
			continue
		}

		safeName := sanitizePathSegment(name)
		out.add("disk/"+safeName+"/read", group, name+" Read Rate", typeCurrent, "KB/s",
			perSecondKB(c.ReadBytes, previous.ReadBytes, elapsed))
		out.add("disk/"+safeName+"/write", group, name+" Write Rate", typeCurrent, "KB/s",
			perSecondKB(c.WriteBytes, previous.WriteBytes, elapsed))
	}
}

// systemCollector reports host information and process counts.
type systemCollector struct{}

func newSystemCollector() *systemCollector { return &systemCollector{} }

func (s *systemCollector) name() string { return "system" }

func (s *systemCollector) collect(_ context.Context, out *sampleSet) error {
	const group = "System"

	info, err := host.Info()
	if err != nil {
		return fmt.Errorf("read host info: %w", err)
	}

	out.add("system/uptime", group, "Uptime", typeOther, "s", float64(info.Uptime))
	out.addText("system/os", group, "Operating System",
		strings.TrimSpace(info.Platform+" "+info.PlatformVersion))
	out.addText("system/hostname", group, "Host Name", info.Hostname)

	if pids, err := process.Pids(); err == nil {
		out.add("system/processes", group, "Process Count", typeOther, "", float64(len(pids)))
	}

	return nil
}

// sanitizePathSegment makes a device or interface name safe to embed in a
// sensor path, whose separator is "/".
//
// Names come from the OS and routinely contain spaces, backslashes, and
// punctuation; without this a path like "disk/C:\/used" would be ambiguous.
func sanitizePathSegment(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
