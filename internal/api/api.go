// Package api is wininfopanel's application surface: everything the UI can
// ask the engine to do, expressed once and independently of transport.
//
// The desktop shell binds to it directly and the built-in web server exposes
// it over HTTP. Keeping one surface means a feature works in both places
// without being written twice, and lets the desktop shell be replaced without
// touching application logic.
package api

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/config/store"
	"github.com/n-kisyov/wininfopanel/internal/logging"
	"github.com/n-kisyov/wininfopanel/internal/plugins"
	"github.com/n-kisyov/wininfopanel/internal/render/font"
	"github.com/n-kisyov/wininfopanel/internal/sensor"
	"github.com/n-kisyov/wininfopanel/internal/sensor/hwinfo"
	"github.com/n-kisyov/wininfopanel/internal/sensor/native"
	"github.com/n-kisyov/wininfopanel/internal/winapi"
	"github.com/n-kisyov/wininfopanel/pkg/plugin"
)

// Service implements the application surface.
type Service struct {
	log *slog.Logger

	store *store.Store
	undo  *store.UndoManager

	// Sensor sources are optional: the app runs with whichever are enabled.
	hwinfo *hwinfo.Reader
	native *native.Monitor

	fonts   *font.Cache
	plugins *plugins.Manager

	// onProfilesChanged lets the display manager re-sync when profiles change.
	onProfilesChanged func()
}

// Options configures a Service.
type Options struct {
	Store   *store.Store
	Undo    *store.UndoManager
	HWiNFO  *hwinfo.Reader
	Native  *native.Monitor
	Plugins *plugins.Manager
	Fonts   *font.Cache

	// OnProfilesChanged fires after any change that could alter which
	// overlays should be showing.
	OnProfilesChanged func()
}

// New returns a service over the given store.
func New(opts Options) *Service {
	undo := opts.Undo
	if undo == nil {
		undo = store.NewUndoManager(store.DefaultUndoDepth)
	}

	return &Service{
		log:               logging.For("api"),
		store:             opts.Store,
		undo:              undo,
		hwinfo:            opts.HWiNFO,
		native:            opts.Native,
		fonts:             opts.Fonts,
		plugins:           opts.Plugins,
		onProfilesChanged: opts.OnProfilesChanged,
	}
}

func (s *Service) profilesChanged() {
	if s.onProfilesChanged != nil {
		s.onProfilesChanged()
	}
}

// Status summarizes the application's current state, for the Home page.
type Status struct {
	Version string `json:"version"`
	Uptime  string `json:"uptime"`

	Sources []SourceStatus `json:"sources"`

	ProfileCount int `json:"profileCount"`
	ActiveCount  int `json:"activeCount"`
}

// SourceStatus describes one sensor source's availability.
type SourceStatus struct {
	Source sensor.Source `json:"source"`
	Name   string        `json:"name"`
	// Available reports whether the source is producing readings now.
	Available bool `json:"available"`
	// SensorCount is how many sensors it currently exposes.
	SensorCount int `json:"sensorCount"`
	// Detail explains an unavailable source, e.g. that HWiNFO is not running.
	Detail string `json:"detail,omitempty"`
}

var startedAt = time.Now()

// Status returns the current application state.
func (s *Service) Status() Status {
	profiles := s.store.Profiles()
	active := 0
	for _, p := range profiles {
		if p.Active {
			active++
		}
	}

	return Status{
		Version:      Version,
		Uptime:       time.Since(startedAt).Round(time.Second).String(),
		Sources:      s.sourceStatuses(),
		ProfileCount: len(profiles),
		ActiveCount:  active,
	}
}

// Version is stamped at build time.
var Version = "dev"

func (s *Service) sourceStatuses() []SourceStatus {
	var out []SourceStatus

	if s.hwinfo != nil {
		status := SourceStatus{
			Source:      sensor.SourceHWiNFO,
			Name:        "HWiNFO",
			Available:   s.hwinfo.Available(),
			SensorCount: len(s.hwinfo.Entries()),
		}
		if !status.Available {
			status.Detail = "HWiNFO is not running, or Shared Memory Support is not enabled in its settings"
			if err := s.hwinfo.LastError(); err != nil {
				status.Detail = err.Error()
			}
		}
		out = append(out, status)
	}

	if s.native != nil {
		out = append(out, SourceStatus{
			Source:      sensor.SourceNative,
			Name:        "Built-in monitor",
			Available:   s.native.Available(),
			SensorCount: len(s.native.Entries()),
		})
	}

	if s.plugins != nil {
		status := SourceStatus{
			Source:      sensor.SourcePlugin,
			Name:        "Plugins",
			Available:   s.plugins.Available(),
			SensorCount: len(s.plugins.Entries()),
		}
		if !status.Available {
			status.Detail = "No plugin is publishing values"
		}
		out = append(out, status)
	}

	return out
}

