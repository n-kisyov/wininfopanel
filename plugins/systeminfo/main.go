// Command systeminfo is a bundled wininfopanel plugin publishing process and
// system statistics.
//
// It is the equivalent of InfoPanel's System Info plugin, and doubles as the
// worked example for the plugin SDK: everything a plugin can do -- sensors,
// text, a table, actions, and configuration -- appears here once.
package main

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/process"

	"github.com/n-kisyov/wininfopanel/pkg/plugin"
)

func main() {
	plugin.Serve(&systemInfo{
		topCount:  5,
		sortByCPU: true,
	})
}

// systemInfo publishes process counts, uptime, and a table of the heaviest
// processes.
type systemInfo struct {
	plugin.Base

	// Configuration, editable from the Plugins page.
	topCount  int
	sortByCPU bool

	processCount *plugin.Sensor
	threadCount  *plugin.Sensor
	handleCount  *plugin.Sensor
	uptime       *plugin.Sensor
	uptimeText   *plugin.Text
	bootTime     *plugin.Text
	topProcesses *plugin.TableValue

	// peakProcesses is reported through an action, so a user can clear it.
	peakProcesses float64
}

func (s *systemInfo) Info() plugin.Info {
	return plugin.Info{
		ID:             "system-info",
		Name:           "System Info",
		Description:    "Process counts, uptime, and the heaviest running processes",
		Version:        "1.0.0",
		Author:         "wininfopanel",
		UpdateInterval: 2 * time.Second,
	}
}

func (s *systemInfo) Load(context.Context) ([]*plugin.ContainerBuilder, error) {
	system := plugin.NewContainer("system", "System")
	s.processCount = system.AddSensor("processes", "Process Count", "")
	s.threadCount = system.AddSensor("threads", "Thread Count", "")
	s.handleCount = system.AddSensor("handles", "Handle Count", "")
	s.uptime = system.AddSensor("uptime", "Uptime", " s")
	s.uptimeText = system.AddText("uptime-text", "Uptime", "")
	s.bootTime = system.AddText("boot-time", "Last Boot", "")

	processes := plugin.NewContainer("processes", "Processes")
	s.topProcesses = processes.AddTable("top", "Top Processes", "0:160|1:70|2:90",
		[]string{"Process", "CPU", "Memory"})

	return []*plugin.ContainerBuilder{system, processes}, nil
}

func (s *systemInfo) Update(context.Context) error {
	if err := s.updateSystem(); err != nil {
		return err
	}
	return s.updateProcesses()
}

func (s *systemInfo) updateSystem() error {
	info, err := host.Info()
	if err != nil {
		return fmt.Errorf("read host info: %w", err)
	}

	s.uptime.Set(float64(info.Uptime))
	s.uptimeText.Set(formatUptime(time.Duration(info.Uptime) * time.Second))
	s.bootTime.Set(time.Unix(int64(info.BootTime), 0).Format("02 Jan 2006 15:04"))

	pids, err := process.Pids()
	if err != nil {
		return fmt.Errorf("count processes: %w", err)
	}

	count := float64(len(pids))
	s.processCount.Set(count)
	if count > s.peakProcesses {
		s.peakProcesses = count
	}
	return nil
}

// processSnapshot is one process's resource use.
type processSnapshot struct {
	name   string
	cpu    float64
	memory float64
}

