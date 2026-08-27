// Package web serves the built-in HTTP interface: the application API and
// live rendered panel frames, for viewing a panel from another device.
package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/api"
	"github.com/n-kisyov/wininfopanel/internal/config/model"
	"github.com/n-kisyov/wininfopanel/internal/logging"
	"github.com/n-kisyov/wininfopanel/internal/render/draw"
	"github.com/n-kisyov/wininfopanel/internal/render/font"
	"github.com/n-kisyov/wininfopanel/internal/sensor"
)

// Server is the built-in HTTP server.
type Server struct {
	log  *slog.Logger
	opts Options

	// handler is built during construction rather than in Run, so a malformed
	// route pattern -- which ServeMux reports by panicking -- surfaces when the
	// server is created and is caught by tests, instead of taking the process
	// down after it has already reported itself as running.
	handler http.Handler
	http    *http.Server
}

// Options configures the server.
type Options struct {
	API *api.Service

	// LayoutProvider and the render dependencies are needed to serve rendered
	// panel frames.
	Sensors sensor.Resolver
	Fonts   *font.Cache
	Images  draw.ImageSource
	History *draw.HistoryStore

	ListenIP string
	Port     int

	// RefreshRate is the frame interval in milliseconds the web UI polls at.
	RefreshRate int
}

// New builds a server. It does not listen until Run is called.
func New(opts Options) *Server {
	if opts.ListenIP == "" {
		opts.ListenIP = "127.0.0.1"
	}
	if opts.Port <= 0 || opts.Port > 65535 {
		opts.Port = 8080
	}
	if opts.RefreshRate < 16 {
		opts.RefreshRate = 66
	}
	if opts.Fonts == nil {
		opts.Fonts = font.NewCache()
	}

	s := &Server{log: logging.For("web"), opts: opts}

	mux := http.NewServeMux()
	s.routes(mux)
	s.handler = mux

	return s
}

// Handler exposes the routed handler, so the surface can be exercised without
// binding a port.
func (s *Server) Handler() http.Handler { return s.handler }

// Address returns the host:port the server listens on.
func (s *Server) Address() string {
	return net.JoinHostPort(s.opts.ListenIP, fmt.Sprint(s.opts.Port))
}

// Run serves until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	s.http = &http.Server{
		Addr:    s.Address(),
		Handler: s.handler,
		// A generous read timeout with no write timeout: rendering a frame for
		// a large panel can take longer than a default write deadline allows,
		// and cutting it off mid-response would show a torn image.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	listener, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.http.Addr, err)
	}

	s.log.Info("web server listening", "address", s.http.Addr)

	served := make(chan error, 1)
	go func() {
		err := s.http.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		served <- err
	}()

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.http.Shutdown(shutdownCtx); err != nil {
		s.log.Warn("web server did not shut down cleanly", "error", err)
	}
	return <-served
}

// LayoutProvider supplies profile layouts for rendering.
type LayoutProvider interface {
	Profile(profileID string) (*model.Profile, bool)
	WithLayout(profileID string, fn func(model.ItemList)) error
}
