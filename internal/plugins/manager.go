package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/logging"
	"github.com/n-kisyov/wininfopanel/internal/sensor"
	"github.com/n-kisyov/wininfopanel/pkg/plugin"
)

// Manager runs the discovered plugins and serves their values.
//
// It implements sensor.Resolver, so plugin values reach display items through
// exactly the same path as HWiNFO's and the built-in monitor's.
type Manager struct {
	log  *slog.Logger
	opts ManagerOptions

	mu      sync.RWMutex
	running map[string]*runningPlugin
	// entries holds every published value, keyed by its sensor path.
	entries map[string]*Entry
	// order preserves publication order so the sensor tree is stable.
	order []string
	// enabled records which plugins the user has switched on.
	enabled map[string]bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// ManagerOptions configures a Manager.
type ManagerOptions struct {
	// BundledDir holds plugins shipped with the application.
	BundledDir string
	// ExternalDir holds user-installed plugins.
	ExternalDir string
	// ConfigDir is where plugin configuration is persisted.
	ConfigDir string

	// MaxRestarts is how many times a crashed plugin is restarted inside
	// RestartWindow before it is given up on.
	MaxRestarts int
	// RestartWindow is the period MaxRestarts applies over.
	RestartWindow time.Duration
}

// Restart defaults, matching InfoPanel: three attempts in a minute is enough
// to ride out a transient failure without spinning on a plugin that is simply
// broken.
const (
	DefaultMaxRestarts   = 3
	DefaultRestartWindow = time.Minute
)

// Entry is one value a plugin publishes.
type Entry struct {
	// Path addresses the entry as "pluginID/containerID/entryID". It is what a
	// saved layout stores, so it must stay stable across restarts.
	Path string `json:"path"`

	PluginID   string `json:"pluginId"`
	PluginName string `json:"pluginName"`
	Container  string `json:"container"`

	Name string           `json:"name"`
	Type plugin.EntryType `json:"type"`
	Unit string           `json:"unit,omitempty"`

	Value float64 `json:"value,omitempty"`
	Min   float64 `json:"min,omitempty"`
	Max   float64 `json:"max,omitempty"`
	Avg   float64 `json:"avg,omitempty"`
	Text  string  `json:"text,omitempty"`

	Table *plugin.Table `json:"table,omitempty"`
}

// runningPlugin tracks one live plugin and its restart history.
type runningPlugin struct {
	descriptor Descriptor
	client     *client

	// restarts records when this plugin was last restarted, so repeated
	// failures can be distinguished from an isolated one.
	restarts []time.Time
	failed   bool
}

// NewManager returns a manager that is not yet running.
func NewManager(opts ManagerOptions) *Manager {
	if opts.MaxRestarts <= 0 {
		opts.MaxRestarts = DefaultMaxRestarts
	}
	if opts.RestartWindow <= 0 {
		opts.RestartWindow = DefaultRestartWindow
	}

	return &Manager{
		log:     logging.For("plugins"),
		opts:    opts,
		running: make(map[string]*runningPlugin),
		entries: make(map[string]*Entry),
		enabled: make(map[string]bool),
	}
}

// Discover lists the plugins available to run.
func (m *Manager) Discover() []Descriptor {
	return Discover(m.opts.BundledDir, m.opts.ExternalDir)
}

// Start begins running the enabled plugins.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	m.ctx, m.cancel = context.WithCancel(ctx)
	runCtx := m.ctx
	m.mu.Unlock()

	m.loadEnabledState()

	for _, descriptor := range m.Discover() {
		if !m.isEnabled(descriptor.Name) {
			continue
		}
		m.startPlugin(runCtx, descriptor)
	}
	return nil
}

// Stop shuts every plugin down.
func (m *Manager) Stop() {
	m.mu.Lock()
	cancel := m.cancel
	running := make([]*runningPlugin, 0, len(m.running))
	for name, entry := range m.running {
		running = append(running, entry)
		delete(m.running, name)
	}
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	for _, entry := range running {
		if entry.client != nil {
			entry.client.stop()
		}
	}
	m.wg.Wait()
}

// startPlugin launches one plugin and supervises it.
func (m *Manager) startPlugin(ctx context.Context, descriptor Descriptor) {
	entry := &runningPlugin{descriptor: descriptor}

	m.mu.Lock()
	m.running[descriptor.Name] = entry
	m.mu.Unlock()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.supervise(ctx, entry)
	}()
}

