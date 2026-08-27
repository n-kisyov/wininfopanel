package native

import (
	"io"
	"log/slog"
)

// testLogger discards output so tests do not spew collector diagnostics.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
