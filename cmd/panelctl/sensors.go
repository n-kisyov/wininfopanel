package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/sensor"
	"github.com/n-kisyov/wininfopanel/internal/sensor/hwinfo"
	"github.com/n-kisyov/wininfopanel/internal/sensor/native"
)

// row is the source-independent shape the table and JSON output are built
// from, so HWiNFO and native sensors render identically.
type row struct {
	Key   sensor.Key `json:"key"`
	Type  string     `json:"type"`
	Group string     `json:"group"`
	Name  string     `json:"name"`
	Unit  string     `json:"unit"`
	Value float64    `json:"value"`
	Min   float64    `json:"min"`
	Max   float64    `json:"max"`
	Avg   float64    `json:"avg"`
	Text  string     `json:"text,omitempty"`
}

// address renders the key in the form a person can match against a profile.
func (r row) address() string {
	if r.Key.Path != "" {
		return r.Key.Path
	}
	return fmt.Sprintf("%d/%d/%d", r.Key.ID, r.Key.Instance, r.Key.EntryID)
}

// reading renders the value column, preferring text for string sensors.
func (r row) reading() string {
	if r.Text != "" {
		return r.Text
	}
	return fmt.Sprintf("%.2f%s", r.Value, r.Unit)
}

func runSensors(ctx context.Context, args []string) error {
	fs := newFlagSet("sensors")
	source := fs.String("source", "native", "sensor source to read: hwinfo or native")
	asJSON := fs.Bool("json", false, "emit JSON instead of a table")
	filter := fs.String("filter", "", "only show sensors whose name or group contains this text")
	watch := fs.Duration("watch", 0, "re-read at this interval until interrupted (e.g. 1s)")
	verbose := fs.Bool("v", false, "log engine activity to stderr")

	if err := fs.Parse(args); err != nil {
		return err
	}
	setupConsoleLogging(*verbose)

	switch *source {
	case "hwinfo":
		return sensorsFromHWiNFO(ctx, *asJSON, *filter, *watch)
	case "native":
		return sensorsFromNative(ctx, *asJSON, *filter, *watch)
	default:
		// The plugin source arrives with the plugin host; naming it here would
		// promise something that does not work yet.
		return fmt.Errorf("unknown source %q (available: hwinfo, native)", *source)
	}
}

func sensorsFromHWiNFO(ctx context.Context, asJSON bool, filter string, watch time.Duration) error {
	reader := hwinfo.New()
	if watch > 0 {
		reader.SetInterval(watch)
	}

	pollCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { reader.Run(pollCtx) }()

	if err := waitFor(ctx, 3*time.Second, reader.Available); err != nil {
		if lastErr := reader.LastError(); lastErr != nil {
			return fmt.Errorf("HWiNFO shared memory is not readable: %w", lastErr)
		}
		return fmt.Errorf("HWiNFO shared memory is not available; " +
			"start HWiNFO and enable Settings > Shared Memory Support")
	}

	return emitLoop(ctx, watch, asJSON, filter, func() []row {
		entries := reader.Entries()
		rows := make([]row, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, row{
				Key: e.Key, Type: e.Type, Group: e.GroupName, Name: e.Name, Unit: e.Unit,
				Value: e.Value, Min: e.Min, Max: e.Max, Avg: e.Avg,
			})
		}
		return rows
	})
}

func sensorsFromNative(ctx context.Context, asJSON bool, filter string, watch time.Duration) error {
	interval := watch
	if interval <= 0 {
		interval = time.Second
	}

	monitor := native.New(native.Options{
		Interval:        interval,
		StorageEnabled:  true,
		StorageInterval: interval,
	})

	pollCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { monitor.Run(pollCtx) }()

	if err := waitFor(ctx, 5*time.Second, monitor.Available); err != nil {
		return fmt.Errorf("native monitor produced no sensors: %w", err)
	}

	// Rate sensors need two polls to produce a value, so let one more elapse
	// before reporting; otherwise upload/download/read/write are all absent.
	select {
	case <-ctx.Done():
		return nil
	case <-time.After(interval + 250*time.Millisecond):
	}

	return emitLoop(ctx, watch, asJSON, filter, func() []row {
		entries := monitor.Entries()
		rows := make([]row, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, row{
				Key: e.Key, Type: e.Type, Group: e.GroupName, Name: e.Name, Unit: e.Unit,
				Value: e.Value, Min: e.Min, Max: e.Max, Avg: e.Avg, Text: e.Text,
			})
		}
		return rows
	})
}

// emitLoop renders once, or repeatedly when watching.
func emitLoop(ctx context.Context, watch time.Duration, asJSON bool, filter string, snapshot func() []row) error {
	for {
		if err := emit(applyFilter(snapshot(), filter), asJSON); err != nil {
			return err
		}
		if watch <= 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(watch):
		}
	}
}

// waitFor polls ready until it returns true, ctx is cancelled, or the timeout
// elapses.
func waitFor(ctx context.Context, timeout time.Duration, ready func() bool) error {
	deadline := time.After(timeout)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	for {
		if ready() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("timed out after %s", timeout)
		case <-ticker.C:
		}
	}
}

func applyFilter(rows []row, filter string) []row {
	if filter == "" {
		return rows
	}

	needle := strings.ToLower(filter)
	kept := rows[:0]
	for _, r := range rows {
		if strings.Contains(strings.ToLower(r.Name), needle) ||
			strings.Contains(strings.ToLower(r.Group), needle) {
			kept = append(kept, r)
		}
	}
	return kept
}

func emit(rows []row, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}

	// Present readings under their group, the way both HWiNFO and the sensor
	// tree do.
	byGroup := make(map[string][]row)
	for _, r := range rows {
		byGroup[r.Group] = append(byGroup[r.Group], r)
	}
	groups := make([]string, 0, len(byGroup))
	for name := range byGroup {
		groups = append(groups, name)
	}
	sort.Strings(groups)

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	for _, name := range groups {
		fmt.Fprintf(w, "\n%s\n", name)
		fmt.Fprintln(w, "  TYPE\tNAME\tVALUE\tMIN\tMAX\tAVG\tSENSOR")
		for _, r := range byGroup[name] {
			fmt.Fprintf(w, "  %s\t%s\t%s\t%.2f\t%.2f\t%.2f\t%s\n",
				r.Type, r.Name, r.reading(), r.Min, r.Max, r.Avg, r.address())
		}
	}
	fmt.Fprintf(w, "\n%d sensors\n", len(rows))
	return w.Flush()
}
