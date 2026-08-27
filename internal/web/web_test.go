package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/n-kisyov/wininfopanel/internal/api"
	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/config/store"
	"github.com/n-kisyov/wininfopanel/internal/render/font"
	"github.com/n-kisyov/wininfopanel/internal/sensor"
)

// fixedSensors resolves every key to one reading.
type fixedSensors struct{ reading sensor.Reading }

func (f fixedSensors) Read(sensor.Key) (sensor.Reading, bool) { return f.reading, true }

// newTestServer builds a server over a temporary store holding one profile.
func newTestServer(t *testing.T) (*Server, *model.Profile) {
	t.Helper()

	configStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	service := api.New(api.Options{Store: configStore, Fonts: font.NewCache()})

	profile, err := service.CreateProfile("Test Panel", 320, 200)
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}

	item := model.NewSensorItem("CPU")
	item.Key = sensor.Key{Source: sensor.SourceNative, Path: "cpu/load"}
	item.X, item.Y, item.FontSize, item.Color = 10, 10, 20, "#FFFFFFFF"
	if err := service.SetLayout(profile.ID, model.ItemList{item}); err != nil {
		t.Fatalf("set layout: %v", err)
	}

	server := New(Options{
		API:     service,
		Sensors: fixedSensors{sensor.Reading{Now: 42, Unit: "%"}},
		Fonts:   font.NewCache(),
	})
	return server, profile
}

// get issues a request against the routed handler without binding a port.
func get(t *testing.T, server *Server, path string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	return rec
}

// TestEveryRouteResponds walks the whole surface. Registering routes during
// construction means a malformed pattern -- which ServeMux reports by
// panicking -- fails here rather than at runtime.
func TestEveryRouteResponds(t *testing.T) {
	server, profile := newTestServer(t)

	routes := []struct {
		path        string
		wantStatus  int
		contentType string
	}{
		{"/", http.StatusOK, "text/html; charset=utf-8"},
		{"/api/status", http.StatusOK, "application/json; charset=utf-8"},
		{"/api/settings", http.StatusOK, "application/json; charset=utf-8"},
		{"/api/profiles", http.StatusOK, "application/json; charset=utf-8"},
		{"/api/profiles/" + profile.ID, http.StatusOK, "application/json; charset=utf-8"},
		{"/api/profiles/" + profile.ID + "/layout", http.StatusOK, "application/json; charset=utf-8"},
		{"/api/screens", http.StatusOK, "application/json; charset=utf-8"},
		{"/api/fonts", http.StatusOK, "application/json; charset=utf-8"},
		{"/panel/" + profile.ID + "/image.png", http.StatusOK, "image/png"},
	}

	for _, route := range routes {
		rec := get(t, server, route.path)
		if rec.Code != route.wantStatus {
			t.Errorf("GET %s = %d, want %d (body: %s)",
				route.path, rec.Code, route.wantStatus, rec.Body.String())
			continue
		}
		if got := rec.Header().Get("Content-Type"); got != route.contentType {
			t.Errorf("GET %s content type = %q, want %q", route.path, got, route.contentType)
		}
	}
}

func TestPanelImageRendersAPNG(t *testing.T) {
	server, profile := newTestServer(t)

	rec := get(t, server, "/panel/"+profile.ID+"/image.png")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// A PNG always starts with this signature; anything else means the
	// response is not an image at all.
	body := rec.Body.Bytes()
	signature := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}
	if len(body) < len(signature) {
		t.Fatalf("response is %d bytes, too short to be a PNG", len(body))
	}
	for i, b := range signature {
		if body[i] != b {
			t.Fatalf("response is not a PNG: byte %d is %#x, want %#x", i, body[i], b)
		}
	}
}

