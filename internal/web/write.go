package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
)

// The write surface mirrors the read one: the same vocabulary, the same
// addressing. Every handler funnels through internal/api rather than touching
// the store, so a change made here behaves identically to one made from the
// desktop shell.

// maxBodyBytes bounds a request body.
//
// A layout is the largest thing posted here and is comfortably under this;
// the limit exists so a malformed or hostile request cannot exhaust memory.
const maxBodyBytes = 8 << 20 // 8MB

// writeRoutes registers the mutating endpoints.
func (s *Server) writeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PATCH /api/settings", s.handleUpdateSettings)

	mux.HandleFunc("POST /api/profiles", s.handleCreateProfile)
	mux.HandleFunc("PATCH /api/profiles/{id}", s.handleUpdateProfile)
	mux.HandleFunc("DELETE /api/profiles/{id}", s.handleDeleteProfile)
	mux.HandleFunc("POST /api/profiles/{id}/duplicate", s.handleDuplicateProfile)
	mux.HandleFunc("POST /api/profiles/order", s.handleReorderProfiles)

	mux.HandleFunc("PUT /api/profiles/{id}/layout", s.handleSetLayout)
	mux.HandleFunc("POST /api/profiles/{id}/items", s.handleAddItem)
	mux.HandleFunc("PUT /api/profiles/{id}/items/{itemId}", s.handleUpdateItem)
	mux.HandleFunc("DELETE /api/profiles/{id}/items/{itemId}", s.handleDeleteItem)
	mux.HandleFunc("POST /api/profiles/{id}/undo", s.handleUndo)
	mux.HandleFunc("POST /api/profiles/{id}/redo", s.handleRedo)

	mux.HandleFunc("POST /api/plugins/{name}/enabled", s.handleSetPluginEnabled)
	mux.HandleFunc("POST /api/plugins/{name}/actions/{action}", s.handleInvokePluginAction)
	mux.HandleFunc("GET /api/plugins/{name}/config", s.handlePluginConfig)
	mux.HandleFunc("PATCH /api/plugins/{name}/config", s.handleSetPluginConfig)
}

// decodeBody reads a JSON request body into v.
func (s *Server) decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	defer r.Body.Close()

	decoder := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	// Rejecting unknown fields turns a typo in a request into a clear error
	// rather than a change that silently does nothing.
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(v); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("read request body: %w", err))
		return false
	}
	return true
}

// settingsPatch carries the settings a request wants changed.
//
// Every field is a pointer so an omitted one is distinguishable from one set
// to its zero value: without that, a request changing only the frame rate
// would silently switch off every boolean setting.
type settingsPatch struct {
	Theme          *model.Theme `json:"theme,omitempty"`
	AutoStart      *bool        `json:"autoStart,omitempty"`
	StartMinimized *bool        `json:"startMinimized,omitempty"`
	MinimizeToTray *bool        `json:"minimizeToTray,omitempty"`
	CloseToTray    *bool        `json:"closeToTray,omitempty"`

	ShowGridLines    *bool    `json:"showGridLines,omitempty"`
	GridLinesSpacing *float64 `json:"gridLinesSpacing,omitempty"`
	GridLinesColor   *string  `json:"gridLinesColor,omitempty"`

	HWiNFOEnabled  *bool `json:"hwinfoEnabled,omitempty"`
	HWiNFOInterval *int  `json:"hwinfoInterval,omitempty"`
	NativeEnabled  *bool `json:"nativeEnabled,omitempty"`
	NativeStorage  *bool `json:"nativeStorage,omitempty"`

	TargetFrameRate       *int `json:"targetFrameRate,omitempty"`
	TargetGraphUpdateRate *int `json:"targetGraphUpdateRate,omitempty"`

	WebServerEnabled     *bool   `json:"webServerEnabled,omitempty"`
	WebServerListenIP    *string `json:"webServerListenIp,omitempty"`
	WebServerPort        *int    `json:"webServerPort,omitempty"`
	WebServerRefreshRate *int    `json:"webServerRefreshRate,omitempty"`
}

