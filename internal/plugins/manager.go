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
	// started guards against a second Start, which would supervise every
	// plugin twice and run two processes for each.
	started bool
	wg      sync.WaitGroup
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

	// cancel ends this plugin's supervisor without disturbing the others. It
	// also unblocks a supervisor sitting in its restart backoff, so switching a
	// plugin off takes effect now rather than seconds from now.
	cancel context.CancelFunc
	// stopping marks a stop somebody asked for, so the supervisor can tell it
	// apart from a crash.
	//
	// Cancelling the context cannot carry that meaning on its own: it kills the
	// process through exec.CommandContext, which would take the plugin's own
	// shutdown handling with it. So a deliberate stop sets this first, shuts
	// the client down politely, and only then cancels.
	stopping bool
	// done is closed once the supervisor has exited and this entry is out of
	// the running set, which is what lets stopEntry mean "it has stopped"
	// rather than "it has been asked to".
	done chan struct{}

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
	if m.started {
		m.mu.Unlock()
		return fmt.Errorf("the plugin manager is already running")
	}
	m.started = true
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
	for _, entry := range m.running {
		running = append(running, entry)
	}
	m.mu.Unlock()

	// Each plugin is stopped deliberately before the shared context is
	// cancelled, so every one of them gets its shutdown handling run rather
	// than being killed where it stands. In parallel, because stopEntry waits
	// for its plugin and one slow shutdown should not hold up the others.
	var stopping sync.WaitGroup
	for _, entry := range running {
		stopping.Add(1)
		go func() {
			defer stopping.Done()
			m.stopEntry(entry)
		}()
	}
	stopping.Wait()

	if cancel != nil {
		cancel()
	}
	m.wg.Wait()

	m.mu.Lock()
	m.running = make(map[string]*runningPlugin)
	m.started = false
	m.ctx, m.cancel = nil, nil
	m.mu.Unlock()
}

// stopEntry shuts one plugin down deliberately and withdraws its sensors.
//
// The order is the whole point. Marking the entry tells its supervisor this is
// a stop rather than a crash -- without it the supervisor treats the
// disconnect as a failure and restarts the plugin that was just switched off.
// The polite shutdown then has to happen before the cancel, because cancelling
// kills the process outright.
//
// It returns only once the supervisor has actually gone. Returning earlier
// would make "disable then enable" a race: the entry would still be in the
// running set, the enable would take it for a live supervisor and do nothing,
// and then the old supervisor would retire -- leaving the plugin switched on
// and not running.
func (m *Manager) stopEntry(entry *runningPlugin) {
	m.mu.Lock()
	entry.stopping = true
	client := entry.client
	m.mu.Unlock()

	if client != nil {
		client.stop()
	}
	if entry.cancel != nil {
		entry.cancel()
	}
	<-entry.done

	// The supervisor withdraws these on its way out too; doing it here as well
	// keeps this correct if the entry never had a supervisor to begin with.
	m.removeEntriesFor(entry.descriptor.Name)
}

// stopRequested reports whether the supervisor should give up rather than
// restart: either this plugin was stopped on purpose, or the whole manager is
// shutting down.
func (m *Manager) stopRequested(ctx context.Context, entry *runningPlugin) bool {
	if ctx.Err() != nil {
		return true
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return entry.stopping
}

// startPlugin launches one plugin and supervises it.
//
// It is idempotent: a plugin that already has a supervisor is left alone, so
// enabling something twice cannot end up with two processes publishing over
// each other's entries.
func (m *Manager) startPlugin(ctx context.Context, descriptor Descriptor) {
	pluginCtx, cancel := context.WithCancel(ctx)
	entry := &runningPlugin{
		descriptor: descriptor,
		cancel:     cancel,
		done:       make(chan struct{}),
	}

	m.mu.Lock()
	// Two entries do not count as a live supervisor. A failed one has given up
	// and gone, kept only so the Plugins page can say so, and a stopping one is
	// on its way out -- replacing either is how a retry happens.
	if existing, ok := m.running[descriptor.Name]; ok && !existing.failed && !existing.stopping {
		m.mu.Unlock()
		cancel()
		return
	}
	m.running[descriptor.Name] = entry
	m.mu.Unlock()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		// Defers run last-in-first-out, so done closes after retire: anything
		// waiting on it sees a manager that has already forgotten the plugin.
		defer close(entry.done)
		defer m.retire(entry)

		m.supervise(pluginCtx, entry)
	}()
}

