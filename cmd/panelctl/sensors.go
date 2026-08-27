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

	"github.com/n-kisyov/wininfopanel/internal/sensor/hwinfo"
)

func runSensors(ctx context.Context, args []string) error {
	fs := newFlagSet("sensors")
	source := fs.String("source", "hwinfo", "sensor source to read: hwinfo")
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
	default:
		// The native monitor and plugin sources arrive in later milestones;
		// naming them here would promise something that does not work yet.
		return fmt.Errorf("unknown source %q (available: hwinfo)", *source)
	}
}

func sensorsFromHWiNFO(ctx context.Context, asJSON bool, filter string, watch time.Duration) error {
	reader := hwinfo.New()

	if watch <= 0 {
		// A single shot still needs the reader's polling machinery, so run it
		// just long enough for one poll to land.
		pollCtx, cancel := context.WithCancel(ctx)
		go func() {
			reader.Run(pollCtx)
		}()
		defer cancel()

		if err := waitForFirstPoll(ctx, reader); err != nil {
			return err
		}
		return emitSensors(reader, asJSON, filter)
	}

	reader.SetInterval(watch)
	pollCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		reader.Run(pollCtx)
	}()

	if err := waitForFirstPoll(ctx, reader); err != nil {
		return err
	}

	ticker := time.NewTicker(watch)
	defer ticker.Stop()
	for {
		if err := emitSensors(reader, asJSON, filter); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// waitForFirstPoll blocks until the reader has data or it becomes clear
// HWiNFO is not publishing.
func waitForFirstPoll(ctx context.Context, reader *hwinfo.Reader) error {
	const timeout = 3 * time.Second

	deadline := time.After(timeout)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	for {
		if reader.Available() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			if err := reader.LastError(); err != nil {
				return fmt.Errorf("HWiNFO shared memory is not readable: %w", err)
			}
			return fmt.Errorf("HWiNFO shared memory is not available; " +
				"start HWiNFO and enable Settings > Shared Memory Support")
		case <-ticker.C:
		}
	}
}

func emitSensors(reader *hwinfo.Reader, asJSON bool, filter string) error {
	entries := reader.Entries()

	if filter != "" {
		needle := strings.ToLower(filter)
		kept := entries[:0]
		for _, e := range entries {
			if strings.Contains(strings.ToLower(e.Name), needle) ||
				strings.Contains(strings.ToLower(e.GroupName), needle) {
				kept = append(kept, e)
			}
		}
		entries = kept
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}

	// Group readings under their sensor group, the way HWiNFO presents them.
	byGroup := make(map[string][]hwinfo.Entry)
	var groupOrder []string
	for _, e := range entries {
		if _, seen := byGroup[e.GroupName]; !seen {
			groupOrder = append(groupOrder, e.GroupName)
		}
		byGroup[e.GroupName] = append(byGroup[e.GroupName], e)
	}
	sort.Strings(groupOrder)

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	for _, groupName := range groupOrder {
		fmt.Fprintf(w, "\n%s\n", groupName)
		fmt.Fprintln(w, "  TYPE\tNAME\tVALUE\tMIN\tMAX\tAVG\tKEY")
		for _, e := range byGroup[groupName] {
			fmt.Fprintf(w, "  %s\t%s\t%.2f%s\t%.2f\t%.2f\t%.2f\t%d/%d/%d\n",
				e.Type, e.Name, e.Value, e.Unit, e.Min, e.Max, e.Avg,
				e.Key.ID, e.Key.Instance, e.Key.EntryID)
		}
	}
	fmt.Fprintf(w, "\n%d sensors\n", len(entries))
	return w.Flush()
}
