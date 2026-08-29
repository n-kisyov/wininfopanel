package plugins

import (
	"context"
	"strings"
	"testing"
	"time"
)

// These cover the ways the supervisor used to mishandle a plugin being
// switched off. Each one failed before the fix it names.

// entryCount reports how many values a plugin currently publishes.
func entryCount(m *Manager, pluginName string) int {
	n := 0
	for _, e := range m.Entries() {
		if e.PluginName == pluginName {
			n++
		}
	}
	return n
}

// statusOf finds one plugin's status.
func statusOf(t *testing.T, m *Manager, pluginName string) Status {
	t.Helper()
	for _, s := range m.Statuses() {
		if s.Descriptor.Name == pluginName {
			return s
		}
	}
	t.Fatalf("plugin %q was not discovered", pluginName)
	return Status{}
}

// stayQuiet fails if the plugin publishes anything over the given window.
func stayQuiet(t *testing.T, m *Manager, pluginName string, window time.Duration) {
	t.Helper()

	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		if n := entryCount(m, pluginName); n > 0 {
			t.Fatalf("a stopped plugin republished %d entries: its supervisor "+
				"treated the stop as a crash and restarted the process", n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Disabling a plugin used to stop the client, whereupon the supervisor read
// the disconnect as a crash and started the process again -- while the entry
// was gone from the running map, so the UI reported it stopped throughout.
func TestDisabledPluginStaysDisabled(t *testing.T) {
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

	// SetEnabled withdraws the entries itself, so this needs no settling time.
	if n := entryCount(manager, "sample"); n != 0 {
		t.Errorf("SetEnabled returned with %d entries still published", n)
	}
	stayQuiet(t, manager, "sample", 5*time.Second)

	if status := statusOf(t, manager, "sample"); status.Running || status.Enabled {
		t.Errorf("a disabled plugin reports running=%v enabled=%v",
			status.Running, status.Enabled)
	}
}

// Re-enabling used to miss the live supervisor and spawn a second one, so two
// processes published over each other.
func TestTogglingAPluginLeavesOneSupervisor(t *testing.T) {
	root := buildTestPlugin(t, "sample", samplePlugin)
	manager := newTestManager(t, root)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer manager.Stop()

	waitFor(t, 30*time.Second, "the plugin to start", manager.Available)

	for i := 0; i < 3; i++ {
		if err := manager.SetEnabled("sample", false); err != nil {
			t.Fatalf("disable: %v", err)
		}
		if err := manager.SetEnabled("sample", true); err != nil {
			t.Fatalf("enable: %v", err)
		}
	}
	waitFor(t, 30*time.Second, "the plugin to publish again", manager.Available)

	// The sample plugin declares three entries. A second process supervising
	// the same plugin would not add more -- both publish the same paths -- but
	// it would leave a second supervisor in the wait group, so Stop is what
	// catches it: it would hang, or the entries would come back after it.
	if n := entryCount(manager, "sample"); n != 3 {
		t.Errorf("plugin publishes %d entries, want 3", n)
	}

	manager.Stop()
	if n := entryCount(manager, "sample"); n != 0 {
		t.Errorf("%d entries survived Stop", n)
	}
	stayQuiet(t, manager, "sample", 3*time.Second)
}

// Enabling an already-running plugin must be a no-op, not a second process.
func TestEnablingARunningPluginIsANoOp(t *testing.T) {
	root := buildTestPlugin(t, "sample", samplePlugin)
	manager := newTestManager(t, root)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer manager.Stop()

	waitFor(t, 30*time.Second, "the plugin to start", manager.Available)

	before := statusOf(t, manager, "sample")
	if err := manager.SetEnabled("sample", true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	after := statusOf(t, manager, "sample")
	if !after.Running {
		t.Error("the plugin stopped running after being enabled again")
	}
	if before.PluginID != after.PluginID {
		t.Errorf("plugin id changed from %q to %q; it was restarted rather than left alone",
			before.PluginID, after.PluginID)
	}
}

// A second Start would supervise every plugin twice.
func TestStartTwiceIsRefused(t *testing.T) {
	root := buildTestPlugin(t, "sample", samplePlugin)
	manager := newTestManager(t, root)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer manager.Stop()

	err := manager.Start(ctx)
	if err == nil {
		t.Fatal("a second Start was accepted")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("second Start said %q, which does not explain the refusal", err)
	}
}

// Stop must leave the manager restartable rather than wedged.
func TestManagerRestartsAfterStop(t *testing.T) {
	root := buildTestPlugin(t, "sample", samplePlugin)
	manager := newTestManager(t, root)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	waitFor(t, 30*time.Second, "the plugin to start", manager.Available)
	manager.Stop()

	if err := manager.Start(ctx); err != nil {
		t.Fatalf("restart manager: %v", err)
	}
	defer manager.Stop()
	waitFor(t, 30*time.Second, "the plugin to start again", manager.Available)
}

// The pipe was open to Everyone, which the comment above it denied.
func TestPipeSecurityDescriptorIsNotWorldAccessible(t *testing.T) {
	descriptor, err := pipeSecurityDescriptor()
	if err != nil {
		t.Fatalf("pipeSecurityDescriptor: %v", err)
	}

	// "WD" is the Everyone SID. An ACE granting it anything would put the
	// plugin channel -- and the credentials plugin config can carry -- within
	// reach of every account on the machine.
	if strings.Contains(descriptor, ";WD)") {
		t.Errorf("the plugin pipe grants access to Everyone: %s", descriptor)
	}
	if !strings.HasPrefix(descriptor, "D:P") {
		t.Errorf("the plugin pipe DACL is not protected: %s", descriptor)
	}
	// S-1-5-21-... for a domain or local account; the point is that some
	// specific principal is named rather than a well-known catch-all.
	if !strings.Contains(descriptor, ";S-1-5-21-") {
		t.Errorf("the plugin pipe names no specific user: %s", descriptor)
	}
}