// retire takes a stopped plugin out of the running set.
//
// The supervisor owns this rather than whoever asked it to stop: it is the
// only thing that knows the plugin is really gone. An entry that failed stays
// in the map, because "gave up on this one" is something the user should see.
func (m *Manager) retire(entry *runningPlugin) {
	m.removeEntriesFor(entry.descriptor.Name)

	m.mu.Lock()
	defer m.mu.Unlock()

	if entry.failed {
		return
	}
	if current, ok := m.running[entry.descriptor.Name]; ok && current == entry {
		delete(m.running, entry.descriptor.Name)
	}
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

// restartBackoff paces retries.
//
// It applies to a plugin that exits immediately after connecting as much as to
// one that never connects: either way, retrying at once would spend the whole
// restart budget in milliseconds and tell nobody anything.
const restartBackoff = 2 * time.Second

// supervise keeps a plugin running, restarting it a bounded number of times.
//
// Its caller in startPlugin owns retiring the entry and signalling that it has
// gone, so every path out of here -- crash, give-up, deliberate stop -- ends
// the same way.
func (m *Manager) supervise(ctx context.Context, entry *runningPlugin) {
	for {
		if m.stopRequested(ctx, entry) {
			return
		}

		client := m.newClientFor(entry)

		m.mu.Lock()
		entry.client = client
		m.mu.Unlock()

		if err := client.start(ctx); err != nil {
			if m.stopRequested(ctx, entry) {
				return
			}
			m.log.Error("plugin failed to start",
				"plugin", entry.descriptor.Name, "error", err)
		} else {
			// Restore the user's saved settings before the plugin publishes
			// anything, so its first values already reflect them.
			m.restoreConfig(ctx, entry, client)

			// Returns only when the plugin disconnects.
			client.waitClosed()

			if m.stopRequested(ctx, entry) {
				return
			}

			m.log.Warn("plugin stopped; restarting", "plugin", entry.descriptor.Name)
			m.removeEntriesFor(entry.descriptor.Name)
		}

		// A plugin that has been up longer than the restart window has no
		// recent failures left in its history, so recordRestart gives it a full
		// budget again: a crash after hours of health is a new problem.
		if !m.recordRestart(entry) {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(restartBackoff):
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

		if entry, ok := m.running[descriptor.Name]; ok {
			// Failed is read outside the client check on purpose: a plugin that
			// was given up on is exactly the one whose client may be gone, and
			// that is the state most worth reporting.
			status.Failed = entry.failed

			if entry.client != nil {
				info := entry.client.metadata()
				status.Running = !entry.failed && !entry.stopping && info.ID != ""
				status.PluginID = info.ID
				status.Version = info.Version
				status.Actions = info.Actions
				status.Configurable = info.Configurable
			}
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

	if !enabled {
		m.stopPlugin(pluginName)
		return nil
	}

	for _, descriptor := range m.Discover() {
		if descriptor.Name != pluginName {
			continue
		}
		if ctx != nil {
			// startPlugin is idempotent, so enabling something already running
			// is a no-op rather than a second process.
			m.startPlugin(ctx, descriptor)
		}
		// A nil context means the manager has not been started yet; Start will
		// pick this plugin up from the enabled state just saved.
		return nil
	}
	return fmt.Errorf("plugin %s was not found", pluginName)
}

// stopPlugin shuts a plugin down and stops its supervisor restarting it.
func (m *Manager) stopPlugin(pluginName string) {
	m.mu.Lock()
	entry, ok := m.running[pluginName]
	m.mu.Unlock()

	if ok {
		m.stopEntry(entry)
	}
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
//
// The client is passed in rather than looked up by name for the same reason
// newClientFor closes over one: a lookup can return whatever client is
// registered now, which during a restart is not necessarily the one being
// started here.
func (m *Manager) restoreConfig(ctx context.Context, entry *runningPlugin, client *client) {
	if client == nil || m.opts.ConfigDir == "" || !client.metadata().Configurable {
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
