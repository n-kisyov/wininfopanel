package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
)

// send issues a request with a JSON body against the routed handler.
func send(t *testing.T, server *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	return rec
}

func TestCreateAndDeleteProfile(t *testing.T) {
	server, _ := newTestServer(t)

	rec := send(t, server, http.MethodPost, "/api/profiles",
		map[string]any{"name": "New Panel", "width": 480, "height": 320})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}

	var created model.Profile
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Name != "New Panel" || created.Width != 480 {
		t.Errorf("created profile = %+v", created)
	}

	if rec := send(t, server, http.MethodDelete, "/api/profiles/"+created.ID, nil); rec.Code != http.StatusNoContent {
		t.Errorf("delete = %d, want 204", rec.Code)
	}
	if rec := get(t, server, "/api/profiles/"+created.ID); rec.Code != http.StatusNotFound {
		t.Errorf("the deleted profile is still readable: %d", rec.Code)
	}
}

func TestCreateProfileRejectsBadDimensions(t *testing.T) {
	server, _ := newTestServer(t)

	rec := send(t, server, http.MethodPost, "/api/profiles",
		map[string]any{"name": "Bad", "width": 0, "height": 100})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a zero width returned %d, want 400", rec.Code)
	}
}

func TestPatchProfileLeavesOmittedFieldsAlone(t *testing.T) {
	// Every patch field is a pointer for exactly this reason: a request
	// changing only the name must not switch off every boolean on the profile.
	server, profile := newTestServer(t)

	before, err := server.opts.API.Profile(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !before.Active || !before.Drag {
		t.Fatalf("fixture profile is not in the expected state: %+v", before)
	}

	rec := send(t, server, http.MethodPatch, "/api/profiles/"+profile.ID,
		map[string]any{"name": "Renamed"})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	after, err := server.opts.API.Profile(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != "Renamed" {
		t.Errorf("Name = %q, want %q", after.Name, "Renamed")
	}
	if !after.Active || !after.Drag {
		t.Errorf("omitted booleans were reset: Active=%v Drag=%v", after.Active, after.Drag)
	}
	if after.Width != before.Width {
		t.Errorf("Width changed from %d to %d", before.Width, after.Width)
	}
}

func TestPatchProfileRejectsUnknownFields(t *testing.T) {
	// A typo in a request should be named, not silently ignored.
	server, profile := newTestServer(t)

	rec := send(t, server, http.MethodPatch, "/api/profiles/"+profile.ID,
		map[string]any{"nmae": "typo"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown field returned %d, want 400", rec.Code)
	}
}

func TestDuplicateProfileCopiesTheLayout(t *testing.T) {
	server, profile := newTestServer(t)

	rec := send(t, server, http.MethodPost, "/api/profiles/"+profile.ID+"/duplicate", nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("duplicate = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}

	var copied model.Profile
	if err := json.Unmarshal(rec.Body.Bytes(), &copied); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if copied.ID == profile.ID {
		t.Fatal("the duplicate reused the original's id")
	}

	original, err := server.opts.API.Layout(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := server.opts.API.Layout(copied.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(duplicate) != len(original) {
		t.Fatalf("the copy has %d items, the original %d", len(duplicate), len(original))
	}
	if duplicate[0].Base().ID == original[0].Base().ID {
		t.Error("copied items reused the original's ids; edits would affect both profiles")
	}
}

func TestAddUpdateAndDeleteItem(t *testing.T) {
	server, profile := newTestServer(t)

	shape := model.NewShapeItem("Box", model.ShapeHexagon)
	shape.X, shape.Y = 40, 50

	encoded, err := model.MarshalItem(shape)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/profiles/"+profile.ID+"/items",
		bytes.NewReader(encoded))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("add = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}

	items, err := server.opts.API.Layout(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("layout has %d items after adding one, want 2", len(items))
	}

	// Move it, and check the change lands.
	shape.X = 123
	encoded, err = model.MarshalItem(shape)
	if err != nil {
		t.Fatal(err)
	}

	req = httptest.NewRequest(http.MethodPut,
		"/api/profiles/"+profile.ID+"/items/"+shape.ID, bytes.NewReader(encoded))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	items, _ = server.opts.API.Layout(profile.ID)
	found := false
	for _, item := range items {
		if item.Base().ID == shape.ID {
			found = true
			if item.Base().X != 123 {
				t.Errorf("X = %d, want 123", item.Base().X)
			}
		}
	}
	if !found {
		t.Fatal("the updated item is missing from the layout")
	}

	rec = send(t, server, http.MethodDelete,
		"/api/profiles/"+profile.ID+"/items/"+shape.ID, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", rec.Code)
	}

	items, _ = server.opts.API.Layout(profile.ID)
	if len(items) != 1 {
		t.Errorf("layout has %d items after deleting one, want 1", len(items))
	}
}

func TestUpdateItemRejectsMismatchedID(t *testing.T) {
	// The path is authoritative; a body disagreeing with it is a mistake worth
	// naming rather than quietly resolving.
	server, profile := newTestServer(t)

	shape := model.NewShapeItem("Box", model.ShapeRectangle)
	encoded, err := model.MarshalItem(shape)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPut,
		"/api/profiles/"+profile.ID+"/items/some-other-id", bytes.NewReader(encoded))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("a mismatched id returned %d, want 400", rec.Code)
	}
}

func TestSetLayoutReplacesEverything(t *testing.T) {
	server, profile := newTestServer(t)

	replacement := model.ItemList{
		model.NewTextItem("One"),
		model.NewTextItem("Two"),
	}

	rec := send(t, server, http.MethodPut, "/api/profiles/"+profile.ID+"/layout", replacement)
	if rec.Code != http.StatusOK {
		t.Fatalf("set layout = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	items, err := server.opts.API.Layout(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("layout has %d items, want 2", len(items))
	}
	if items[0].Base().Name != "One" {
		t.Errorf("first item = %q, want %q", items[0].Base().Name, "One")
	}
}

func TestUndoRestoresThePreviousLayout(t *testing.T) {
	server, profile := newTestServer(t)

	before, err := server.opts.API.Layout(profile.ID)
	if err != nil {
		t.Fatal(err)
	}

	rec := send(t, server, http.MethodPut, "/api/profiles/"+profile.ID+"/layout",
		model.ItemList{model.NewTextItem("Replaced")})
	if rec.Code != http.StatusOK {
		t.Fatalf("set layout = %d", rec.Code)
	}

	rec = send(t, server, http.MethodPost, "/api/profiles/"+profile.ID+"/undo", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("undo = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	restored, err := server.opts.API.Layout(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != len(before) {
		t.Fatalf("undo restored %d items, want %d", len(restored), len(before))
	}
	if restored[0].Kind() != before[0].Kind() {
		t.Errorf("undo restored a %q where a %q was expected",
			restored[0].Kind(), before[0].Kind())
	}
}

func TestRedoReappliesAnUndoneChange(t *testing.T) {
	server, profile := newTestServer(t)

	if rec := send(t, server, http.MethodPut, "/api/profiles/"+profile.ID+"/layout",
		model.ItemList{model.NewTextItem("Replaced")}); rec.Code != http.StatusOK {
		t.Fatalf("set layout = %d", rec.Code)
	}
	if rec := send(t, server, http.MethodPost, "/api/profiles/"+profile.ID+"/undo", nil); rec.Code != http.StatusOK {
		t.Fatalf("undo = %d", rec.Code)
	}

	rec := send(t, server, http.MethodPost, "/api/profiles/"+profile.ID+"/redo", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("redo = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	items, err := server.opts.API.Layout(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Base().Name != "Replaced" {
		t.Errorf("redo did not reapply the change: %v", items)
	}
}

func TestReorderProfiles(t *testing.T) {
	server, first := newTestServer(t)

	second, err := server.opts.API.CreateProfile("Second", 100, 100)
	if err != nil {
		t.Fatal(err)
	}

	rec := send(t, server, http.MethodPost, "/api/profiles/order",
		[]string{second.ID, first.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("reorder = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	profiles := server.opts.API.Profiles()
	if profiles[0].ID != second.ID {
		t.Errorf("first profile is %q, want the reordered one", profiles[0].Name)
	}
}

func TestPatchSettings(t *testing.T) {
	server, _ := newTestServer(t)

	rec := send(t, server, http.MethodPatch, "/api/settings",
		map[string]any{"targetFrameRate": 30})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	if got := server.opts.API.Settings().TargetFrameRate; got != 30 {
		t.Errorf("TargetFrameRate = %d, want 30", got)
	}
}

func TestPatchSettingsNormalizesOutOfRangeValues(t *testing.T) {
	server, _ := newTestServer(t)

	rec := send(t, server, http.MethodPatch, "/api/settings",
		map[string]any{"webServerPort": 99999})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d, want 200", rec.Code)
	}

	if got := server.opts.API.Settings().WebServer.Port; got != 8080 {
		t.Errorf("Port = %d, want it normalized to the default", got)
	}
}

func TestWritesToUnknownProfilesAreRejected(t *testing.T) {
	server, _ := newTestServer(t)

	cases := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPatch, "/api/profiles/nope", map[string]any{"name": "x"}},
		{http.MethodDelete, "/api/profiles/nope", nil},
		{http.MethodPost, "/api/profiles/nope/duplicate", nil},
		{http.MethodPut, "/api/profiles/nope/layout", model.ItemList{}},
		{http.MethodPost, "/api/profiles/nope/undo", nil},
	}

	for _, c := range cases {
		if rec := send(t, server, c.method, c.path, c.body); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", c.method, c.path, rec.Code)
		}
	}
}

func TestPluginEndpointsReportWhenDisabled(t *testing.T) {
	// The fixture has no plugin manager attached, so the endpoints must say so
	// rather than panicking on a nil.
	server, _ := newTestServer(t)

	if rec := get(t, server, "/api/plugins"); rec.Code != http.StatusOK {
		t.Errorf("plugin list = %d, want 200 with an empty list", rec.Code)
	}

	rec := send(t, server, http.MethodPost, "/api/plugins/whatever/enabled",
		map[string]any{"enabled": true})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("enabling a plugin without a manager = %d, want 400", rec.Code)
	}
}
