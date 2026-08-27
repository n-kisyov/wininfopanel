package web

import (
	_ "embed"
	"net/http"
	"strconv"
	"strings"
)

// The web UI is embedded in the binary rather than read from disk, so a single
// executable is the whole deployment and there is no asset path to get wrong.
//
//go:embed index.html
var indexHTML string

// handleIndex serves the web UI.
//
// The page's poll interval comes from settings, so it is injected rather than
// hard-coded: a panel viewed over a slow link should not be asked to refresh
// at the same rate as one on the same machine.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	page := strings.Replace(indexHTML,
		"window.__refreshMs || 1000",
		strconv.Itoa(s.opts.RefreshRate),
		1)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(page))
}