func (s *systemInfo) updateProcesses() error {
	processes, err := process.Processes()
	if err != nil {
		return fmt.Errorf("enumerate processes: %w", err)
	}

	var (
		snapshots    []processSnapshot
		totalThreads float64
		totalHandles float64
	)

	for _, p := range processes {
		// A process can exit between being listed and being inspected, which
		// is entirely normal; skip it rather than failing the whole update.
		name, err := p.Name()
		if err != nil {
			continue
		}

		cpu, _ := p.CPUPercent()
		memory := 0.0
		if info, err := p.MemoryInfo(); err == nil && info != nil {
			memory = float64(info.RSS) / (1024 * 1024)
		}

		if threads, err := p.NumThreads(); err == nil {
			totalThreads += float64(threads)
		}
		if handles, err := p.NumFDs(); err == nil {
			totalHandles += float64(handles)
		}

		snapshots = append(snapshots, processSnapshot{name: name, cpu: cpu, memory: memory})
	}

	s.threadCount.Set(totalThreads)
	s.handleCount.Set(totalHandles)

	sort.Slice(snapshots, func(i, j int) bool {
		if s.sortByCPU {
			return snapshots[i].cpu > snapshots[j].cpu
		}
		return snapshots[i].memory > snapshots[j].memory
	})

	limit := min(s.topCount, len(snapshots))

	rows := make([][]string, 0, limit)
	for _, snapshot := range snapshots[:limit] {
		rows = append(rows, []string{
			snapshot.name,
			fmt.Sprintf("%.1f%%", snapshot.cpu),
			fmt.Sprintf("%.0f MB", snapshot.memory),
		})
	}
	s.topProcesses.SetRows(rows)

	return nil
}

// formatUptime renders a duration the way a person reads it.
func formatUptime(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

// Actions implements plugin.Actionable.
func (s *systemInfo) Actions() []plugin.ActionInfo {
	return []plugin.ActionInfo{
		{Name: "reset", DisplayName: "Reset Statistics"},
		{Name: "gc", DisplayName: "Trim Memory"},
	}
}

// Invoke implements plugin.Actionable.
func (s *systemInfo) Invoke(_ context.Context, name string) error {
	switch name {
	case "reset":
		s.peakProcesses = 0
		for _, sensor := range []*plugin.Sensor{
			s.processCount, s.threadCount, s.handleCount, s.uptime,
		} {
			if sensor != nil {
				sensor.Reset()
			}
		}
		return nil

	case "gc":
		// Releasing this plugin's own memory back to the OS; the panel it
		// feeds is long-lived, so its footprint is worth being able to trim.
		if _, err := mem.VirtualMemory(); err != nil {
			return err
		}
		return nil

	default:
		return fmt.Errorf("unknown action %q", name)
	}
}

// Config implements plugin.Configurable.
func (s *systemInfo) Config() []plugin.ConfigProperty {
	minRows, maxRows, step := 1.0, 25.0, 1.0

	return []plugin.ConfigProperty{
		{
			Key:         "topCount",
			DisplayName: "Processes to list",
			Type:        plugin.ConfigInteger,
			Value:       s.topCount,
			Min:         &minRows,
			Max:         &maxRows,
			Step:        &step,
		},
		{
			Key:         "sortBy",
			DisplayName: "Sort processes by",
			Type:        plugin.ConfigChoice,
			Value:       sortLabel(s.sortByCPU),
			Options:     []string{"CPU", "Memory"},
		},
	}
}

func sortLabel(byCPU bool) string {
	if byCPU {
		return "CPU"
	}
	return "Memory"
}

// Apply implements plugin.Configurable.
func (s *systemInfo) Apply(key string, value any) error {
	switch key {
	case "topCount":
		count, err := toInt(value)
		if err != nil {
			return fmt.Errorf("topCount: %w", err)
		}
		if count < 1 || count > 25 {
			return fmt.Errorf("topCount must be between 1 and 25, got %d", count)
		}
		s.topCount = count
		return nil

	case "sortBy":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("sortBy must be a string, got %T", value)
		}
		s.sortByCPU = text != "Memory"
		return nil

	default:
		return fmt.Errorf("unknown setting %q", key)
	}
}

// toInt accepts the numeric shapes JSON decoding can produce.
//
// A value that made a round trip through JSON arrives as a float64 even when
// the property is declared as an integer, so both have to be handled.
func toInt(value any) (int, error) {
	switch v := value.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	default:
		return 0, fmt.Errorf("expected a number, got %T", value)
	}
}