// apply copies the set fields onto the settings.
func (p settingsPatch) apply(s *model.Settings) {
	assign(&s.Theme, p.Theme)
	assign(&s.AutoStart, p.AutoStart)
	assign(&s.StartMinimized, p.StartMinimized)
	assign(&s.MinimizeToTray, p.MinimizeToTray)
	assign(&s.CloseToTray, p.CloseToTray)

	assign(&s.ShowGridLines, p.ShowGridLines)
	assign(&s.GridLinesSpacing, p.GridLinesSpacing)
	assign(&s.GridLinesColor, p.GridLinesColor)

	assign(&s.HWiNFOEnabled, p.HWiNFOEnabled)
	assign(&s.HWiNFOInterval, p.HWiNFOInterval)
	assign(&s.NativeEnabled, p.NativeEnabled)
	assign(&s.NativeStorage, p.NativeStorage)

	assign(&s.TargetFrameRate, p.TargetFrameRate)
	assign(&s.TargetGraphUpdateRate, p.TargetGraphUpdateRate)

	assign(&s.WebServer.Enabled, p.WebServerEnabled)
	assign(&s.WebServer.ListenIP, p.WebServerListenIP)
	assign(&s.WebServer.Port, p.WebServerPort)
	assign(&s.WebServer.RefreshRate, p.WebServerRefreshRate)
}

// assign writes through a patch pointer when it is set.
func assign[T any](target *T, value *T) {
	if value != nil {
		*target = *value
	}
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var patch settingsPatch
	if !s.decodeBody(w, r, &patch) {
		return
	}

	updated, err := s.opts.API.UpdateSettings(patch.apply)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.writeJSON(w, http.StatusOK, updated)
}

