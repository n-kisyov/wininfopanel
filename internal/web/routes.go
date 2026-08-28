package web

import (
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"strconv"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/plugins"
	"github.com/n-kisyov/wininfopanel/internal/render/draw"
	"github.com/n-kisyov/wininfopanel/internal/render/graphics"
	"github.com/n-kisyov/wininfopanel/internal/sensor"
)

// routes registers the HTTP surface.
//
// The API mirrors internal/api rather than inventing its own vocabulary, so a
// feature added to the desktop shell is reachable here without extra work.
func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/settings", s.handleSettings)
	mux.HandleFunc("GET /api/profiles", s.handleProfiles)
	mux.HandleFunc("GET /api/profiles/{id}", s.handleProfile)
	mux.HandleFunc("GET /api/profiles/{id}/layout", s.handleLayout)
	mux.HandleFunc("GET /api/sensors/{source}", s.handleSensors)
	mux.HandleFunc("GET /api/screens", s.handleScreens)
	mux.HandleFunc("GET /api/fonts", s.handleFonts)
	mux.HandleFunc("GET /api/plugins", s.handlePlugins)

	// Rendered frames, the reason to open this from another device. A wildcard
	// must span a whole path segment, so the extension is its own segment
	// rather than a suffix on {id}.
	mux.HandleFunc("GET /panel/{id}/image.png", s.handlePanelImage)

	s.writeRoutes(mux)

	mux.HandleFunc("GET /", s.handleIndex)
}

// writeJSON sends a value as JSON.
func (s *Server) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(value); err != nil {
		// The status line is already sent, so this can only be logged.
		s.log.Warn("could not write response", "error", err)
	}
}

// writeError sends a JSON error body.
func (s *Server) writeError(w http.ResponseWriter, status int, err error) {
	s.writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, s.opts.API.Status())
}

func (s *Server) handleSettings(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, s.opts.API.Settings())
}

func (s *Server) handleProfiles(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, s.opts.API.Profiles())
}

func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	profile, err := s.opts.API.Profile(r.PathValue("id"))
	if err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return
	}
	s.writeJSON(w, http.StatusOK, profile)
}

func (s *Server) handleLayout(w http.ResponseWriter, r *http.Request) {
	items, err := s.opts.API.Layout(r.PathValue("id"))
	if err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return
	}
	s.writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleSensors(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.opts.API.Sensors(sensor.Source(r.PathValue("source")))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	s.writeJSON(w, http.StatusOK, nodes)
}

func (s *Server) handleScreens(w http.ResponseWriter, _ *http.Request) {
	screens, err := s.opts.API.Screens()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.writeJSON(w, http.StatusOK, screens)
}

func (s *Server) handleFonts(w http.ResponseWriter, _ *http.Request) {
	families, err := s.opts.API.Fonts()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.writeJSON(w, http.StatusOK, families)
}

func (s *Server) handlePlugins(w http.ResponseWriter, _ *http.Request) {
	// An empty list rather than null, so the page can iterate without a guard.
	statuses := s.opts.API.PluginStatuses()
	if statuses == nil {
		statuses = []plugins.Status{}
	}
	s.writeJSON(w, http.StatusOK, statuses)
}