func TestPanelImageHonorsScale(t *testing.T) {
	server, profile := newTestServer(t)

	normal := get(t, server, "/panel/"+profile.ID+"/image.png")
	doubled := get(t, server, "/panel/"+profile.ID+"/image.png?scale=2")

	if doubled.Code != http.StatusOK {
		t.Fatalf("scaled request = %d, want 200", doubled.Code)
	}
	if doubled.Body.Len() <= normal.Body.Len() {
		t.Errorf("a 2x render produced %d bytes, not more than the 1x render's %d",
			doubled.Body.Len(), normal.Body.Len())
	}
}

func TestPanelImageRejectsBadScale(t *testing.T) {
	server, profile := newTestServer(t)

	for _, scale := range []string{"0", "-1", "99", "abc"} {
		rec := get(t, server, "/panel/"+profile.ID+"/image.png?scale="+scale)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("scale=%s returned %d, want 400", scale, rec.Code)
		}
	}
}

func TestUnknownProfileIsNotFound(t *testing.T) {
	server, _ := newTestServer(t)

	for _, path := range []string{
		"/api/profiles/does-not-exist",
		"/api/profiles/does-not-exist/layout",
		"/panel/does-not-exist/image.png",
	} {
		if rec := get(t, server, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
}

func TestUnknownSensorSourceIsRejected(t *testing.T) {
	server, _ := newTestServer(t)

	if rec := get(t, server, "/api/sensors/nonsense"); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown source = %d, want 400", rec.Code)
	}
}

func TestProfilesEndpointReturnsTheProfile(t *testing.T) {
	server, profile := newTestServer(t)

	rec := get(t, server, "/api/profiles")
	var profiles []model.Profile
	if err := json.Unmarshal(rec.Body.Bytes(), &profiles); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(profiles) != 1 || profiles[0].ID != profile.ID {
		t.Errorf("got %d profiles, want the one created", len(profiles))
	}
}

func TestLayoutEndpointPreservesItemKinds(t *testing.T) {
	// The layout crosses the wire as polymorphic JSON, so the kind
	// discriminator has to survive.
	server, profile := newTestServer(t)

	rec := get(t, server, "/api/profiles/"+profile.ID+"/layout")
	var items model.ItemList
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].Kind() != model.KindSensor {
		t.Errorf("item kind = %q, want %q", items[0].Kind(), model.KindSensor)
	}
}

func TestPanelFramesAreNotCached(t *testing.T) {
	// A rendered frame is current only for an instant; a cached one would show
	// stale readings.
	server, profile := newTestServer(t)

	rec := get(t, server, "/panel/"+profile.ID+"/image.png")
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want %q", got, "no-store")
	}
}

func TestIndexRejectsUnknownPaths(t *testing.T) {
	// The root handler is a catch-all, so it must not answer for every path.
	server, _ := newTestServer(t)

	if rec := get(t, server, "/no/such/page"); rec.Code != http.StatusNotFound {
		t.Errorf("GET an unknown path = %d, want 404", rec.Code)
	}
}

func TestScaleIntKeepsNonZeroDimensionsVisible(t *testing.T) {
	// A one-pixel border scaled down must not round away to nothing.
	if got := scaleInt(1, 0.25); got != 1 {
		t.Errorf("scaleInt(1, 0.25) = %d, want 1", got)
	}
	if got := scaleInt(0, 2); got != 0 {
		t.Errorf("scaleInt(0, 2) = %d, want 0", got)
	}
	if got := scaleInt(10, 2); got != 20 {
		t.Errorf("scaleInt(10, 2) = %d, want 20", got)
	}
}

func TestScaleLayoutDoesNotMutateTheOriginal(t *testing.T) {
	item := model.NewShapeItem("Box", model.ShapeRectangle)
	item.X, item.Y, item.Width, item.Height = 10, 20, 30, 40
	items := model.ItemList{item}

	scaleLayout(items, 2)

	if item.X != 10 || item.Width != 30 {
		t.Errorf("scaling the response mutated the stored layout: %+v", item)
	}
}