// SensorNode is one entry in the sensor picker tree.
type SensorNode struct {
	// Group is the section a sensor belongs under, e.g. "CPU".
	Group string `json:"group"`
	// Sensors are the readings in that group.
	Sensors []SensorEntry `json:"sensors"`
}

// SensorEntry is one selectable sensor.
type SensorEntry struct {
	Key   sensor.Key `json:"key"`
	Name  string     `json:"name"`
	Type  string     `json:"type"`
	Unit  string     `json:"unit"`
	Value float64    `json:"value"`
	Min   float64    `json:"min"`
	Max   float64    `json:"max"`
	Avg   float64    `json:"avg"`
	Text  string     `json:"text,omitempty"`
}

// Sensors returns the sensor tree for one source, grouped and sorted.
//
// Groups are sorted, but sensors within a group keep their source order: for
// HWiNFO that is the order the hardware reports them, which is meaningful and
// which alphabetizing would destroy.
func (s *Service) Sensors(source sensor.Source) ([]SensorNode, error) {
	switch source {
	case sensor.SourceHWiNFO:
		if s.hwinfo == nil {
			return nil, fmt.Errorf("the HWiNFO source is not enabled")
		}
		// Read the list once: it is replaced wholesale on every poll, so two
		// reads could disagree.
		entries := s.hwinfo.Entries()
		grouped := make([]groupedEntry, 0, len(entries))
		for _, e := range entries {
			grouped = append(grouped, groupedEntry{
				group: e.GroupName,
				entry: SensorEntry{
					Key: e.Key, Name: e.Name, Type: e.Type, Unit: e.Unit,
					Value: e.Value, Min: e.Min, Max: e.Max, Avg: e.Avg,
				},
			})
		}
		return groupSensors(grouped), nil

	case sensor.SourceNative:
		if s.native == nil {
			return nil, fmt.Errorf("the built-in monitor is not enabled")
		}
		entries := s.native.Entries()
		grouped := make([]groupedEntry, 0, len(entries))
		for _, e := range entries {
			grouped = append(grouped, groupedEntry{
				group: e.GroupName,
				entry: SensorEntry{
					Key: e.Key, Name: e.Name, Type: e.Type, Unit: e.Unit,
					Value: e.Value, Min: e.Min, Max: e.Max, Avg: e.Avg, Text: e.Text,
				},
			})
		}
		return groupSensors(grouped), nil

	case sensor.SourcePlugin:
		if s.plugins == nil {
			return nil, fmt.Errorf("the plugin system is not enabled")
		}
		entries := s.plugins.Entries()
		grouped := make([]groupedEntry, 0, len(entries))
		for _, e := range entries {
			grouped = append(grouped, groupedEntry{
				// Plugin entries are grouped by plugin and container together,
				// so two plugins with a container of the same name stay apart.
				group: e.PluginName + " / " + e.Container,
				entry: SensorEntry{
					Key:  sensor.Key{Source: sensor.SourcePlugin, Path: e.Path},
					Name: e.Name, Type: string(e.Type), Unit: e.Unit,
					Value: e.Value, Min: e.Min, Max: e.Max, Avg: e.Avg, Text: e.Text,
				},
			})
		}
		return groupSensors(grouped), nil

	default:
		return nil, fmt.Errorf("unknown sensor source %q", source)
	}
}

// PluginStatuses describes every discovered plugin, for the Plugins page.
func (s *Service) PluginStatuses() []plugins.Status {
	if s.plugins == nil {
		return nil
	}
	return s.plugins.Statuses()
}

// InvokePluginAction runs one of a plugin's actions.
func (s *Service) InvokePluginAction(ctx context.Context, pluginName, action string) error {
	if s.plugins == nil {
		return fmt.Errorf("the plugin system is not enabled")
	}
	return s.plugins.Invoke(ctx, pluginName, action)
}

// PluginConfig reads a plugin's settings.
func (s *Service) PluginConfig(ctx context.Context, pluginName string) ([]plugin.ConfigProperty, error) {
	if s.plugins == nil {
		return nil, fmt.Errorf("the plugin system is not enabled")
	}
	return s.plugins.Config(ctx, pluginName)
}

// SetPluginConfig applies and persists one of a plugin's settings.
func (s *Service) SetPluginConfig(ctx context.Context, pluginName, key string, value any) error {
	if s.plugins == nil {
		return fmt.Errorf("the plugin system is not enabled")
	}
	return s.plugins.SetConfig(ctx, pluginName, key, value)
}

