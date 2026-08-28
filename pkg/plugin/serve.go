package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
)

// Serve runs a plugin until wininfopanel disconnects, then exits the process.
//
// It is the whole of a plugin's main:
//
//	func main() { plugin.Serve(&MyPlugin{}) }
//
// Diagnostics go to stderr, which wininfopanel captures into its own log.
// stdout is deliberately left alone so a plugin can print freely without
// corrupting the protocol -- the reason this uses a pipe rather than stdio.
func Serve(p Plugin) {
	if err := Run(context.Background(), p); err != nil {
		fmt.Fprintf(os.Stderr, "plugin: %v\n", err)
		os.Exit(1)
	}
}

// Run serves a plugin and returns when the connection closes.
//
// Prefer Serve unless the caller needs to control the context or handle the
// error itself.
func Run(ctx context.Context, p Plugin) error {
	pipeName := os.Getenv(PipeEnvVar)
	if pipeName == "" {
		return fmt.Errorf("%s is not set; this program is started by wininfopanel, "+
			"not run directly", PipeEnvVar)
	}

	if err := validateInfo(p.Info()); err != nil {
		return err
	}

	// A generous connect timeout: the host creates the pipe before starting
	// the process, but a cold start under load can still take a moment.
	timeout := 30 * time.Second
	conn, err := winio.DialPipe(pipeName, &timeout)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", pipeName, err)
	}
	defer conn.Close()

	// Ctrl+C and a host shutdown should both unwind the same way.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	s := &server{plugin: p, conn: conn, encoder: json.NewEncoder(conn)}
	return s.run(ctx)
}

// server is the plugin side of the connection.
type server struct {
	plugin Plugin
	conn   io.ReadWriteCloser

	// writeMu serializes writes: the update loop pushes values while the read
	// loop answers requests, and a JSON encoder is not safe for concurrent use.
	writeMu sync.Mutex
	encoder *json.Encoder

	containers []*ContainerBuilder
	loaded     bool
}

// run reads requests until the connection closes.
func (s *server) run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// The update loop starts only after Load has run, so it never publishes
	// values for entries the host has not been told about.
	updates := make(chan struct{}, 1)
	go s.updateLoop(ctx, updates)

	defer func() {
		if err := s.plugin.Close(); err != nil {
			s.logf("error", "close failed: %v", err)
		}
	}()

	decoder := json.NewDecoder(bufio.NewReader(s.conn))
	for {
		var message Message
		if err := decoder.Decode(&message); err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return nil // the host went away, which is an ordinary shutdown
			}
			return fmt.Errorf("read message: %w", err)
		}

		if message.Kind == KindShutdown {
			s.respond(message.ID, nil, nil)
			return nil
		}

		s.handle(ctx, message, updates)
	}
}

// handle dispatches one request.
func (s *server) handle(ctx context.Context, message Message, updates chan<- struct{}) {
	switch message.Kind {
	case KindHello:
		s.respond(message.ID, s.hello(), nil)

	case KindLoad:
		containers, err := s.plugin.Load(ctx)
		if err != nil {
			s.respond(message.ID, nil, err)
			return
		}
		s.containers = containers
		s.loaded = true

		snapshot := LoadResponse{Containers: make([]Container, 0, len(containers))}
		for _, container := range containers {
			snapshot.Containers = append(snapshot.Containers, container.snapshot())
		}
		s.respond(message.ID, snapshot, nil)

		// Wake the update loop now that entries exist to publish.
		select {
		case updates <- struct{}{}:
		default:
		}

	case KindUpdate:
		err := s.plugin.Update(ctx)
		s.respond(message.ID, nil, err)
		s.pushValues()

	case KindAction:
		s.respond(message.ID, nil, s.invoke(ctx, message.Payload))

	case KindGetConfig:
		configurable, ok := s.plugin.(Configurable)
		if !ok {
			s.respond(message.ID, ConfigResponse{}, nil)
			return
		}
		s.respond(message.ID, ConfigResponse{Properties: configurable.Config()}, nil)

	case KindSetConfig:
		s.respond(message.ID, nil, s.setConfig(message.Payload))

	default:
		s.respond(message.ID, nil, fmt.Errorf("unknown request %q", message.Kind))
	}
}