// createProfileRequest describes a new profile.
type createProfileRequest struct {
	Name   string `json:"name"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

func (s *Server) handleCreateProfile(w http.ResponseWriter, r *http.Request) {
	var request createProfileRequest
	if !s.decodeBody(w, r, &request) {
		return
	}

	profile, err := s.opts.API.CreateProfile(request.Name, request.Width, request.Height)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, profile)
}

// profilePatch carries the profile fields a request wants changed.
type profilePatch struct {
	Name   *string `json:"name,omitempty"`
	Width  *int    `json:"width,omitempty"`
	Height *int    `json:"height,omitempty"`

	BackgroundColor *string  `json:"backgroundColor,omitempty"`
	Font            *string  `json:"font,omitempty"`
	FontSize        *int     `json:"fontSize,omitempty"`
	Color           *string  `json:"color,omitempty"`
	FontScale       *float64 `json:"fontScale,omitempty"`

	Active      *bool `json:"active,omitempty"`
	Topmost     *bool `json:"topmost,omitempty"`
	Drag        *bool `json:"drag,omitempty"`
	Resize      *bool `json:"resize,omitempty"`
	ShowFPS     *bool `json:"showFps,omitempty"`
	Accelerated *bool `json:"accelerated,omitempty"`

	WindowX *int `json:"windowX,omitempty"`
	WindowY *int `json:"windowY,omitempty"`

	TriggerProcessNames  *string `json:"triggerProcessNames,omitempty"`
	StrictWindowMatching *bool   `json:"strictWindowMatching,omitempty"`
}

func (p profilePatch) apply(profile *model.Profile) {
	assign(&profile.Name, p.Name)
	assign(&profile.Width, p.Width)
	assign(&profile.Height, p.Height)

	assign(&profile.BackgroundColor, p.BackgroundColor)
	assign(&profile.Font, p.Font)
	assign(&profile.FontSize, p.FontSize)
	assign(&profile.Color, p.Color)
	assign(&profile.FontScale, p.FontScale)

	assign(&profile.Active, p.Active)
	assign(&profile.Topmost, p.Topmost)
	assign(&profile.Drag, p.Drag)
	assign(&profile.Resize, p.Resize)
	assign(&profile.ShowFPS, p.ShowFPS)
	assign(&profile.Accelerated, p.Accelerated)

	assign(&profile.WindowX, p.WindowX)
	assign(&profile.WindowY, p.WindowY)

	assign(&profile.TriggerProcessNames, p.TriggerProcessNames)
	assign(&profile.StrictWindowMatching, p.StrictWindowMatching)
}

func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	var patch profilePatch
	if !s.decodeBody(w, r, &patch) {
		return
	}

	profile, err := s.opts.API.UpdateProfile(r.PathValue("id"), patch.apply)
	if err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return
	}
	s.writeJSON(w, http.StatusOK, profile)
}

func (s *Server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	if err := s.opts.API.DeleteProfile(r.PathValue("id")); err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDuplicateProfile(w http.ResponseWriter, r *http.Request) {
	profile, err := s.opts.API.DuplicateProfile(r.PathValue("id"))
	if err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, profile)
}

func (s *Server) handleReorderProfiles(w http.ResponseWriter, r *http.Request) {
	var ids []string
	if !s.decodeBody(w, r, &ids) {
		return
	}

	if err := s.opts.API.ReorderProfiles(ids); err != nil {
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.writeJSON(w, http.StatusOK, s.opts.API.Profiles())
}

func (s *Server) handleSetLayout(w http.ResponseWriter, r *http.Request) {
	var items model.ItemList
	if !s.decodeBody(w, r, &items) {
		return
	}

	if err := s.opts.API.SetLayout(r.PathValue("id"), items); err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return
	}
	s.writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleAddItem(w http.ResponseWriter, r *http.Request) {
	item, ok := s.decodeItem(w, r)
	if !ok {
		return
	}

	if err := s.opts.API.AddItem(r.PathValue("id"), item); err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleUpdateItem(w http.ResponseWriter, r *http.Request) {
	item, ok := s.decodeItem(w, r)
	if !ok {
		return
	}

	// The path is authoritative, so a body whose id disagrees is a mistake
	// worth naming rather than quietly resolving one way or the other.
	if wanted := r.PathValue("itemId"); item.Base().ID != wanted {
		s.writeError(w, http.StatusBadRequest,
			fmt.Errorf("item id in the body is %q but the path says %q",
				item.Base().ID, wanted))
		return
	}

	if err := s.opts.API.UpdateItem(r.PathValue("id"), item); err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return
	}
	s.writeJSON(w, http.StatusOK, item)
}

// decodeItem reads a display item, dispatching on its kind discriminator.
func (s *Server) decodeItem(w http.ResponseWriter, r *http.Request) (model.DisplayItem, bool) {
	defer r.Body.Close()

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("read request body: %w", err))
		return nil, false
	}

	item, err := model.UnmarshalItem(body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return nil, false
	}
	return item, true
}

func (s *Server) handleDeleteItem(w http.ResponseWriter, r *http.Request) {
	err := s.opts.API.DeleteItems(r.PathValue("id"), []string{r.PathValue("itemId")})
	if err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUndo(w http.ResponseWriter, r *http.Request) {
	items, err := s.opts.API.Undo(r.PathValue("id"))
	if err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return
	}
	s.writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleRedo(w http.ResponseWriter, r *http.Request) {
	items, err := s.opts.API.Redo(r.PathValue("id"))
	if err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return
	}
	s.writeJSON(w, http.StatusOK, items)
}

// enabledRequest toggles a plugin.
type enabledRequest struct {
	Enabled bool `json:"enabled"`
}

func (s *Server) handleSetPluginEnabled(w http.ResponseWriter, r *http.Request) {
	var request enabledRequest
	if !s.decodeBody(w, r, &request) {
		return
	}

	if err := s.opts.API.SetPluginEnabled(r.PathValue("name"), request.Enabled); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	s.writeJSON(w, http.StatusOK, s.opts.API.PluginStatuses())
}

func (s *Server) handleInvokePluginAction(w http.ResponseWriter, r *http.Request) {
	err := s.opts.API.InvokePluginAction(r.Context(), r.PathValue("name"), r.PathValue("action"))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePluginConfig(w http.ResponseWriter, r *http.Request) {
	properties, err := s.opts.API.PluginConfig(r.Context(), r.PathValue("name"))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	s.writeJSON(w, http.StatusOK, properties)
}

// configPatch sets one plugin setting.
type configPatch struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

func (s *Server) handleSetPluginConfig(w http.ResponseWriter, r *http.Request) {
	var patch configPatch
	if !s.decodeBody(w, r, &patch) {
		return
	}

	err := s.opts.API.SetPluginConfig(r.Context(), r.PathValue("name"), patch.Key, patch.Value)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	properties, err := s.opts.API.PluginConfig(r.Context(), r.PathValue("name"))
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.writeJSON(w, http.StatusOK, properties)
}