// SetPluginEnabled turns a plugin on or off.
func (s *Service) SetPluginEnabled(pluginName string, enabled bool) error {
	if s.plugins == nil {
		return fmt.Errorf("the plugin system is not enabled")
	}
	return s.plugins.SetEnabled(pluginName, enabled)
}

// groupedEntry pairs a sensor with the tree section it belongs under.
type groupedEntry struct {
	group string
	entry SensorEntry
}

// groupSensors buckets entries by group, preserving within-group order.
func groupSensors(entries []groupedEntry) []SensorNode {
	byGroup := make(map[string][]SensorEntry)
	for _, e := range entries {
		byGroup[e.group] = append(byGroup[e.group], e.entry)
	}

	nodes := make([]SensorNode, 0, len(byGroup))
	for group, sensors := range byGroup {
		nodes = append(nodes, SensorNode{Group: group, Sensors: sensors})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Group < nodes[j].Group })
	return nodes
}

// Settings returns the current application settings.
func (s *Service) Settings() model.Settings { return s.store.Settings() }

// UpdateSettings applies a settings change and persists it.
func (s *Service) UpdateSettings(apply func(*model.Settings)) (model.Settings, error) {
	if err := s.store.UpdateSettings(apply); err != nil {
		return model.Settings{}, err
	}
	s.profilesChanged()
	return s.store.Settings(), nil
}

// Profiles returns every profile.
func (s *Service) Profiles() []*model.Profile { return s.store.Profiles() }

// Profile returns one profile.
func (s *Service) Profile(id string) (*model.Profile, error) {
	profile, ok := s.store.Profile(id)
	if !ok {
		return nil, fmt.Errorf("profile %s not found", id)
	}
	return profile, nil
}

// CreateProfile adds a profile and returns it.
func (s *Service) CreateProfile(name string, width, height int) (*model.Profile, error) {
	if name == "" {
		name = "New Profile"
	}
	if width < 1 || height < 1 {
		return nil, fmt.Errorf("profile size must be positive, got %dx%d", width, height)
	}

	profile := model.NewProfile(name, width, height)
	if err := s.store.AddProfile(profile); err != nil {
		return nil, err
	}
	s.profilesChanged()
	return profile, nil
}

// DuplicateProfile copies a profile along with its layout.
func (s *Service) DuplicateProfile(id string) (*model.Profile, error) {
	original, ok := s.store.Profile(id)
	if !ok {
		return nil, fmt.Errorf("profile %s not found", id)
	}

	clone := original.Clone()
	clone.Name = original.Name + " (copy)"
	if err := s.store.AddProfile(clone); err != nil {
		return nil, err
	}

	// The layout is duplicated rather than cloned, so the copy's items carry
	// fresh IDs and edits to one profile cannot reach the other.
	items, err := s.store.Layout(id)
	if err != nil {
		return nil, err
	}
	if err := s.store.SetLayout(clone.ID, model.DuplicateAll(items)); err != nil {
		return nil, err
	}

	s.profilesChanged()
	return clone, nil
}

// UpdateProfile applies a change to a profile.
func (s *Service) UpdateProfile(id string, apply func(*model.Profile)) (*model.Profile, error) {
	if err := s.store.UpdateProfile(id, apply); err != nil {
		return nil, err
	}
	s.profilesChanged()
	return s.Profile(id)
}

// DeleteProfile removes a profile, its layout, and its assets.
func (s *Service) DeleteProfile(id string) error {
	if err := s.store.RemoveProfile(id); err != nil {
		return err
	}
	s.undo.Clear(id)
	s.profilesChanged()
	return nil
}

// ReorderProfiles sets the profile order.
func (s *Service) ReorderProfiles(ids []string) error {
	if err := s.store.ReorderProfiles(ids); err != nil {
		return err
	}
	s.profilesChanged()
	return nil
}

// requireProfile reports an error unless the profile exists.
//
// The store loads layouts lazily, so a missing layout file is indistinguishable
// from an empty layout there. Without this guard a mistyped profile ID would
// read back as an empty panel rather than an error, and a write would leave an
// orphan layout file behind.
func (s *Service) requireProfile(profileID string) error {
	if _, ok := s.store.Profile(profileID); !ok {
		return fmt.Errorf("profile %s not found", profileID)
	}
	return nil
}

// Layout returns a profile's display items.
func (s *Service) Layout(profileID string) (model.ItemList, error) {
	if err := s.requireProfile(profileID); err != nil {
		return nil, err
	}
	return s.store.Layout(profileID)
}

// SetLayout replaces a profile's display items, recording an undo point.
func (s *Service) SetLayout(profileID string, items model.ItemList) error {
	if err := s.snapshot(profileID); err != nil {
		return err
	}
	return s.store.SetLayout(profileID, items)
}