// newClientFor builds a client wired to this manager's callbacks.
//
// A client owns one process and one pipe, both of which are gone once it
// stops, so every attempt gets a fresh one rather than reusing a spent client.
func (m *Manager) newClientFor(entry *runningPlugin) *client {
	descriptor := entry.descriptor

	c := newClient(descriptor, nil, nil)

	// The callbacks close over the client itself rather than over the entry.
	// Reading entry.client would race with a restart swapping it, and could
	// attribute one client's values to another's identity.
	c.onContainers = func(containers []plugin.Container) {
		m.replaceContainers(descriptor, c.metadata(), containers)
	}
	c.onValues = func(updates []plugin.EntryUpdate) {
		m.applyUpdates(c.metadata(), updates)
	}
	return c
}

// supervise keeps a plugin running, restarting it a bounded number of times.
func (m *Manager) supervise(ctx context.Context, entry *runningPlugin) {
	for {
		if ctx.Err() != nil {
			return
		}

		client := m.newClientFor(entry)

		m.mu.Lock()
		entry.client = client
		m.mu.Unlock()

		if err := client.start(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			m.log.Error("plugin failed to start",
				"plugin", entry.descriptor.Name, "error", err)

			if !m.recordRestart(entry) {
				return
			}
			// Back off before retrying, so a plugin that fails instantly does
			// not burn a CPU spinning through its restart budget.
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}

		// Restore the user's saved settings before the plugin publishes
		// anything, so its first values already reflect them.
		m.restoreConfig(ctx, entry)

		// Returns only when the plugin disconnects.
		client.waitClosed()

		if ctx.Err() != nil {
			return
		}

		m.log.Warn("plugin stopped; restarting", "plugin", entry.descriptor.Name)
		m.removeEntriesFor(entry.descriptor.Name)

		if !m.recordRestart(entry) {
			return
		}
	}
}

// recordRestart notes a restart and reports whether another is allowed.
func (m *Manager) recordRestart(entry *runningPlugin) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-m.opts.RestartWindow)

	// Only failures inside the window count, so a plugin that has been stable
	// for hours is not penalised for a crash long ago.
	recent := entry.restarts[:0]
	for _, at := range entry.restarts {
		if at.After(cutoff) {
			recent = append(recent, at)
		}
	}
	entry.restarts = append(recent, now)

	if len(entry.restarts) > m.opts.MaxRestarts {
		entry.failed = true
		m.log.Error("plugin restarted too often; giving up",
			"plugin", entry.descriptor.Name,
			"attempts", len(entry.restarts),
			"window", m.opts.RestartWindow)
		return false
	}
	return true
}

// replaceContainers installs a plugin's published entries.
func (m *Manager) replaceContainers(descriptor Descriptor, info plugin.HelloResponse,
	containers []plugin.Container) {

	m.mu.Lock()
	defer m.mu.Unlock()

	// Drop anything this plugin published before, so a reload does not leave
	// entries behind that the plugin no longer offers.
	m.removeEntriesLocked(descriptor.Name)

	for _, container := range containers {
		for _, published := range container.Entries {
			path := fmt.Sprintf("%s/%s/%s", info.ID, container.ID, published.ID)

			m.entries[path] = &Entry{
				Path:       path,
				PluginID:   info.ID,
				PluginName: descriptor.Name,
				Container:  container.Name,
				Name:       published.Name,
				Type:       published.Type,
				Unit:       published.Unit,
				Value:      published.Value,
				Min:        published.Min,
				Max:        published.Max,
				Avg:        published.Avg,
				Text:       published.Text,
				Table:      published.Table,
			}
			m.order = append(m.order, path)
		}
	}
}

// applyUpdates records changed values.
func (m *Manager) applyUpdates(info plugin.HelloResponse, updates []plugin.EntryUpdate) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, update := range updates {
		path := info.ID + "/" + update.Path

		existing, ok := m.entries[path]
		if !ok {
			// A value for an entry that was never declared cannot be bound to
			// anything, so there is nothing useful to do with it.
			continue
		}

		existing.Value = update.Value
		existing.Min = update.Min
		existing.Max = update.Max
		existing.Avg = update.Avg
		if update.Text != "" {
			existing.Text = update.Text
		}
		if update.Table != nil {
			existing.Table = update.Table
		}
	}
}

