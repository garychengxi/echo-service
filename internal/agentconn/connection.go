// Package agentconn owns ACP WebSocket lifecycles and concurrent RPC routing.
package agentconn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"echo-service/internal/config"
	"github.com/gorilla/websocket"
)

var ErrClosed = errors.New("agent connection closed")
var ErrBusy = errors.New("agent write queue is full")

type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("agent RPC error %d", e.Code) }

type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}
type response struct {
	result json.RawMessage
	err    error
}
type Connection struct {
	socket   *websocket.Conn
	config   config.AgentConnection
	done     chan struct{}
	outgoing chan []byte
	events   chan Message
	mu       sync.Mutex
	pending  map[string]chan response
	sequence atomic.Uint64
	once     sync.Once
	wg       sync.WaitGroup
}

// ValidateEndpoint intentionally permits only private/loopback IP targets for
// this demo. No DNS rebinding, proxies, redirects, userinfo or query credentials.
func ValidateEndpoint(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u == nil {
		return errors.New("invalid container endpoint")
	}
	if (u.Scheme != "ws" && u.Scheme != "wss") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("endpoint must be credential-free ws(s)://ip:port/")
	}
	ip := net.ParseIP(u.Hostname())
	port, e := strconv.Atoi(u.Port())
	if e != nil || port < 1 || port > 65535 || ip == nil || (!ip.IsPrivate() && !ip.IsLoopback()) {
		return errors.New("endpoint requires private or loopback IP and valid port")
	}
	return nil
}
func Dial(ctx context.Context, endpoint string, c config.AgentConnection) (*Connection, error) {
	if err := ValidateEndpoint(endpoint); err != nil {
		return nil, err
	}
	dialer := websocket.Dialer{HandshakeTimeout: c.DialTimeout, Proxy: nil}
	dialCtx, cancel := context.WithTimeout(ctx, c.DialTimeout)
	defer cancel()
	socket, resp, err := dialer.DialContext(dialCtx, endpoint, http.Header{})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, errors.New("container connection failed")
	}
	conn := &Connection{socket: socket, config: c, done: make(chan struct{}), outgoing: make(chan []byte, c.WriteQueueSize), events: make(chan Message, 16), pending: make(map[string]chan response)}
	socket.SetReadLimit(c.MaxMessageBytes)
	_ = socket.SetReadDeadline(time.Now().Add(60 * time.Second))
	socket.SetPongHandler(func(string) error { return socket.SetReadDeadline(time.Now().Add(60 * time.Second)) })
	conn.wg.Add(2)
	go conn.readLoop()
	go conn.writeLoop()
	initCtx, stop := context.WithTimeout(ctx, c.RequestTimeout)
	defer stop()
	if _, err := conn.Call(initCtx, "initialize", map[string]any{"protocolVersion": 1, "clientInfo": map[string]string{"name": "echo-service", "version": "0.1.0"}, "clientCapabilities": map[string]any{}}); err != nil {
		conn.Close()
		return nil, errors.New("container initialization failed")
	}
	return conn, nil
}
func (c *Connection) Events() <-chan Message { return c.events }
func (c *Connection) Done() <-chan struct{}  { return c.done }
func (c *Connection) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	timeout := c.config.RequestTimeout
	if method == "session/prompt" {
		timeout = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	id := strconv.FormatUint(c.sequence.Add(1), 10)
	raw, e := json.Marshal(params)
	if e != nil {
		return nil, e
	}
	payload, e := json.Marshal(Message{JSONRPC: "2.0", ID: json.RawMessage(id), Method: method, Params: raw})
	if e != nil {
		return nil, e
	}
	wait := make(chan response, 1)
	c.mu.Lock()
	if len(c.pending) >= 256 {
		c.mu.Unlock()
		return nil, ErrBusy
	}
	c.pending[id] = wait
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	if e = c.enqueue(payload); e != nil {
		return nil, e
	}
	select {
	case r := <-wait:
		return r.result, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, ErrClosed
	}
}
func (c *Connection) Notify(ctx context.Context, method string, params any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, e := json.Marshal(params)
	if e != nil {
		return e
	}
	b, e := json.Marshal(Message{JSONRPC: "2.0", Method: method, Params: raw})
	if e != nil {
		return e
	}
	return c.enqueue(b)
}
func (c *Connection) Reply(id json.RawMessage, result any) error {
	raw, e := json.Marshal(result)
	if e != nil {
		return e
	}
	b, e := json.Marshal(Message{JSONRPC: "2.0", ID: id, Result: raw})
	if e != nil {
		return e
	}
	return c.enqueue(b)
}
func (c *Connection) enqueue(b []byte) error {
	if len(b) > 1<<20 {
		return ErrBusy
	}
	select {
	case <-c.done:
		return ErrClosed
	default:
	}
	select {
	case c.outgoing <- b:
		return nil
	case <-c.done:
		return ErrClosed
	default:
		return ErrBusy
	}
}
func (c *Connection) readLoop() {
	defer c.wg.Done()
	defer c.shutdown()
	defer close(c.events)
	for {
		kind, b, e := c.socket.ReadMessage()
		if e != nil {
			return
		}
		if kind != websocket.TextMessage {
			return
		}
		_ = c.socket.SetReadDeadline(time.Now().Add(60 * time.Second))
		var msg Message
		if json.Unmarshal(b, &msg) != nil || msg.JSONRPC != "2.0" {
			return
		}
		if msg.Method == "" && len(msg.ID) > 0 {
			key := string(msg.ID)
			c.mu.Lock()
			wait := c.pending[key]
			c.mu.Unlock()
			if wait != nil {
				r := response{result: msg.Result}
				if msg.Error != nil {
					r.err = msg.Error
				}
				select {
				case wait <- r:
				default:
				}
			}
			continue
		}
		if len(msg.ID) > 0 && msg.Method != "session/request_permission" {
			b, _ := json.Marshal(Message{JSONRPC: "2.0", ID: msg.ID, Error: &RPCError{Code: -32601, Message: "Unsupported server request"}})
			if c.enqueue(b) != nil {
				return
			}
			continue
		}
		// Unknown server requests fail closed. Permission requests are delivered to
		// the owning application actor and must be replied to on this connection.
		if len(b) > 1<<20 {
			return
		}
		select {
		case c.events <- msg:
		case <-c.done:
			return
		default:
			return
		}
	}
}
func (c *Connection) writeLoop() {
	defer c.wg.Done()
	defer c.shutdown()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case b := <-c.outgoing:
			_ = c.socket.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if c.socket.WriteMessage(websocket.TextMessage, b) != nil {
				return
			}
		case <-ticker.C:
			if c.socket.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
				return
			}
		}
	}
}
func (c *Connection) shutdown()    { c.once.Do(func() { close(c.done); _ = c.socket.Close() }) }
func (c *Connection) Close() error { c.shutdown(); c.wg.Wait(); return nil }

