package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/display"
	"github.com/n-kisyov/wininfopanel/internal/render/draw"
	"github.com/n-kisyov/wininfopanel/internal/render/font"
	"github.com/n-kisyov/wininfopanel/internal/render/media"
	"github.com/n-kisyov/wininfopanel/internal/sensor/native"
	"github.com/n-kisyov/wininfopanel/internal/winapi"
)

// runOverlay shows a live desktop overlay, the same path the application uses.
func runOverlay(ctx context.Context, args []string) error {
	fs := newFlagSet("overlay")
	profilePath := fs.String("profile", "", "profile layout JSON to show; omitted shows the built-in demo")
	width := fs.Int("width", 800, "overlay width")
	height := fs.Int("height", 480, "overlay height")
	x := fs.Int("x", 100, "overlay screen position")
	y := fs.Int("y", 100, "overlay screen position")
	frameRate := fs.Int("fps", 15, "target frame rate")
	duration := fs.Duration("for", 0, "close automatically after this long; 0 runs until interrupted")
	topmost := fs.Bool("topmost", true, "keep the overlay above other windows")
	transparent := fs.Bool("transparent", false, "use a transparent background instead of the profile's")
	verbose := fs.Bool("v", false, "log engine activity to stderr")

	if err := fs.Parse(args); err != nil {
		return err
	}
	setupConsoleLogging(*verbose)

	profile, items, err := loadLayout(*profilePath, *width, *height)
	if err != nil {
		return err
	}
	profile.WindowX, profile.WindowY = *x, *y
	profile.Topmost = *topmost
	profile.Drag = true
	if *transparent {
		profile.BackgroundColor = "#00000000"
	}

	monitor := native.New(native.Options{Interval: time.Second, StorageEnabled: true})
	monitorCtx, stopMonitor := context.WithCancel(ctx)
	defer stopMonitor()
	go func() { monitor.Run(monitorCtx) }()

	if err := waitFor(ctx, 5*time.Second, monitor.Available); err != nil {
		return fmt.Errorf("native monitor produced no sensors: %w", err)
	}

	layouts := &staticLayouts{profile: profile, items: items}
	manager := display.NewManager(display.ManagerOptions{
		Layouts:         layouts,
		Sensors:         monitor,
		Fonts:           font.NewCache(),
		Images:          media.NewLoader(media.Options{}),
		History:         draw.NewHistoryStore(time.Duration(1000/(*frameRate))*time.Millisecond, draw.DefaultHistoryCapacity),
		FrameRate:       *frameRate,
		GraphUpdateRate: 500,
		OnPositionChanged: func(profileID string, nx, ny int) {
			layouts.setPosition(nx, ny)
			fmt.Printf("overlay moved to %d,%d\n", nx, ny)
		},
		OnRightClick: func(string) {
			fmt.Println("overlay right-clicked")
		},
	})

	if err := manager.Start(ctx); err != nil {
		return err
	}
	defer manager.Stop()

	manager.Sync([]*model.Profile{profile})

	screens, err := winapi.Screens()
	if err == nil {
		fmt.Fprintf(os.Stderr, "%d display(s) detected\n", len(screens))
		for _, s := range screens {
			fmt.Fprintf(os.Stderr, "  %s %dx%d at %d,%d dpi=%d primary=%v\n",
				s.DeviceName, s.Bounds.Width(), s.Bounds.Height(),
				s.Bounds.Left, s.Bounds.Top, s.DPI, s.Primary)
		}
	}
	fmt.Printf("overlay showing at %d,%d (%dx%d); drag it, right-click it, Ctrl+C to stop\n",
		*x, *y, profile.Width, profile.Height)

	if *duration > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(*duration):
		}
		return nil
	}

	<-ctx.Done()
	return nil
}

// staticLayouts serves one in-memory profile, standing in for the config store
// so the overlay can be exercised without touching the user's data.
type staticLayouts struct {
	mu      sync.RWMutex
	profile *model.Profile
	items   model.ItemList
}

func (s *staticLayouts) Profile(profileID string) (*model.Profile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.profile == nil || s.profile.ID != profileID {
		return nil, false
	}
	clone := *s.profile
	return &clone, true
}

func (s *staticLayouts) WithLayout(profileID string, fn func(model.ItemList)) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.profile == nil || s.profile.ID != profileID {
		return fmt.Errorf("profile %s not found", profileID)
	}
	fn(s.items)
	return nil
}

func (s *staticLayouts) setPosition(x, y int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profile.WindowX, s.profile.WindowY = x, y
}

// ensure the CLI's stand-in satisfies the interface the manager expects.
var _ display.LayoutProvider = (*staticLayouts)(nil)
