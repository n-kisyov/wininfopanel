package plugins

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/google/uuid"

	"github.com/n-kisyov/wininfopanel/internal/logging"
	"github.com/n-kisyov/wininfopanel/pkg/plugin"
)

// client owns one plugin process and the connection to it.
//
// A plugin runs out of process so a misbehaving one cannot take wininfopanel
// down with it. The client is what makes that isolation real: it starts the
// process, speaks the protocol, and tears everything down when the plugin
// stops answering.
type client struct {
	log        *slog.Logger
	descriptor Descriptor

	// callbacks deliver plugin output to the manager.
	onContainers func(containers []plugin.Container)
	onValues     func(updates []plugin.EntryUpdate)

	mu       sync.Mutex
	cmd      *exec.Cmd
	conn     net.Conn
	listener net.Listener
	info     plugin.HelloResponse
	closed   bool

	// pending correlates responses with the requests waiting on them.
	pending   sync.Map // request ID -> chan plugin.Message
	requestID atomic.Uint64

	writeMu sync.Mutex
	encoder *json.Encoder

	// closedCh is closed when the connection drops, so the supervisor can wait
	// for the plugin to go away without polling.
	closedCh  chan struct{}
	closeOnce sync.Once
}

// newClient prepares a client. Nothing runs until start is called.
func newClient(descriptor Descriptor,
	onContainers func([]plugin.Container),
	onValues func([]plugin.EntryUpdate)) *client {

	return &client{
		log:          logging.For("plugins").With("plugin", descriptor.Name),
		descriptor:   descriptor,
		onContainers: onContainers,
		onValues:     onValues,
		closedCh:     make(chan struct{}),
	}
}

// requestTimeout bounds how long the host waits for a plugin to answer.
//
// A plugin that stops responding must not hold up the rest of the
// application, so every request has a deadline rather than blocking forever.
const requestTimeout = 15 * time.Second

// start launches the plugin process and completes the handshake.
func (c *client) start(ctx context.Context) error {
	// The pipe is created before the process starts, so the plugin cannot lose
	// a race trying to connect to something that does not exist yet.
	pipeName := `\\.\pipe\wininfopanel-` + uuid.NewString()

	listener, err := winio.ListenPipe(pipeName, &winio.PipeConfig{
		// Only this user's processes may connect: a plugin channel can carry
		// configuration values such as API keys.
		SecurityDescriptor: "D:P(A;;GA;;;WD)",
		MessageMode:        false,
	})
	if err != nil {
		c.markClosed()
		return fmt.Errorf("create pipe: %w", err)
	}

	cmd := exec.CommandContext(ctx, c.descriptor.Executable)
	cmd.Dir = c.descriptor.Dir
	// The plugin inherits this process's environment so it can find its own
	// dependencies and honour proxy settings, with the pipe name added.
	cmd.Env = append(os.Environ(), plugin.PipeEnvVar+"="+pipeName)

	// A plugin's stderr is merged into wininfopanel's log, so its problems are
	// visible in one place rather than lost to a detached process.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		listener.Close()
		c.markClosed()
		return fmt.Errorf("capture plugin stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		listener.Close()
		c.markClosed()
		return fmt.Errorf("start %s: %w", c.descriptor.Executable, err)
	}

	go c.drainStderr(stderr)

	conn, err := acceptWithTimeout(ctx, listener, requestTimeout)
	if err != nil {
		listener.Close()
		cmd.Process.Kill()
		c.markClosed()
		return fmt.Errorf("plugin did not connect: %w", err)
	}

	c.mu.Lock()
	c.cmd, c.conn, c.listener = cmd, conn, listener
	c.encoder = json.NewEncoder(conn)
	c.mu.Unlock()

	go c.readLoop()

	info, err := c.hello(ctx)
	if err != nil {
		c.stop()
		return err
	}

	c.mu.Lock()
	c.info = info
	c.mu.Unlock()

	if err := c.load(ctx); err != nil {
		c.stop()
		return err
	}

	c.log.Info("plugin started",
		"id", info.ID, "version", info.Version, "interval", info.UpdateIntervalMS)
	return nil
}

// acceptWithTimeout waits for the plugin to connect.
func acceptWithTimeout(ctx context.Context, listener net.Listener, timeout time.Duration) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	accepted := make(chan result, 1)

	go func() {
		conn, err := listener.Accept()
		accepted <- result{conn: conn, err: err}
	}()

	select {
	case r := <-accepted:
		return r.conn, r.err
	case <-time.After(timeout):
		return nil, fmt.Errorf("timed out after %s", timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// hello performs the opening handshake.
func (c *client) hello(ctx context.Context) (plugin.HelloResponse, error) {
	payload, err := c.request(ctx, plugin.KindHello, nil)
	if err != nil {
		return plugin.HelloResponse{}, fmt.Errorf("handshake: %w", err)
	}

	var info plugin.HelloResponse
	if err := json.Unmarshal(payload, &info); err != nil {
		return plugin.HelloResponse{}, fmt.Errorf("decode handshake: %w", err)
	}

	// Refusing a version mismatch here turns it into one clear message,
	// rather than a decode failure somewhere later that says nothing useful.
	if info.ProtocolVersion != plugin.ProtocolVersion {
		return plugin.HelloResponse{}, fmt.Errorf(
			"plugin speaks protocol version %d, this build speaks %d",
			info.ProtocolVersion, plugin.ProtocolVersion)
	}
	if info.ID == "" {
		return plugin.HelloResponse{}, fmt.Errorf("plugin reported no id")
	}
	return info, nil
}

// load asks the plugin for its containers.
func (c *client) load(ctx context.Context) error {
	payload, err := c.request(ctx, plugin.KindLoad, nil)
	if err != nil {
		return fmt.Errorf("load: %w", err)
	}

	var response plugin.LoadResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return fmt.Errorf("decode containers: %w", err)
	}

	if c.onContainers != nil {
		c.onContainers(response.Containers)
	}
	return nil
}

// invoke runs one of the plugin's actions.
func (c *client) invoke(ctx context.Context, name string) error {
	_, err := c.request(ctx, plugin.KindAction, plugin.ActionRequest{Name: name})
	return err
}

// config reads the plugin's configuration properties.
func (c *client) config(ctx context.Context) ([]plugin.ConfigProperty, error) {
	payload, err := c.request(ctx, plugin.KindGetConfig, nil)
	if err != nil {
		return nil, err
	}

	var response plugin.ConfigResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return nil, fmt.Errorf("decode configuration: %w", err)
	}
	return response.Properties, nil
}