// removeEntriesFor drops a plugin's entries, so a dead plugin's sensors stop
// resolving rather than showing a stale last value forever.
func (m *Manager) removeEntriesFor(pluginName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeEntriesLocked(pluginName)
}

func (m *Manager) removeEntriesLocked(pluginName string) {
	kept := m.order[:0]
	for _, path := range m.order {
		if existing, ok := m.entries[path]; ok && existing.PluginName == pluginName {
			delete(m.entries, path)
			continue
		}
		kept = append(kept, path)
	}
	m.order = kept
}

// Read implements sensor.Resolver.
func (m *Manager) Read(key sensor.Key) (sensor.Reading, bool) {
	if key.Source != sensor.SourcePlugin {
		return sensor.Reading{}, false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.entries[key.Path]
	if !ok {
		return sensor.Reading{}, false
	}

	return sensor.Reading{
		Name: entry.Name,
		Unit: entry.Unit,
		Now:  entry.Value,
		Min:  entry.Min,
		Max:  entry.Max,
		Avg:  entry.Avg,
		Text: entry.Text,
	}, true
}

// Entries returns every published value, in publication order.
func (m *Manager) Entries() []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Entry, 0, len(m.order))
	for _, path := range m.order {
		if entry, ok := m.entries[path]; ok {
			out = append(out, *entry)
		}
	}
	return out
}

// Available reports whether any plugin is publishing.
func (m *Manager) Available() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.entries) > 0
}

// Status describes one plugin's current state, for the Plugins page.
type Status struct {
	Descriptor Descriptor `json:"descriptor"`

	Running bool `json:"running"`
	Enabled bool `json:"enabled"`
	// Failed marks a plugin that restarted too often and was given up on.
	Failed bool `json:"failed"`

	PluginID string `json:"pluginId,omitempty"`
	Version  string `json:"version,omitempty"`
	// EntryCount is how many values it publishes.
	EntryCount int `json:"entryCount"`

	Actions      []plugin.ActionInfo `json:"actions,omitempty"`
	Configurable bool                `json:"configurable"`
}

// Statuses describes every discovered plugin.
func (m *Manager) Statuses() []Status {
	discovered := m.Discover()

	m.mu.RLock()
	defer m.mu.RUnlock()

	counts := make(map[string]int)
	for _, entry := range m.entries {
		counts[entry.PluginName]++
	}

	out := make([]Status, 0, len(discovered))
	for _, descriptor := range discovered {
		status := Status{
			Descriptor: descriptor,
			Enabled:    m.isEnabledLocked(descriptor.Name),
			EntryCount: counts[descriptor.Name],
		}

		if entry, ok := m.running[descriptor.Name]; ok && entry.client != nil {
			info := entry.client.metadata()
			status.Running = !entry.failed && info.ID != ""
			status.Failed = entry.failed
			status.PluginID = info.ID
			status.Version = info.Version
			status.Actions = info.Actions
			status.Configurable = info.Configurable
		}
		out = append(out, status)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Descriptor.Name < out[j].Descriptor.Name
	})
	return out
}

// Invoke runs one of a plugin's actions.
func (m *Manager) Invoke(ctx context.Context, pluginName, action string) error {
	client, ok := m.clientFor(pluginName)
	if !ok {
		return fmt.Errorf("plugin %s is not running", pluginName)
	}
	return client.invoke(ctx, action)
}

// clientFor returns a plugin's live client.
func (m *Manager) clientFor(pluginName string) (*client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.running[pluginName]
	if !ok || entry.client == nil {
		return nil, false
	}
	return entry.client, true
}

// Config reads a plugin's configuration properties.
func (m *Manager) Config(ctx context.Context, pluginName string) ([]plugin.ConfigProperty, error) {
	client, ok := m.clientFor(pluginName)
	if !ok {
		return nil, fmt.Errorf("plugin %s is not running", pluginName)
	}
	return client.config(ctx)
}