// handlePanelImage renders a profile and returns it as a PNG.
//
// Rendering per request rather than sharing the overlay's frame keeps the two
// independent: a panel can be viewed remotely whether or not its overlay is
// showing, and at whatever scale the viewer asks for.
func (s *Server) handlePanelImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	profile, err := s.opts.API.Profile(id)
	if err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return
	}

	scale := 1.0
	if raw := r.URL.Query().Get("scale"); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil || parsed <= 0 || parsed > 4 {
			s.writeError(w, http.StatusBadRequest,
				fmt.Errorf("scale must be a number between 0 and 4, got %q", raw))
			return
		}
		scale = parsed
	}

	items, err := s.opts.API.Layout(id)
	if err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return
	}

	width := int(float64(profile.Width) * scale)
	height := int(float64(profile.Height) * scale)

	fontScale := profile.FontScale
	if fontScale <= 0 {
		fontScale = 1
	}

	g := graphics.New(width, height, graphics.Options{
		Fonts:     s.opts.Fonts,
		FontScale: fontScale * scale,
	})

	// Scaling the canvas alone would leave items at their original
	// coordinates, so the layout is scaled with it.
	if scale != 1 {
		items = scaleLayout(items, scale)
	}

	draw.Render(g, items, draw.Frame{
		Profile: profile,
		Sensors: s.opts.Sensors,
		Now:     time.Now(),
		History: s.opts.History,
		Images:  s.opts.Images,
	})

	w.Header().Set("Content-Type", "image/png")
	// A rendered frame is current only for an instant; caching it would show
	// stale readings.
	w.Header().Set("Cache-Control", "no-store")

	if err := png.Encode(w, g.Image()); err != nil {
		s.log.Warn("could not encode panel image", "profile", id, "error", err)
	}
}

// scaleLayout returns a copy of a layout with every coordinate and dimension
// multiplied, so a panel can be rendered larger than its design size.
func scaleLayout(items model.ItemList, scale float64) model.ItemList {
	scaled := model.CloneAll(items)
	for _, item := range scaled {
		scaleItem(item, scale)
	}
	return scaled
}

func scaleItem(item model.DisplayItem, scale float64) {
	base := item.Base()
	base.X = int(float64(base.X) * scale)
	base.Y = int(float64(base.Y) * scale)

	switch it := item.(type) {
	case *model.GroupItem:
		for _, child := range it.Items {
			scaleItem(child, scale)
		}
	case *model.TextItem:
		scaleTextStyle(&it.TextStyle, scale)
	case *model.ClockItem:
		scaleTextStyle(&it.TextStyle, scale)
	case *model.CalendarItem:
		scaleTextStyle(&it.TextStyle, scale)
	case *model.SensorItem:
		scaleTextStyle(&it.TextStyle, scale)
	case *model.TableItem:
		scaleTextStyle(&it.TextStyle, scale)
	case *model.GraphItem:
		scaleChartStyle(&it.ChartStyle, scale)
		it.Thickness = scaleInt(it.Thickness, scale)
		it.Step = scaleInt(it.Step, scale)
	case *model.BarItem:
		scaleChartStyle(&it.ChartStyle, scale)
		it.CornerRadius = scaleInt(it.CornerRadius, scale)
	case *model.DonutItem:
		scaleChartStyle(&it.ChartStyle, scale)
		it.Thickness = scaleInt(it.Thickness, scale)
	case *model.GaugeItem:
		it.Width = scaleInt(it.Width, scale)
		it.Height = scaleInt(it.Height, scale)
	case *model.ShapeItem:
		it.Width = scaleInt(it.Width, scale)
		it.Height = scaleInt(it.Height, scale)
		it.CornerRadius = scaleInt(it.CornerRadius, scale)
		it.StrokeWidth = scaleInt(it.StrokeWidth, scale)
	case *model.ImageItem:
		it.Width = scaleInt(it.Width, scale)
		it.Height = scaleInt(it.Height, scale)
	}
}

func scaleTextStyle(style *model.TextStyle, scale float64) {
	// Font size is scaled through the surface's font scale instead, so that a
	// half-pixel size does not round away at small sizes.
	style.Width = scaleInt(style.Width, scale)
	style.Height = scaleInt(style.Height, scale)
	style.Glow.Radius = scaleInt(style.Glow.Radius, scale)
	style.MarqueeSpacing = scaleInt(style.MarqueeSpacing, scale)
}

func scaleChartStyle(style *model.ChartStyle, scale float64) {
	style.Width = scaleInt(style.Width, scale)
	style.Height = scaleInt(style.Height, scale)
}

// scaleInt scales a dimension, keeping a non-zero value non-zero so a
// one-pixel border does not vanish when scaled down.
func scaleInt(v int, scale float64) int {
	if v == 0 {
		return 0
	}
	scaled := int(float64(v)*scale + 0.5)
	if scaled == 0 {
		return 1
	}
	return scaled
}