// setConfig applies one configuration value.
func (c *client) setConfig(ctx context.Context, key string, value any) error {
	_, err := c.request(ctx, plugin.KindSetConfig,
		plugin.SetConfigRequest{Key: key, Value: value})
	return err
}

// request sends a message and waits for its response.
func (c *client) request(ctx context.Context, kind plugin.Kind, payload any) (json.RawMessage, error) {
	id := fmt.Sprint(c.requestID.Add(1))

	message := plugin.Message{ID: id, Kind: kind}
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encode %s request: %w", kind, err)
		}
		message.Payload = encoded
	}

	replies := make(chan plugin.Message, 1)
	c.pending.Store(id, replies)
	defer c.pending.Delete(id)

	if err := c.write(message); err != nil {
		return nil, err
	}

	select {
	case reply := <-replies:
		if reply.Error != "" {
			return nil, fmt.Errorf("%s", reply.Error)
		}
		return reply.Payload, nil
	case <-time.After(requestTimeout):
		return nil, fmt.Errorf("%s timed out after %s", kind, requestTimeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *client) write(message plugin.Message) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if c.encoder == nil {
		return fmt.Errorf("plugin is not connected")
	}
	if err := c.encoder.Encode(message); err != nil {
		return fmt.Errorf("send %s: %w", message.Kind, err)
	}
	return nil
}

// readLoop dispatches everything the plugin sends.
func (c *client) readLoop() {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return
	}

	decoder := json.NewDecoder(bufio.NewReader(conn))
	for {
		var message plugin.Message
		if err := decoder.Decode(&message); err != nil {
			if err != io.EOF && !c.isClosed() {
				c.log.Warn("plugin connection failed", "error", err)
			}
			c.failPending(fmt.Errorf("plugin disconnected"))
			c.markClosed()
			return
		}

		switch message.Kind {
		case plugin.KindResponse:
			if waiter, ok := c.pending.Load(message.ID); ok {
				// Buffered, so a caller that has already timed out does not
				// block the read loop.
				waiter.(chan plugin.Message) <- message
			}

		case plugin.KindValues:
			var values plugin.ValuesNotification
			if err := json.Unmarshal(message.Payload, &values); err != nil {
				c.log.Warn("could not decode plugin values", "error", err)
				continue
			}
			if c.onValues != nil {
				c.onValues(values.Entries)
			}

		case plugin.KindLog:
			var entry plugin.LogNotification
			if err := json.Unmarshal(message.Payload, &entry); err == nil {
				c.logFromPlugin(entry)
			}
		}
	}
}

// failPending unblocks every waiting request when the connection dies, so a
// caller gets a clear error instead of waiting out its timeout.
func (c *client) failPending(err error) {
	c.pending.Range(func(key, value any) bool {
		select {
		case value.(chan plugin.Message) <- plugin.Message{
			ID: key.(string), Kind: plugin.KindResponse, Error: err.Error(),
		}:
		default:
		}
		return true
	})
}

// logFromPlugin merges a plugin's diagnostics into the application log.
func (c *client) logFromPlugin(entry plugin.LogNotification) {
	switch entry.Level {
	case "debug":
		c.log.Debug(entry.Message)
	case "warn":
		c.log.Warn(entry.Message)
	case "error":
		c.log.Error(entry.Message)
	default:
		c.log.Info(entry.Message)
	}
}

// drainStderr forwards a plugin's stderr into the application log.
func (c *client) drainStderr(stderr io.ReadCloser) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		if line := scanner.Text(); line != "" {
			c.log.Warn("plugin stderr", "message", line)
		}
	}
}

// markClosed signals that the connection is gone. It is idempotent: both the
// read loop and stop can reach it.
func (c *client) markClosed() {
	c.closeOnce.Do(func() { close(c.closedCh) })
}

// waitClosed blocks until the plugin disconnects.
func (c *client) waitClosed() { <-c.closedCh }

func (c *client) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// metadata returns what the plugin reported at handshake.
func (c *client) metadata() plugin.HelloResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info
}

// stop shuts the plugin down, asking politely before insisting.
func (c *client) stop() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	cmd, conn, listener := c.cmd, c.conn, c.listener
	c.mu.Unlock()

	defer c.markClosed()

	// Give the plugin a moment to run its own cleanup before the connection
	// disappears underneath it.
	if conn != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		c.request(shutdownCtx, plugin.KindShutdown, nil)
		cancel()
		conn.Close()
	}
	if listener != nil {
		listener.Close()
	}

	if cmd == nil || cmd.Process == nil {
		return
	}

	exited := make(chan struct{})
	go func() {
		cmd.Wait()
		close(exited)
	}()

	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		// It had its chance.
		c.log.Warn("plugin did not exit; terminating it")
		cmd.Process.Kill()
		<-exited
	}
}