type Key struct{ UserID, AssignmentID, ContainerID string }
type managed struct {
	conn     *Connection
	endpoint string
	ready    chan struct{}
	cancel   context.CancelFunc
	err      error
}
type failureState struct {
	attempts int
	retryAt  time.Time
}
type Snapshot struct {
	State   string     `json:"state"`
	RetryAt *time.Time `json:"retryAt,omitempty"`
}
type Manager struct {
	mu          sync.Mutex
	connections map[Key]*managed
	config      config.AgentConnection
	closed      bool
	failures    map[Key]failureState
}

func NewManager(c config.AgentConnection) *Manager {
	return &Manager{connections: make(map[Key]*managed), failures: make(map[Key]failureState), config: c}
}

// Acquire does not retry side effects. A dead connection is replaced only on a
// later explicit call; sessions must be loaded again after a connection changes.
func (m *Manager) Acquire(ctx context.Context, key Key, endpoint string) (*Connection, error) {
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, ErrClosed
		}
		if old, ok := m.connections[key]; ok {
			if old.conn == nil {
				ready := old.ready
				m.mu.Unlock()
				select {
				case <-ready:
					return old.conn, old.err
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			select {
			case <-old.conn.Done():
				delete(m.connections, key)
			default:
				if old.endpoint != endpoint {
					m.mu.Unlock()
					return nil, errors.New("connection endpoint changed")
				}
				m.mu.Unlock()
				return old.conn, nil
			}
		}
		if failure, ok := m.failures[key]; ok {
			if time.Now().Before(failure.retryAt) {
				m.mu.Unlock()
				return nil, ErrBackoff
			}
		}
		if len(m.connections) >= 256 {
			m.mu.Unlock()
			return nil, ErrBusy
		}
		dialCtx, cancel := context.WithCancel(ctx)
		entry := &managed{endpoint: endpoint, ready: make(chan struct{}), cancel: cancel}
		m.connections[key] = entry
		m.mu.Unlock()
		conn, e := Dial(dialCtx, endpoint, m.config)
		cancel()
		m.mu.Lock()
		current := m.connections[key] == entry && !m.closed
		if current {
			if e == nil {
				entry.conn = conn
				delete(m.failures, key)
			} else {
				delete(m.connections, key)
				entry.err = e
				previous := m.failures[key]
				previous.attempts++
				delay := time.Second * time.Duration(1<<min(previous.attempts-1, 5))
				if delay > 30*time.Second {
					delay = 30 * time.Second
				}
				previous.retryAt = time.Now().Add(delay)
				m.failures[key] = previous
			}
		} else {
			entry.err = ErrClosed
		}
		close(entry.ready)
		m.mu.Unlock()
		if !current {
			if conn != nil {
				_ = conn.Close()
			}
			return nil, ErrClosed
		}
		return conn, e
	}
}

var ErrBackoff = errors.New("agent connection is in retry backoff")

func (m *Manager) Status(key Key) Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	if entry, ok := m.connections[key]; ok {
		if entry.conn == nil {
			return Snapshot{State: "connecting"}
		}
		select {
		case <-entry.conn.Done():
			return Snapshot{State: "disconnected"}
		default:
			return Snapshot{State: "connected"}
		}
	}
	if failure, ok := m.failures[key]; ok && time.Now().Before(failure.retryAt) {
		at := failure.retryAt
		return Snapshot{State: "backoff", RetryAt: &at}
	}
	return Snapshot{State: "disconnected"}
}
func (m *Manager) Invalidate(userID, containerID string) {
	m.mu.Lock()
	var closed []*Connection
	for k, v := range m.connections {
		if (userID == "" || k.UserID == userID) && (containerID == "" || k.ContainerID == containerID) {
			v.cancel()
			if v.conn != nil {
				closed = append(closed, v.conn)
			}
			delete(m.connections, k)
			delete(m.failures, k)
		}
	}
	for k := range m.failures {
		if (userID == "" || k.UserID == userID) && (containerID == "" || k.ContainerID == containerID) {
			delete(m.failures, k)
		}
	}
	m.mu.Unlock()
	for _, conn := range closed {
		_ = conn.Close()
	}
}
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.Invalidate("", "")
	return nil
}
