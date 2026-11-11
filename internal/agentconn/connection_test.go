package agentconn

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"echo-service/internal/config"
	"github.com/gorilla/websocket"
)

func testConfig() config.AgentConnection {
	return config.AgentConnection{DialTimeout: time.Second, RequestTimeout: time.Second, WriteQueueSize: 64, MaxMessageBytes: 1 << 20}
}

func TestConcurrentRoutingTimeoutAndDisconnect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var requests []Message
		for {
			var m Message
			if conn.ReadJSON(&m) != nil {
				return
			}
			switch m.Method {
			case "initialize":
				_ = conn.WriteJSON(Message{JSONRPC: "2.0", ID: m.ID, Result: json.RawMessage(`{"protocolVersion":1}`)})
			case "reverse":
				requests = append(requests, m)
				if len(requests) == 8 {
					for i := 7; i >= 0; i-- {
						v := requests[i]
						_ = conn.WriteJSON(Message{JSONRPC: "2.0", ID: v.ID, Result: v.Params})
					}
					requests = nil
				}
			case "close":
				return
			case "bad":
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`not-json`))
			case "wait":
			}
		}
	}))
	defer server.Close()
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, err := Dial(context.Background(), endpoint, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			raw, e := conn.Call(context.Background(), "reverse", n)
			if e != nil || string(raw) != string(mustJSON(n)) {
				t.Errorf("request %d misrouted: %s %v", n, raw, e)
			}
		}(i)
	}
	wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	if _, err = conn.Call(ctx, "wait", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("timeout lost", err)
	}
	conn.mu.Lock()
	n := len(conn.pending)
	conn.mu.Unlock()
	if n != 0 {
		t.Fatal("pending call leaked")
	}
	if _, err = conn.Call(context.Background(), "close", nil); !errors.Is(err, ErrClosed) {
		t.Fatal("disconnect not propagated", err)
	}
	bad, err := Dial(context.Background(), endpoint, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	if _, err = bad.Call(context.Background(), "bad", nil); !errors.Is(err, ErrClosed) {
		t.Fatal("malformed frame not isolated", err)
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func TestManagerSharesConnectionAndInvalidates(t *testing.T) {
	var dials atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		dials.Add(1)
		for {
			var m Message
			if conn.ReadJSON(&m) != nil {
				return
			}
			_ = conn.WriteJSON(Message{JSONRPC: "2.0", ID: m.ID, Result: json.RawMessage(`{"protocolVersion":1}`)})
		}
	}))
	defer server.Close()
	m := NewManager(testConfig())
	defer m.Close()
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	key := Key{UserID: "user", ContainerID: "container", AssignmentID: "assignment"}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.Acquire(context.Background(), key, endpoint); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if dials.Load() != 1 {
		t.Fatal("duplicate concurrent dials", dials.Load())
	}
	old, err := m.Acquire(context.Background(), key, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	m.Invalidate("user", "")
	select {
	case <-old.Done():
	default:
		t.Fatal("connection not closed on revoke")
	}
	next, err := m.Acquire(context.Background(), key, endpoint)
	if err != nil || next == old {
		t.Fatal("fresh connection required", err)
	}
}

func TestManagerBackoffPreventsRedialStorm(t *testing.T) {
	m := NewManager(testConfig())
	defer m.Close()
	key := Key{UserID: "u", AssignmentID: "a", ContainerID: "c"}
	if _, err := m.Acquire(context.Background(), key, "ws://127.0.0.1:1"); err == nil {
		t.Fatal("unexpected dial success")
	}
	if status := m.Status(key); status.State != "backoff" || status.RetryAt == nil {
		t.Fatal("failure state missing", status)
	}
	for i := 0; i < 12; i++ {
		if _, err := m.Acquire(context.Background(), key, "ws://127.0.0.1:1"); !errors.Is(err, ErrBackoff) {
			t.Fatal("dial backoff not enforced", err)
		}
	}
}

func TestEndpointRestrictions(t *testing.T) {
	for _, endpoint := range []string{"ws://127.0.0.1:4600", "ws://10.1.2.3:4600/", "wss://[::1]:4600"} {
		if err := ValidateEndpoint(endpoint); err != nil {
			t.Fatal(endpoint, err)
		}
	}
	for _, endpoint := range []string{"ws://example.com:4600", "ws://8.8.8.8:80", "http://127.0.0.1:4600", "ws://user:pass@127.0.0.1:4600", "ws://127.0.0.1:4600?token=secret", "ws://127.0.0.1:4600/other", "ws://169.254.169.254:80"} {
		if ValidateEndpoint(endpoint) == nil {
			t.Fatal("unsafe endpoint accepted", endpoint)
		}
	}
}

// History replay can outpace database-backed event routing. A full bounded
// queue must apply backpressure rather than destroy the shared ACP connection.
func TestHistoryReplayWaitsForSlowConsumer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var msg Message
			if conn.ReadJSON(&msg) != nil {
				return
			}
			if msg.Method == "session/load" {
				for i := 0; i < 128; i++ {
					if conn.WriteJSON(Message{JSONRPC: "2.0", Method: "session/update", Params: mustJSON(map[string]int{"index": i})}) != nil {
						return
					}
				}
			}
			if conn.WriteJSON(Message{JSONRPC: "2.0", ID: msg.ID, Result: json.RawMessage(`{}`)}) != nil {
				return
			}
		}
	}))
	defer server.Close()
	conn, err := Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), testConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	result := make(chan error, 1)
	go func() { _, err := conn.Call(context.Background(), "session/load", nil); result <- err }()
	// Wait for replay to fill the queue before allowing the consumer to catch up.
	deadline := time.After(time.Second)
	for len(conn.events) < cap(conn.events) {
		select {
		case <-deadline:
			t.Fatal("replay did not fill queue")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	time.Sleep(20 * time.Millisecond)
	for i := 0; i < 128; i++ {
		select {
		case msg, ok := <-conn.Events():
			if !ok {
				t.Fatal("history burst closed connection")
			}
			var p struct {
				Index int `json:"index"`
			}
			if json.Unmarshal(msg.Params, &p) != nil || p.Index != i {
				t.Fatalf("event %d out of order: %s", i, msg.Params)
			}
		case <-time.After(time.Second):
			t.Fatal("history delivery stalled")
		}
	}
	if err := <-result; err != nil {
		t.Fatal("load failed", err)
	}
	if _, err := conn.Call(context.Background(), "session/set_mode", nil); err != nil {
		t.Fatal("connection not reusable", err)
	}
}
