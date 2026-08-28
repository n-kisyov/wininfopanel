package plugins

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/sensor"
	"github.com/n-kisyov/wininfopanel/pkg/plugin"
)

// buildTestPlugin compiles the sample plugin into a discoverable folder and
// returns the directory holding it.
//
// Building a real executable and running it is the point: the protocol only
// matters end to end, and a mocked transport would not catch a handshake or
// process-lifecycle mistake.
func buildTestPlugin(t *testing.T, name, source string) string {
	t.Helper()

	root := t.TempDir()
	pluginDir := filepath.Join(root, name)
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}

	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "main.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	// The test plugin imports the SDK from this module, so it builds inside
	// this module's context rather than as a standalone one.
	moduleRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	output := filepath.Join(pluginDir, name+".exe")
	cmd := exec.Command("go", "build", "-o", output, filepath.Join(sourceDir, "main.go"))
	cmd.Dir = moduleRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build test plugin: %v\n%s", err, out)
	}

	manifest := "[PluginInfo]\nName=" + name + "\nAuthor=test\nVersion=9.9.9\n"
	if err := os.WriteFile(filepath.Join(pluginDir, manifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	return root
}

// samplePlugin publishes one sensor, one text value, a table, an action, and a
// setting -- enough to exercise the whole protocol.
const samplePlugin = `package main

import (
	"context"
	"fmt"
	"time"

	"github.com/n-kisyov/wininfopanel/pkg/plugin"
)

type sample struct {
	plugin.Base
	counter *plugin.Sensor
	label   *plugin.Text
	table   *plugin.TableValue
	step    int
	ticks   float64
}

func (s *sample) Info() plugin.Info {
	return plugin.Info{
		ID:             "sample",
		Name:           "Sample",
		Version:        "9.9.9",
		UpdateInterval: 100 * time.Millisecond,
	}
}

func (s *sample) Load(context.Context) ([]*plugin.ContainerBuilder, error) {
	c := plugin.NewContainer("main", "Main")
	s.counter = c.AddSensor("counter", "Counter", " units")
	s.label = c.AddText("label", "Label", "initial")
	s.table = c.AddTable("rows", "Rows", "0:100|1:60", []string{"Key", "Value"})
	if s.step == 0 {
		s.step = 1
	}
	return []*plugin.ContainerBuilder{c}, nil
}

func (s *sample) Update(context.Context) error {
	s.ticks += float64(s.step)
	s.counter.Set(s.ticks)
	s.label.Set(fmt.Sprintf("tick %d", int(s.ticks)))
	s.table.SetRows([][]string{{"ticks", fmt.Sprint(int(s.ticks))}})
	return nil
}

func (s *sample) Actions() []plugin.ActionInfo {
	return []plugin.ActionInfo{{Name: "reset", DisplayName: "Reset"}}
}

func (s *sample) Invoke(_ context.Context, name string) error {
	if name != "reset" {
		return fmt.Errorf("unknown action %q", name)
	}
	s.ticks = 0
	s.counter.Set(0)
	return nil
}

func (s *sample) Config() []plugin.ConfigProperty {
	return []plugin.ConfigProperty{{
		Key: "step", DisplayName: "Step", Type: plugin.ConfigInteger, Value: s.step,
	}}
}

func (s *sample) Apply(key string, value any) error {
	if key != "step" {
		return fmt.Errorf("unknown setting %q", key)
	}
	switch v := value.(type) {
	case float64:
		s.step = int(v)
	case int:
		s.step = v
	default:
		return fmt.Errorf("step must be a number, got %T", value)
	}
	return nil
}

func main() { plugin.Serve(&sample{step: 1}) }
`

// crashingPlugin exits as soon as it starts, to exercise the restart budget.
const crashingPlugin = `package main

import "os"

func main() { os.Exit(1) }
`

func newTestManager(t *testing.T, pluginRoot string) *Manager {
	t.Helper()

	return NewManager(ManagerOptions{
		ExternalDir:   pluginRoot,
		ConfigDir:     t.TempDir(),
		MaxRestarts:   2,
		RestartWindow: 5 * time.Second,
	})
}

func TestDiscoverFindsPluginsAndReadsManifests(t *testing.T) {
	root := buildTestPlugin(t, "sample", samplePlugin)

	found := Discover("", root)
	if len(found) != 1 {
		t.Fatalf("discovered %d plugins, want 1", len(found))
	}

	descriptor := found[0]
	if descriptor.Name != "sample" {
		t.Errorf("Name = %q, want %q", descriptor.Name, "sample")
	}
	if descriptor.Version != "9.9.9" {
		t.Errorf("Version = %q, want it read from the manifest", descriptor.Version)
	}
	if descriptor.Author != "test" {
		t.Errorf("Author = %q, want %q", descriptor.Author, "test")
	}
	if descriptor.Bundled {
		t.Error("a plugin from the external directory was marked bundled")
	}
}

func TestDiscoverIgnoresFoldersWithoutAnExecutable(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "not-a-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}

	if found := Discover("", root); len(found) != 0 {
		t.Errorf("discovered %d plugins in a folder with no executable, want 0", len(found))
	}
}

func TestDiscoverIgnoresMissingDirectories(t *testing.T) {
	// Most installations have no external plugins at all, so an absent
	// directory is normal rather than an error.
	if found := Discover(filepath.Join(t.TempDir(), "nope")); len(found) != 0 {
		t.Errorf("discovered %d plugins in a missing directory, want 0", len(found))
	}
}

// waitFor polls until condition holds or the deadline passes.
func waitFor(t *testing.T, timeout time.Duration, what string, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

func TestPluginPublishesValuesEndToEnd(t *testing.T) {
	root := buildTestPlugin(t, "sample", samplePlugin)
	manager := newTestManager(t, root)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer manager.Stop()

	waitFor(t, 30*time.Second, "the plugin to publish", manager.Available)

	entries := manager.Entries()
	if len(entries) != 3 {
		t.Fatalf("published %d entries, want 3: %+v", len(entries), entries)
	}

	byPath := make(map[string]Entry)
	for _, entry := range entries {
		byPath[entry.Path] = entry
	}

	counter, ok := byPath["sample/main/counter"]
	if !ok {
		t.Fatalf("no counter entry; got %v", pathsOf(entries))
	}
	if counter.Type != plugin.EntrySensor {
		t.Errorf("counter type = %q, want a sensor", counter.Type)
	}
	if counter.Unit != " units" {
		t.Errorf("counter unit = %q, want %q", counter.Unit, " units")
	}

	if _, ok := byPath["sample/main/label"]; !ok {
		t.Error("the text entry was not published")
	}
	if table, ok := byPath["sample/main/rows"]; !ok {
		t.Error("the table entry was not published")
	} else if table.Type != plugin.EntryTable {
		t.Errorf("table type = %q, want a table", table.Type)
	}
}

func TestPluginValuesResolveThroughTheSensorInterface(t *testing.T) {
	// A plugin value must reach display items by exactly the same path as an
	// HWiNFO or built-in reading.
	root := buildTestPlugin(t, "sample", samplePlugin)
	manager := newTestManager(t, root)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer manager.Stop()

	key := sensor.Key{Source: sensor.SourcePlugin, Path: "sample/main/counter"}
	waitFor(t, 30*time.Second, "the counter to advance", func() bool {
		reading, ok := manager.Read(key)
		return ok && reading.Now > 0
	})

	reading, ok := manager.Read(key)
	if !ok {
		t.Fatal("the counter did not resolve")
	}
	if reading.Unit != " units" {
		t.Errorf("Unit = %q, want %q", reading.Unit, " units")
	}
	if reading.Max < reading.Now {
		t.Errorf("Max %v is below the current value %v; statistics are not tracked",
			reading.Max, reading.Now)
	}
}

func TestPluginRejectsForeignSensorKeys(t *testing.T) {
	manager := newTestManager(t, t.TempDir())
	if _, ok := manager.Read(sensor.Key{Source: sensor.SourceNative, Path: "cpu/load"}); ok {
		t.Error("the plugin manager resolved a native sensor key")
	}
}

func TestPluginActionRuns(t *testing.T) {
	root := buildTestPlugin(t, "sample", samplePlugin)
	manager := newTestManager(t, root)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer manager.Stop()

	key := sensor.Key{Source: sensor.SourcePlugin, Path: "sample/main/counter"}
	waitFor(t, 30*time.Second, "the counter to advance", func() bool {
		reading, ok := manager.Read(key)
		return ok && reading.Now >= 3
	})

	if err := manager.Invoke(ctx, "sample", "reset"); err != nil {
		t.Fatalf("invoke: %v", err)
	}

	waitFor(t, 5*time.Second, "the counter to restart", func() bool {
		reading, ok := manager.Read(key)
		return ok && reading.Now < 3
	})
}

func TestUnknownActionIsReported(t *testing.T) {
	root := buildTestPlugin(t, "sample", samplePlugin)
	manager := newTestManager(t, root)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer manager.Stop()

	waitFor(t, 30*time.Second, "the plugin to start", manager.Available)

	if err := manager.Invoke(ctx, "sample", "nonsense"); err == nil {
		t.Error("invoking an unknown action succeeded")
	}
}

func TestPluginConfigurationRoundTrips(t *testing.T) {
	root := buildTestPlugin(t, "sample", samplePlugin)
	manager := newTestManager(t, root)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer manager.Stop()

	waitFor(t, 30*time.Second, "the plugin to start", manager.Available)

	properties, err := manager.Config(ctx, "sample")
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if len(properties) != 1 || properties[0].Key != "step" {
		t.Fatalf("config = %+v, want one 'step' property", properties)
	}

	if err := manager.SetConfig(ctx, "sample", "step", 5); err != nil {
		t.Fatalf("set config: %v", err)
	}

	properties, err = manager.Config(ctx, "sample")
	if err != nil {
		t.Fatalf("re-read config: %v", err)
	}
	if got := toFloat(properties[0].Value); got != 5 {
		t.Errorf("step = %v, want 5", properties[0].Value)
	}
}

func TestPluginConfigurationIsPersisted(t *testing.T) {
	// The host saves settings so a plugin never has to write a config file of
	// its own; the file must appear where the manager says it will.
	root := buildTestPlugin(t, "sample", samplePlugin)
	configDir := t.TempDir()

	manager := NewManager(ManagerOptions{ExternalDir: root, ConfigDir: configDir})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer manager.Stop()

	waitFor(t, 30*time.Second, "the plugin to start", manager.Available)

	if err := manager.SetConfig(ctx, "sample", "step", 7); err != nil {
		t.Fatalf("set config: %v", err)
	}

	if _, err := os.Stat(filepath.Join(configDir, "sample.config.json")); err != nil {
		t.Errorf("configuration was not persisted: %v", err)
	}
}

func TestSetEnabledStopsAPlugin(t *testing.T) {
	root := buildTestPlugin(t, "sample", samplePlugin)
	manager := newTestManager(t, root)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer manager.Stop()

	waitFor(t, 30*time.Second, "the plugin to start", manager.Available)

	if err := manager.SetEnabled("sample", false); err != nil {
		t.Fatalf("disable: %v", err)
	}

	// A stopped plugin's sensors must stop resolving rather than showing a
	// stale last value forever.
	waitFor(t, 10*time.Second, "the entries to be withdrawn", func() bool {
		return len(manager.Entries()) == 0
	})
}

func TestCrashingPluginGivesUpAfterTheRestartBudget(t *testing.T) {
	root := buildTestPlugin(t, "crasher", crashingPlugin)

	manager := NewManager(ManagerOptions{
		ExternalDir:   root,
		ConfigDir:     t.TempDir(),
		MaxRestarts:   2,
		RestartWindow: time.Minute,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer manager.Stop()

	// Each attempt waits for a connect timeout and a backoff, so this needs
	// room; what matters is that it stops rather than restarting forever.
	waitFor(t, 120*time.Second, "the plugin to be given up on", func() bool {
		for _, status := range manager.Statuses() {
			if status.Descriptor.Name == "crasher" && status.Failed {
				return true
			}
		}
		return false
	})
}

func TestStatusesDescribeDiscoveredPlugins(t *testing.T) {
	root := buildTestPlugin(t, "sample", samplePlugin)
	manager := newTestManager(t, root)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer manager.Stop()

	waitFor(t, 30*time.Second, "the plugin to start", manager.Available)

	statuses := manager.Statuses()
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want 1", len(statuses))
	}

	status := statuses[0]
	if !status.Running {
		t.Error("the plugin is publishing but was not reported as running")
	}
	if status.PluginID != "sample" {
		t.Errorf("PluginID = %q, want %q", status.PluginID, "sample")
	}
	if status.EntryCount != 3 {
		t.Errorf("EntryCount = %d, want 3", status.EntryCount)
	}
	if !status.Configurable {
		t.Error("a configurable plugin was not reported as such")
	}
	if len(status.Actions) != 1 {
		t.Errorf("Actions = %+v, want one", status.Actions)
	}
}

func pathsOf(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, entry := range entries {
		out[i] = entry.Path
	}
	return out
}

func toFloat(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	default:
		return -1
	}
}

func TestNeverToggledPluginReportsItselfEnabled(t *testing.T) {
	// Absence from the enabled map means enabled, and Statuses must agree with
	// the rule the supervisor actually uses -- otherwise a running plugin
	// shows as disabled in the UI.
	root := buildTestPlugin(t, "sample", samplePlugin)
	manager := newTestManager(t, root)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer manager.Stop()

	waitFor(t, 30*time.Second, "the plugin to start", manager.Available)

	statuses := manager.Statuses()
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want 1", len(statuses))
	}
	if statuses[0].Running && !statuses[0].Enabled {
		t.Error("a running plugin reported itself disabled")
	}
}