// AddItem appends a display item to a profile.
func (s *Service) AddItem(profileID string, item model.DisplayItem) error {
	if item == nil {
		return fmt.Errorf("no item given")
	}
	if err := s.snapshot(profileID); err != nil {
		return err
	}
	return s.store.Mutate(profileID, func(items model.ItemList) model.ItemList {
		return append(items, item)
	})
}

// UpdateItem replaces one display item in place, matched by ID.
func (s *Service) UpdateItem(profileID string, item model.DisplayItem) error {
	if item == nil || item.Base().ID == "" {
		return fmt.Errorf("item must have an id")
	}
	if err := s.snapshot(profileID); err != nil {
		return err
	}

	replaced := false
	err := s.store.Mutate(profileID, func(items model.ItemList) model.ItemList {
		replaced = replaceItem(items, item)
		return items
	})
	if err != nil {
		return err
	}
	if !replaced {
		return fmt.Errorf("item %s not found in profile %s", item.Base().ID, profileID)
	}
	return nil
}

// replaceItem swaps an item into the tree, descending into groups.
func replaceItem(items model.ItemList, replacement model.DisplayItem) bool {
	id := replacement.Base().ID
	for i, item := range items {
		if item.Base().ID == id {
			items[i] = replacement
			return true
		}
		if group, ok := item.(*model.GroupItem); ok {
			if replaceItem(group.Items, replacement) {
				return true
			}
		}
	}
	return false
}

// DeleteItems removes display items by ID.
func (s *Service) DeleteItems(profileID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := s.snapshot(profileID); err != nil {
		return err
	}

	remove := make(map[string]bool, len(ids))
	for _, id := range ids {
		remove[id] = true
	}

	return s.store.Mutate(profileID, func(items model.ItemList) model.ItemList {
		return removeItems(items, remove)
	})
}

// removeItems filters an item tree, descending into groups.
func removeItems(items model.ItemList, remove map[string]bool) model.ItemList {
	kept := make(model.ItemList, 0, len(items))
	for _, item := range items {
		if remove[item.Base().ID] {
			continue
		}
		if group, ok := item.(*model.GroupItem); ok {
			group.Items = removeItems(group.Items, remove)
		}
		kept = append(kept, item)
	}
	return kept
}

// snapshot records the current layout for undo, before a change is applied.
//
// Every mutating operation calls it first, so it is also where the profile's
// existence is checked once for all of them.
func (s *Service) snapshot(profileID string) error {
	items, err := s.Layout(profileID)
	if err != nil {
		return err
	}
	return s.undo.Snapshot(profileID, items)
}

// Undo reverts the last layout change.
func (s *Service) Undo(profileID string) (model.ItemList, error) {
	return s.step(profileID, s.undo.Undo)
}

// Redo re-applies the last undone layout change.
func (s *Service) Redo(profileID string) (model.ItemList, error) {
	return s.step(profileID, s.undo.Redo)
}

func (s *Service) step(profileID string,
	move func(string, model.ItemList) (model.ItemList, bool, error)) (model.ItemList, error) {

	current, err := s.Layout(profileID)
	if err != nil {
		return nil, err
	}

	restored, ok, err := move(profileID, current)
	if err != nil {
		return nil, err
	}
	if !ok {
		return current, nil
	}

	if err := s.store.SetLayout(profileID, restored); err != nil {
		return nil, err
	}
	return restored, nil
}

// CanUndo reports whether an undo is available.
func (s *Service) CanUndo(profileID string) bool { return s.undo.CanUndo(profileID) }

// CanRedo reports whether a redo is available.
func (s *Service) CanRedo(profileID string) bool { return s.undo.CanRedo(profileID) }

// Fonts lists the installed font families, for the design UI's font picker.
func (s *Service) Fonts() ([]string, error) {
	if s.fonts == nil {
		return nil, fmt.Errorf("no font cache is attached")
	}
	return s.fonts.Families()
}

// Screen describes a connected display, for positioning overlays.
type Screen struct {
	DeviceName string `json:"deviceName"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	X          int    `json:"x"`
	Y          int    `json:"y"`
	Primary    bool   `json:"primary"`
	DPI        int    `json:"dpi"`
}

// Screens lists the connected displays.
func (s *Service) Screens() ([]Screen, error) {
	found, err := winapi.Screens()
	if err != nil {
		return nil, err
	}

	out := make([]Screen, 0, len(found))
	for _, screen := range found {
		out = append(out, Screen{
			DeviceName: screen.DeviceName,
			Width:      screen.Bounds.Width(),
			Height:     screen.Bounds.Height(),
			X:          screen.Bounds.Left,
			Y:          screen.Bounds.Top,
			Primary:    screen.Primary,
			DPI:        screen.DPI,
		})
	}
	return out, nil
}