func (s *server) hello() HelloResponse {
	info := s.plugin.Info()

	interval := info.UpdateInterval
	if interval <= 0 {
		interval = DefaultUpdateInterval
	}
	if interval < MinUpdateInterval {
		interval = MinUpdateInterval
	}

	response := HelloResponse{
		ProtocolVersion:  ProtocolVersion,
		ID:               info.ID,
		Name:             info.Name,
		Description:      info.Description,
		Version:          info.Version,
		Author:           info.Author,
		Website:          info.Website,
		UpdateIntervalMS: int(interval / time.Millisecond),
	}

	if actionable, ok := s.plugin.(Actionable); ok {
		response.Actions = actionable.Actions()
	}
	if _, ok := s.plugin.(Configurable); ok {
		response.Configurable = true
	}
	return response
}

func (s *server) invoke(ctx context.Context, payload json.RawMessage) error {
	actionable, ok := s.plugin.(Actionable)
	if !ok {
		return fmt.Errorf("this plugin exposes no actions")
	}

	var request ActionRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return fmt.Errorf("decode action request: %w", err)
	}
	return actionable.Invoke(ctx, request.Name)
}

func (s *server) setConfig(payload json.RawMessage) error {
	configurable, ok := s.plugin.(Configurable)
	if !ok {
		return fmt.Errorf("this plugin has no configuration")
	}

	var request SetConfigRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return fmt.Errorf("decode config request: %w", err)
	}
	return configurable.Apply(request.Key, request.Value)
}

// updateLoop calls the plugin's Update on its requested interval.
//
// The host also drives updates explicitly, but a self-paced loop means a
// plugin's own cadence is honoured even when the host is busy.
func (s *server) updateLoop(ctx context.Context, start <-chan struct{}) {
	select {
	case <-ctx.Done():
		return
	case <-start:
	}

	interval := s.plugin.Info().UpdateInterval
	if interval <= 0 {
		interval = DefaultUpdateInterval
	}
	if interval < MinUpdateInterval {
		interval = MinUpdateInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.plugin.Update(ctx); err != nil {
				s.logf("error", "update failed: %v", err)
				continue
			}
			s.pushValues()
		}
	}
}

// pushValues sends whatever changed since the last push.
func (s *server) pushValues() {
	if !s.loaded {
		return
	}

	var updates []EntryUpdate
	for _, container := range s.containers {
		updates = append(updates, container.changed()...)
	}
	if len(updates) == 0 {
		return
	}

	s.notify(KindValues, ValuesNotification{Entries: updates})
}

// logf sends a diagnostic to the host, so a plugin's problems appear in
// wininfopanel's log rather than only in its own stderr.
func (s *server) logf(level, format string, args ...any) {
	s.notify(KindLog, LogNotification{Level: level, Message: fmt.Sprintf(format, args...)})
}

// respond answers a request.
func (s *server) respond(id string, payload any, err error) {
	message := Message{ID: id, Kind: KindResponse}

	if err != nil {
		message.Error = err.Error()
	} else if payload != nil {
		encoded, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			message.Error = marshalErr.Error()
		} else {
			message.Payload = encoded
		}
	}

	s.write(message)
}

// notify sends an unsolicited message.
func (s *server) notify(kind Kind, payload any) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	s.write(Message{Kind: kind, Payload: encoded})
}

func (s *server) write(message Message) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := s.encoder.Encode(message); err != nil {
		// The connection is gone; the read loop will notice and unwind. There
		// is nowhere useful left to report this.
		fmt.Fprintf(os.Stderr, "plugin: write failed: %v\n", err)
	}
}
