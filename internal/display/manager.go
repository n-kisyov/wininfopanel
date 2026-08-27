package display

import (
	"context"
	"log/slog"
	"sync"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/logging"
	"github.com/n-kisyov/wininfopanel/internal/render/draw"
	"github.com/n-kisyov/wininfopanel/internal/render/font"
	"github.com/n-kisyov/wininfopanel/internal/sensor"
	"github.com/n-kisyov/wininfopanel/internal/winapi"
)

// Manager keeps one overlay running per active profile.
//
// Sync is the only entry point that changes what is on screen: it reconciles
// the running overlays against the profiles that should be showing. Callers
// change a profile and call Sync rather than starting and stopping windows
// themselves, which keeps the two from drifting apart.
type Manager struct {
	log  *slog.Logger
	opts ManagerOptions

	mu      sync.Mutex
	running map[string]*overlayHandle
	ctx     context.Context
	cancel  context.CancelFunc
	started bool
}

// overlayHandle is one running overlay and the means to stop it.
type overlayHandle struct {
	overlay *Overlay
	cancel  context.CancelFunc
}

// ManagerOptions configures a Manager.
type ManagerOptions struct {
	Layouts LayoutProvider
	Sensors sensor.Resolver
	Fonts   *font.Cache
	Images  draw.ImageSource
	History *draw.HistoryStore

	FrameRate       int
	GraphUpdateRate int

	OnPositionChanged func(profileID string, x, y int)
	OnRightClick      func(profileID string)
}

// NewManager returns a manager that is not yet running.
func NewManager(opts ManagerOptions) *Manager {
	if opts.Fonts == nil {
		opts.Fonts = font.NewCache()
	}
	if opts.History == nil {
		opts.History = draw.NewHistoryStore(0, draw.DefaultHistoryCapacity)
	}

	return &Manager{
		log:     logging.For("display.manager"),
		opts:    opts,
		running: make(map[string]*overlayHandle),
	}
}

// Start prepares the manager to run overlays. Per-monitor DPI awareness is
// requested here because it has to be set before any window exists.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.started {
		return nil
	}

	if err := winapi.SetPerMonitorDPIAware(); err != nil {
		// Not fatal: the process runs at the system default instead, and
		// overlays scale less gracefully across mixed-DPI displays.
		m.log.Warn("could not enable per-monitor DPI awareness", "error", err)
	}

	m.ctx, m.cancel = context.WithCancel(ctx)
	m.started = true
	return nil
}

// Sync reconciles the running overlays against a set of profiles.
//
// Profiles marked active gain an overlay; every other running overlay is
// stopped.
func (m *Manager) Sync(profiles []*model.Profile) {
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		m.log.Warn("Sync called before Start; no overlays changed")
		return
	}
	ctx := m.ctx
	m.mu.Unlock()

	wanted := make(map[string]bool)
	for _, profile := range profiles {
		if profile != nil && profile.Active {
			wanted[profile.ID] = true
		}
	}

	m.mu.Lock()
	var toStop []*overlayHandle
	for id, handle := range m.running {
		if !wanted[id] {
			toStop = append(toStop, handle)
			delete(m.running, id)
		}
	}

	var toStart []string
	for id := range wanted {
		if _, already := m.running[id]; !already {
			toStart = append(toStart, id)
		}
	}
	m.mu.Unlock()

	for _, handle := range toStop {
		handle.cancel()
	}
	for _, id := range toStart {
		m.start(ctx, id)
	}
}

// start launches one overlay.
func (m *Manager) start(ctx context.Context, profileID string) {
	overlayCtx, cancel := context.WithCancel(ctx)

	overlay := New(Options{
		ProfileID:         profileID,
		Layouts:           m.opts.Layouts,
		Sensors:           m.opts.Sensors,
		Fonts:             m.opts.Fonts,
		Images:            m.opts.Images,
		History:           m.opts.History,
		FrameRate:         m.opts.FrameRate,
		GraphUpdateRate:   m.opts.GraphUpdateRate,
		OnPositionChanged: m.opts.OnPositionChanged,
		OnRightClick:      m.opts.OnRightClick,
	})

	m.mu.Lock()
	m.running[profileID] = &overlayHandle{overlay: overlay, cancel: cancel}
	m.mu.Unlock()

	go func() {
		if err := overlay.Run(overlayCtx); err != nil && overlayCtx.Err() == nil {
			m.log.Error("overlay stopped unexpectedly", "profile", profileID, "error", err)
		}

		// Drop the handle so a later Sync can restart this profile.
		m.mu.Lock()
		if current, ok := m.running[profileID]; ok && current.overlay == overlay {
			delete(m.running, profileID)
		}
		m.mu.Unlock()
		cancel()
	}()
}

// Overlay returns the running overlay for a profile.
func (m *Manager) Overlay(profileID string) (*Overlay, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	handle, ok := m.running[profileID]
	if !ok {
		return nil, false
	}
	return handle.overlay, true
}

// Active lists the profiles currently showing an overlay.
func (m *Manager) Active() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]string, 0, len(m.running))
	for id := range m.running {
		out = append(out, id)
	}
	return out
}

// Stop shuts every overlay down and waits for them to finish.
func (m *Manager) Stop() {
	m.mu.Lock()
	handles := make([]*overlayHandle, 0, len(m.running))
	for id, handle := range m.running {
		handles = append(handles, handle)
		delete(m.running, id)
	}
	cancel := m.cancel
	m.started = false
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	for _, handle := range handles {
		handle.cancel()
		<-handle.overlay.Stopped()
	}
}