// SetConfig applies one configuration value and persists it, so it survives a
// restart without the plugin having to write a file of its own.
func (m *Manager) SetConfig(ctx context.Context, pluginName, key string, value any) error {
	client, ok := m.clientFor(pluginName)
	if !ok {
		return fmt.Errorf("plugin %s is not running", pluginName)
	}

	if err := client.setConfig(ctx, key, value); err != nil {
		return err
	}
	return m.persistConfig(ctx, pluginName, client)
}

// SetEnabled turns a plugin on or off and persists the choice.
func (m *Manager) SetEnabled(pluginName string, enabled bool) error {
	m.mu.Lock()
	m.enabled[pluginName] = enabled
	ctx := m.ctx
	m.mu.Unlock()

	if err := m.saveEnabledState(); err != nil {
		return err
	}

	if enabled {
		for _, descriptor := range m.Discover() {
			if descriptor.Name == pluginName {
				if _, running := m.clientFor(pluginName); !running && ctx != nil {
					m.startPlugin(ctx, descriptor)
				}
				return nil
			}
		}
		return fmt.Errorf("plugin %s was not found", pluginName)
	}

	if client, running := m.clientFor(pluginName); running {
		client.stop()
		m.removeEntriesFor(pluginName)

		m.mu.Lock()
		delete(m.running, pluginName)
		m.mu.Unlock()
	}
	return nil
}

func (m *Manager) isEnabled(pluginName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.isEnabledLocked(pluginName)
}

// isEnabledLocked reports whether a plugin should run. Caller holds m.mu.
//
// Absence means enabled: a freshly installed plugin should work without the
// user having to find a switch first. Every caller must go through here, or a
// never-toggled plugin reports itself disabled while it is plainly running.
func (m *Manager) isEnabledLocked(pluginName string) bool {
	enabled, recorded := m.enabled[pluginName]
	return !recorded || enabled
}

// enabledStateFile records which plugins are switched off.
const enabledStateFile = "plugins.json"

func (m *Manager) loadEnabledState() {
	if m.opts.ConfigDir == "" {
		return
	}

	data, err := os.ReadFile(filepath.Join(m.opts.ConfigDir, enabledStateFile))
	if err != nil {
		return
	}

	var state map[string]bool
	if err := json.Unmarshal(data, &state); err != nil {
		m.log.Warn("could not read plugin state; enabling all plugins", "error", err)
		return
	}

	m.mu.Lock()
	m.enabled = state
	m.mu.Unlock()
}

func (m *Manager) saveEnabledState() error {
	if m.opts.ConfigDir == "" {
		return nil
	}
	if err := os.MkdirAll(m.opts.ConfigDir, 0o755); err != nil {
		return err
	}

	m.mu.RLock()
	state := make(map[string]bool, len(m.enabled))
	for name, enabled := range m.enabled {
		state[name] = enabled
	}
	m.mu.RUnlock()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.opts.ConfigDir, enabledStateFile), data, 0o644)
}

// configPath is where one plugin's settings are stored.
func (m *Manager) configPath(pluginName string) string {
	return filepath.Join(m.opts.ConfigDir, pluginName+".config.json")
}

// persistConfig saves a plugin's current settings.
func (m *Manager) persistConfig(ctx context.Context, pluginName string, client *client) error {
	if m.opts.ConfigDir == "" {
		return nil
	}

	properties, err := client.config(ctx)
	if err != nil {
		return err
	}

	values := make(map[string]any, len(properties))
	for _, property := range properties {
		values[property.Key] = property.Value
	}

	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.opts.ConfigDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(m.configPath(pluginName), data, 0o644)
}

// restoreConfig replays saved settings into a freshly started plugin.
func (m *Manager) restoreConfig(ctx context.Context, entry *runningPlugin) {
	client, ok := m.clientFor(entry.descriptor.Name)
	if !ok || m.opts.ConfigDir == "" || !client.metadata().Configurable {
		return
	}

	data, err := os.ReadFile(m.configPath(entry.descriptor.Name))
	if err != nil {
		return // no saved settings, which is the normal first-run case
	}

	var values map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		m.log.Warn("could not read saved plugin settings",
			"plugin", entry.descriptor.Name, "error", err)
		return
	}

	for key, value := range values {
		if err := client.setConfig(ctx, key, value); err != nil {
			m.log.Warn("could not restore a plugin setting",
				"plugin", entry.descriptor.Name, "key", key, "error", err)
		}
	}
}
