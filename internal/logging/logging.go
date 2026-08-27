// Package logging configures application-wide structured logging.
//
// Records fan out to three destinations: a dated rolling file on disk, an
// in-memory ring buffer that backs the Logs page, and (in debug builds) the
// console. This mirrors InfoPanel's Serilog setup, including its 7-day / 100MB
// log retention.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/n-kisyov/wininfopanel/internal/paths"
)

// sourceKey labels the subsystem a logger belongs to, the equivalent of
// Serilog's Log.ForContext<T>().
const sourceKey = "source"

const (
	logMaxAge   = 7 * 24 * time.Hour
	logMaxTotal = 100 << 20 // 100MB
	memoryDepth = 2000
)

// Options configures Setup.
type Options struct {
	// Level is the minimum level recorded. Defaults to slog.LevelInfo.
	Level slog.Level
	// Prefix names the log files, as "{prefix}-20060102.log". Defaults to
	// "wininfopanel". Plugin hosts pass their own so their logs stay separate.
	Prefix string
	// Console additionally writes human-readable records to stderr.
	Console bool
	// Dir overrides the log directory. Defaults to the standard logs path.
	Dir string
}

// Logging is a configured logging stack. Close it on shutdown to flush and
// release the log file.
type Logging struct {
	Memory *MemorySink

	writer *rollingWriter
}

// Setup installs a global slog logger and returns the stack backing it.
func Setup(opts Options) (*Logging, error) {
	if opts.Prefix == "" {
		opts.Prefix = "wininfopanel"
	}

	dir := opts.Dir
	if dir == "" {
		var err error
		if dir, err = paths.LogsDir(); err != nil {
			return nil, fmt.Errorf("resolve log directory: %w", err)
		}
	} else if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create log directory %s: %w", dir, err)
	}

	writer := newRollingWriter(dir, opts.Prefix, logMaxAge, logMaxTotal)
	memory := NewMemorySink(memoryDepth)

	handlers := []slog.Handler{
		slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: opts.Level}),
		&memoryHandler{sink: memory, level: opts.Level},
	}
	if opts.Console {
		handlers = append(handlers, slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: opts.Level}))
	}

	slog.SetDefault(slog.New(&fanoutHandler{handlers: handlers}))

	return &Logging{Memory: memory, writer: writer}, nil
}

// Close flushes and closes the log file.
func (l *Logging) Close() error {
	if l == nil || l.writer == nil {
		return nil
	}
	return l.writer.Close()
}

// For returns a logger tagged with the subsystem it belongs to, so records can
// be filtered by origin on the Logs page.
func For(source string) *slog.Logger {
	return slog.Default().With(sourceKey, source)
}

// fanoutHandler dispatches every record to each configured handler.
type fanoutHandler struct {
	handlers []slog.Handler
}

func (h *fanoutHandler) Enabled(ctx context.Context, l slog.Level) bool {
	for _, sub := range h.handlers {
		if sub.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (h *fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, sub := range h.handlers {
		if !sub.Enabled(ctx, r.Level) {
			continue
		}
		// Each handler gets its own clone: handlers may consume the record's
		// attribute iterator.
		if err := sub.Handle(ctx, r.Clone()); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

func (h *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	subs := make([]slog.Handler, len(h.handlers))
	for i, sub := range h.handlers {
		subs[i] = sub.WithAttrs(attrs)
	}
	return &fanoutHandler{handlers: subs}
}

func (h *fanoutHandler) WithGroup(name string) slog.Handler {
	subs := make([]slog.Handler, len(h.handlers))
	for i, sub := range h.handlers {
		subs[i] = sub.WithGroup(name)
	}
	return &fanoutHandler{handlers: subs}
}

// Discard returns a logger that drops everything, for tests.
func Discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
