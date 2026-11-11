package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"echo-service/internal/agentconn"
	"echo-service/internal/auth"
	"echo-service/internal/client"
	"echo-service/internal/config"
	"echo-service/internal/events"
	"echo-service/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type fixture struct {
	s     *Service
	url   string
	admin string
}

func setup(t *testing.T) fixture {
	t.Helper()
	c, err := client.Open(config.Database{Path: filepath.Join(t.TempDir(), "test.db"), BusyTimeout: time.Second, MaxOpenConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Cache: config.Cache{TTL: time.Minute, CleanupInterval: time.Minute}, Auth: config.Auth{AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour}, AgentConnection: config.AgentConnection{DialTimeout: time.Second, RequestTimeout: 2 * time.Second, WriteQueueSize: 64, MaxMessageBytes: 8 << 20}}
	s, err := New(c.Database, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Auth.BootstrapAdmin(context.Background(), "admin", "admin-test-password"); err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	engine.Use(gin.Recovery())
	s.Register(engine)
	server := httptest.NewServer(engine)
	t.Cleanup(func() { _ = s.Close(); server.Close(); _ = c.Close() })
	f := fixture{s: s, url: server.URL}
	f.admin = f.login(t, "admin", "admin-test-password")
	return f
}

func (f fixture) request(t *testing.T, method, path, token string, body any, status int, key ...string) map[string]any {
	t.Helper()
	b, _ := json.Marshal(body)
	r, err := http.NewRequest(method, f.url+"/api/v1"+path, strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if len(key) > 0 {
		r.Header.Set("Idempotency-Key", key[0])
	}
	c := http.Client{Timeout: 5 * time.Second}
	resp, err := c.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if status != 204 {
		if err = json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
	}
	if resp.StatusCode != status {
		t.Fatalf("%s %s status %d want %d: %+v", method, path, resp.StatusCode, status, out)
	}
	return out
}

func data(m map[string]any) map[string]any { return m["data"].(map[string]any) }
func (f fixture) login(t *testing.T, name, password string) string {
	t.Helper()
	r := f.request(t, "POST", "/auth/login", "", gin.H{"username": name, "password": password}, 200)
	return data(r)["tokens"].(map[string]any)["accessToken"].(string)
}

func (f fixture) assign(t *testing.T, endpoint, root string) (string, string, string) {
	t.Helper()
	u := data(f.request(t, "POST", "/admin/users", f.admin, gin.H{"username": "alice", "password": "alice-test-password"}, 201))
	container := data(f.request(t, "POST", "/admin/containers", f.admin, gin.H{"name": "test", "endpoint": endpoint, "workspaceRoots": []string{root}}, 201))
	id := container["id"].(string)
	user := u["id"].(string)
	f.request(t, "PUT", "/admin/containers/"+id+"/users/"+user, f.admin, gin.H{"role": "owner"}, 200)
	return id, user, f.login(t, "alice", "alice-test-password")
}

func TestAuthIsolationAndCacheInvalidation(t *testing.T) {
	f := setup(t)
	f.request(t, "GET", "/me", "", nil, 401)
	id, user, token := f.assign(t, "ws://127.0.0.1:4600", "/workspace")
	f.request(t, "GET", "/admin/users", token, nil, 403)
	f.request(t, "GET", "/containers/"+id+"/status", f.admin, nil, 404)
	f.request(t, "PATCH", "/me", token, gin.H{"platformRole": "admin"}, 422)
	f.request(t, "GET", "/me", token, nil, 200)
	sessions := data(f.request(t, "GET", "/me/auth-sessions", token, nil, 200))["items"].([]any)
	if len(sessions) != 1 || sessions[0].(map[string]any)["current"] != true {
		t.Fatal("session enumeration incorrect", sessions)
	}
	f.request(t, "DELETE", "/me/auth-sessions/"+sessions[0].(map[string]any)["id"].(string), token, nil, 204)
	f.request(t, "GET", "/me", token, nil, 401)
	token = f.login(t, "alice", "alice-test-password")
	f.request(t, "PATCH", "/me", token, gin.H{"displayName": "Alice"}, 200)
	m := f.request(t, "GET", "/me", token, nil, 200)
	if data(m)["user"].(map[string]any)["displayName"] != "Alice" {
		t.Fatal("stale user cache")
	}
	b, _ := json.Marshal(m)
	if strings.Contains(string(b), "password") || strings.Contains(string(b), "PasswordHash") {
		t.Fatal("password hash in DTO")
	}
	list := f.request(t, "GET", "/me/containers", token, nil, 200)
	b, _ = json.Marshal(list)
	if strings.Contains(string(b), "endpoint") {
		t.Fatal("internal endpoint leaked")
	}
	f.request(t, "GET", "/admin/containers/"+id, f.admin, nil, 200)
	f.request(t, "PATCH", "/admin/containers/"+id, f.admin, gin.H{"status": "disabled"}, 200)
	f.request(t, "GET", "/containers/"+id+"/status", token, nil, 404)
	f.request(t, "PATCH", "/admin/users/"+user, f.admin, gin.H{"status": "disabled"}, 200)
	f.request(t, "GET", "/me", token, nil, 401)
	f.request(t, "POST", "/auth/login", "", gin.H{"username": "alice", "password": "alice-test-password"}, 401)
}

func TestEventSocketExplicitProxyOrigin(t *testing.T) {
	f := setup(t)
	f.s.allowedOrigins = "https://example.test:5443"
	id, _, token := f.assign(t, "ws://127.0.0.1:4600", "/workspace")
	for _, origin := range []string{"https://example.test:5443", "https://example.test", "https://evil.test:5443"} {
		ticket := data(f.request(t, "POST", "/ws-tickets", token, gin.H{"containerId": id}, 200))["ticket"].(string)
		socket, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(f.url, "http")+"/api/v1/events?ticket="+ticket, http.Header{"Origin": []string{origin}, "X-Forwarded-Proto": []string{"https"}})
		if origin == "https://example.test:5443" {
			if err != nil {
				t.Fatal(err)
			}
			readEvent(t, socket, "connection.ready")
			socket.Close()
		} else {
			if err == nil {
				socket.Close()
				t.Fatalf("accepted invalid origin %s", origin)
			}
			if response == nil || response.StatusCode != 403 {
				t.Fatalf("wrong denial for %s: %v", origin, response)
			}
			response.Body.Close()
		}
	}
}

func fakeAgent(t *testing.T, withSubagents ...bool) (string, *atomic.Int32) {
	t.Helper()
	count := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var pending json.RawMessage
		var session string
		reply := func(id json.RawMessage, result any) {
			raw, _ := json.Marshal(result)
			_ = conn.WriteJSON(agentconn.Message{JSONRPC: "2.0", ID: id, Result: raw})
		}
		for {
			var m agentconn.Message
			if conn.ReadJSON(&m) != nil {
				return
			}
			var params map[string]any
			_ = json.Unmarshal(m.Params, &params)
			switch m.Method {
			case "initialize":
				reply(m.ID, gin.H{"protocolVersion": 1})
			case "_project/create":
				reply(m.ID, gin.H{"project": gin.H{"id": uuid.NewString()}})
			case "session/new", "session/fork":
				reply(m.ID, gin.H{"sessionId": uuid.NewString(), "models": gin.H{"currentModelId": "test-model"}})
			case "session/load", "session/set_model", "session/set_mode", "_rename_session", "_delete_session", "_project/delete", "_project/update":
				reply(m.ID, gin.H{})
			case "_fs/readFile":
				if params["path"] == "/workspace/empty" {
					reply(m.ID, gin.H{"path": params["path"], "type": "directory", "entries": []any{}})
				} else {
					reply(m.ID, gin.H{"path": params["path"], "content": "hello", "size": 5, "truncated": false})
				}
			case "_get_common_settings", "_edit_common_settings":
				reply(m.ID, gin.H{"mode": "always-ask", "base_url": "hidden", "oauth2": gin.H{"client_secret": "hidden"}})
			case "session/prompt":
				count.Add(1)
				if strings.Contains(string(m.Params), "simulate-model-image-error") {
					_ = conn.WriteJSON(agentconn.Message{JSONRPC: "2.0", ID: m.ID, Error: &agentconn.RPCError{Code: -32602, Message: "Invalid params", Data: json.RawMessage(`"provider secret: hidden-provider-key is not a multimodal model"`)}})
					continue
				}
				if strings.Contains(string(m.Params), "simulate-model-error") {
					_ = conn.WriteJSON(agentconn.Message{JSONRPC: "2.0", ID: m.ID, Error: &agentconn.RPCError{Code: -32603, Message: "provider secret", Data: json.RawMessage(`{"http_status":503,"message":"Bearer hidden-provider-key"}`)}})
					continue
				}
				pending = m.ID
				session = params["sessionId"].(string)
				p, _ := json.Marshal(gin.H{"sessionId": session, "options": []any{gin.H{"optionId": "allow-once", "kind": "allow_once"}, gin.H{"optionId": "reject-once", "kind": "reject_once"}}, "toolCall": gin.H{"toolCallId": "tool-1", "title": "Read workspace"}})
				_ = conn.WriteJSON(agentconn.Message{JSONRPC: "2.0", ID: json.RawMessage(`"permission-1"`), Method: "session/request_permission", Params: p})
				if len(withSubagents) > 1 && withSubagents[1] {
					p, _ := json.Marshal(gin.H{"sessionId": session, "toolCallId": "ask-1", "questions": []any{gin.H{"id": "q", "question": "Which?", "options": []any{gin.H{"label": "A", "description": "First"}}}}})
					method := "_codeflow/ask_user_question"
					if len(withSubagents) > 2 && withSubagents[2] {
						method = "codeflow/ask_user_question"
					}
					_ = conn.WriteJSON(agentconn.Message{JSONRPC: "2.0", ID: json.RawMessage(`"ask-1"`), Method: method, Params: p})
				}
			case "session/cancel":
				if len(m.ID) > 0 {
					t.Error("cancel must be notification")
				}
				reply(pending, gin.H{"stopReason": "cancelled"})
				pending = nil
			case "":
				if len(withSubagents) > 1 && withSubagents[1] && string(m.ID) == `"permission-1"` {
					continue
				}
				if len(withSubagents) > 1 && withSubagents[1] && string(m.ID) == `"ask-1"` {
					var result map[string]any
					_ = json.Unmarshal(m.Result, &result)
					if m.Error != nil || result["outcome"] != "accepted" && result["outcome"] != "cancelled" {
						t.Errorf("invalid ask response: %+v", m)
					}
				}
				if len(pending) > 0 {
					if len(withSubagents) > 0 && withSubagents[0] {
						for _, event := range []struct {
							method, remote string
							update         gin.H
						}{
							{"_codeflow/session_notification", "foreign-parent", gin.H{"sessionUpdate": "subagent_spawned", "child_session_id": "foreign-child"}},
							{"session/update", "foreign-child", gin.H{"sessionUpdate": "agent_message_chunk", "content": gin.H{"type": "text", "text": "must not leak"}}},
							{"_codeflow/session_notification", session, gin.H{"sessionUpdate": "subagent_spawned", "subagent_id": "agent-1", "child_session_id": "child-1", "subagent_type": "sa-agent"}},
							{"session/update", "child-1", gin.H{"sessionUpdate": "agent_message_chunk", "content": gin.H{"type": "text", "text": "SA_CHILD_OK"}}},
							{"_codeflow/session_notification", session, gin.H{"sessionUpdate": "subagent_finished", "subagent_id": "agent-1", "child_session_id": "child-1", "status": "completed"}},
						} {
							p, _ := json.Marshal(gin.H{"sessionId": event.remote, "update": event.update})
							_ = conn.WriteJSON(agentconn.Message{JSONRPC: "2.0", Method: event.method, Params: p})
						}
					}
					p, _ := json.Marshal(gin.H{"sessionId": session, "update": gin.H{"sessionUpdate": "agent_message_chunk", "content": gin.H{"type": "text", "text": "done"}}})
					_ = conn.WriteJSON(agentconn.Message{JSONRPC: "2.0", Method: "session/update", Params: p})
					reply(pending, gin.H{"stopReason": "end_turn"})
					pending = nil
				}
			default:
				reply(m.ID, gin.H{"items": []any{}})
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http"), count
}

func TestFailedTurnPreservesSafeDiagnosticsInEventsAndPolling(t *testing.T) {
	endpoint, _ := fakeAgent(t)
	f := setup(t)
	id, _, token := f.assign(t, endpoint, "/workspace")
	ticket := data(f.request(t, "POST", "/ws-tickets", token, gin.H{"containerId": id}, 200))["ticket"].(string)
	socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(f.url, "http")+"/api/v1/events?ticket="+ticket, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	readEvent(t, socket, "connection.ready")
	session := data(f.request(t, "POST", "/containers/"+id+"/sessions", token, gin.H{"cwd": "/workspace"}, 201, "failure-session"))["id"].(string)
	turn := data(f.request(t, "POST", "/sessions/"+session+"/turns", token, gin.H{"clientRequestId": "failure-turn", "content": []any{gin.H{"type": "text", "text": "simulate-model-error"}}}, 202))["id"].(string)
	event := readEvent(t, socket, "turn.failed")
	want := "Model service returned HTTP 503 (RPC -32603)."
	if event["payload"].(map[string]any)["errorMessage"] != want {
		t.Fatalf("missing event diagnostics: %#v", event)
	}
	row := data(f.request(t, "GET", "/turns/"+turn, token, nil, 200))
	if row["state"] != "failed" || row["errorMessage"] != want {
		t.Fatalf("missing stored diagnostics: %#v", row)
	}
	encoded, _ := json.Marshal([]any{row, event})
	if strings.Contains(string(encoded), "hidden-provider-key") || strings.Contains(string(encoded), "provider secret") {
		t.Fatal("provider credentials leaked")
	}
	imageTurn := data(f.request(t, "POST", "/sessions/"+session+"/turns", token, gin.H{"clientRequestId": "image-failure-turn", "content": []any{gin.H{"type": "text", "text": "simulate-model-image-error"}}}, 202))["id"].(string)
	imageEvent := readEvent(t, socket, "turn.failed")
	imageRow := data(f.request(t, "GET", "/turns/"+imageTurn, token, nil, 200))
	if !strings.Contains(imageRow["errorMessage"].(string), "当前模型不支持图片") || imageEvent["payload"].(map[string]any)["errorMessage"] != imageRow["errorMessage"] {
		t.Fatalf("missing image failure explanation: %#v", imageRow)
	}
	encoded, _ = json.Marshal([]any{imageRow, imageEvent})
	if strings.Contains(string(encoded), "hidden-provider-key") || strings.Contains(string(encoded), "provider secret") {
		t.Fatal("image provider credentials leaked")
	}
}

func TestSubagentStreamRoutesOnlyAnnouncedChildrenOfOwnedParents(t *testing.T) {
	endpoint, _ := fakeAgent(t, true)
	f := setup(t)
	id, _, token := f.assign(t, endpoint, "/workspace")
	ticket := data(f.request(t, "POST", "/ws-tickets", token, gin.H{"containerId": id}, 200))["ticket"].(string)
	socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(f.url, "http")+"/api/v1/events?ticket="+ticket, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	readEvent(t, socket, "connection.ready")
	session := data(f.request(t, "POST", "/containers/"+id+"/sessions", token, gin.H{"cwd": "/workspace"}, 201, "subagent-session"))["id"].(string)
	turn := data(f.request(t, "POST", "/sessions/"+session+"/turns", token, gin.H{"clientRequestId": "subagent-turn", "content": []any{gin.H{"type": "text", "text": "test"}}}, 202))["id"].(string)
	approval := readEvent(t, socket, "approval.requested")["payload"].(map[string]any)["approvalId"].(string)
	f.request(t, "POST", "/turns/"+turn+"/approvals/"+approval+"/response", token, gin.H{"optionId": "allow-once"}, 204)
	for _, expected := range []string{"subagent_spawned", "agent_message_chunk", "subagent_finished"} {
		event := readEvent(t, socket, "session.update")
		payload := event["payload"].(map[string]any)
		update := payload["update"].(map[string]any)
		if event["sessionId"] != session || update["sessionUpdate"] != expected {
			t.Fatalf("incorrect routing: %#v", event)
		}
		if expected == "agent_message_chunk" && (payload["childSessionId"] != "child-1" || update["content"].(map[string]any)["text"] != "SA_CHILD_OK") {
			t.Fatalf("child output lost: %#v", event)
		}
	}
}

func readEvent(t *testing.T, conn *websocket.Conn, kind string) map[string]any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatal(err)
		}
		if msg["type"] == kind {
			return msg
		}
	}
}

func TestPromptApprovalCancelIdempotencyAndRevocation(t *testing.T) {
	endpoint, count := fakeAgent(t)
	f := setup(t)
	id, user, token := f.assign(t, endpoint, "/workspace")
	wsTicket := data(f.request(t, "POST", "/ws-tickets", token, gin.H{"containerId": id}, 200))["ticket"].(string)
	wsURL := "ws" + strings.TrimPrefix(f.url, "http") + "/api/v1/events?ticket=" + wsTicket
	socket, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	readEvent(t, socket, "connection.ready")
	if retry, resp, err := websocket.DefaultDialer.Dial(wsURL, nil); err == nil {
		retry.Close()
		t.Fatal("ticket reused")
	} else if resp == nil || resp.StatusCode != 401 {
		t.Fatal("unexpected ticket result", err)
	}
	project := data(f.request(t, "POST", "/containers/"+id+"/projects", token, gin.H{"name": "project", "roots": []any{gin.H{"path": "/workspace"}}}, 201, "project-1"))["id"].(string)
	f.request(t, "POST", "/containers/"+id+"/projects", token, gin.H{"name": "different", "roots": []any{gin.H{"path": "/workspace"}}}, 409, "project-1")
	row := data(f.request(t, "POST", "/containers/"+id+"/sessions", token, gin.H{"projectId": project}, 201, "session-1"))
	session := row["id"].(string)
	replayed := data(f.request(t, "POST", "/containers/"+id+"/sessions", token, gin.H{"projectId": project}, 200, "session-1"))
	if replayed["id"] != session {
		t.Fatal("session create duplicated")
	}
	f.request(t, "GET", "/sessions/"+session, f.admin, nil, 404)
	f.request(t, "PUT", "/sessions/"+session+"/mode", token, gin.H{"modeId": "always-approve"}, 200)
	f.request(t, "POST", "/sessions/"+session+"/load", token, gin.H{}, 200)
	if saved := data(f.request(t, "GET", "/sessions/"+session, token, nil, 200)); saved["modeId"] != "always-approve" {
		t.Fatalf("permission mode lost after load: %#v", saved)
	}
	for i := 0; i < 2; i++ {
		payload := gin.H{"clientRequestId": fmt.Sprintf("turn-%d", i), "content": []any{gin.H{"type": "text", "text": "hello"}}}
		turn := data(f.request(t, "POST", "/sessions/"+session+"/turns", token, payload, 202))
		turnID := turn["id"].(string)
		event := readEvent(t, socket, "approval.requested")
		if event["turnId"] != turnID || event["sessionId"] != session {
			t.Fatal("event ownership mapping lost", event)
		}
		if i == 0 {
			other := data(f.request(t, "POST", "/containers/"+id+"/sessions", token, gin.H{"cwd": "/workspace"}, 201, "session-2"))
			f.request(t, "POST", "/sessions/"+other["id"].(string)+"/load", token, gin.H{}, 200)
		}
		f.request(t, "POST", "/sessions/"+session+"/turns", token, payload, 200)
		if count.Load() != int32(i+1) {
			t.Fatal("duplicate prompt dispatched")
		}
		if i == 0 {
			approval := event["payload"].(map[string]any)["approvalId"].(string)
			path := "/turns/" + turnID + "/approvals/" + approval + "/response"
			f.request(t, "POST", path, token, gin.H{"optionId": "made-up"}, 422)
			f.request(t, "POST", path, token, gin.H{"optionId": "allow-once"}, 204)
			readEvent(t, socket, "turn.completed")
		} else {
			f.request(t, "POST", "/turns/"+turnID+"/cancel", token, gin.H{}, 202)
			readEvent(t, socket, "turn.cancelled")
		}
	}
	f.request(t, "PATCH", "/sessions/"+session, token, gin.H{"title": "renamed"}, 200)
	f.request(t, "POST", "/sessions/"+session+"/fork", token, gin.H{}, 201, "fork-1")
	f.request(t, "GET", "/containers/"+id+"/agent-settings", token, nil, 200)
	f.request(t, "DELETE", "/admin/containers/"+id+"/users/"+user, f.admin, nil, 204)
	_ = socket.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, _, err = socket.ReadMessage(); err != nil {
			break
		}
	}
	f.request(t, "GET", "/sessions/"+session, token, nil, 404)
}

// Opt-in smoke uses an actual Echo binary with isolated runtime state and no
// model/provider credentials. The production backend repository is read-only.
func TestRealEchoServer(t *testing.T) {
	binary := os.Getenv("ECHO_TEST_BINARY")
	if binary == "" {
		t.Skip("set ECHO_TEST_BINARY for real ACP integration")
	}
	root := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	cmd := exec.Command(binary, "server", "--listen", "ws://"+address)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "ECHO_HOME=" + filepath.Join(root, "echo"), "RUST_LOG=error"}
	cmd.Dir = root
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(8 * time.Second)
	for {
		resp, e := http.Get("http://" + address + "/readyz")
		if e == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("real Echo server not ready", e)
		}
		time.Sleep(30 * time.Millisecond)
	}
	f := setup(t)
	id, _, token := f.assign(t, "ws://"+address, root)
	for _, resource := range []string{"models", "agents", "skills", "plugins", "files", "agent-settings"} {
		f.request(t, "GET", "/containers/"+id+"/"+resource, token, nil, 200)
	}
	project := data(f.request(t, "POST", "/containers/"+id+"/projects", token, gin.H{"name": "smoke", "roots": []any{gin.H{"path": root}}}, 201, "real-project"))["id"].(string)
	row := data(f.request(t, "POST", "/containers/"+id+"/sessions", token, gin.H{"projectId": project}, 201, "real-session"))
	session := row["id"].(string)
	f.request(t, "POST", "/sessions/"+session+"/load", token, gin.H{}, 200)
	f.request(t, "PATCH", "/sessions/"+session, token, gin.H{"title": "smoke"}, 200)
	f.request(t, "PATCH", "/projects/"+project, token, gin.H{"name": "updated"}, 200)
	f.request(t, "DELETE", "/sessions/"+session, token, nil, 204)
	f.request(t, "DELETE", "/projects/"+project, token, nil, 204)
}

func TestContainerDesktopMetadata(t *testing.T) {
	f := setup(t)
	id, _, token := f.assign(t, "ws://127.0.0.1:4600", "/workspace")
	f.request(t, "PATCH", "/admin/containers/"+id, f.admin, gin.H{"webUrl": "javascript:alert(1)"}, 422)
	f.request(t, "PATCH", "/admin/containers/"+id, f.admin, gin.H{"webUrl": "https://user:secret@127.0.0.1:5001/"}, 422)
	f.request(t, "PATCH", "/admin/containers/"+id, f.admin, gin.H{"webUrl": "https://127.0.0.1:5001/"}, 200)
	items := data(f.request(t, "GET", "/me/containers", token, nil, 200))["items"].([]any)
	item := items[0].(map[string]any)
	if item["executionType"] != "remote" || item["webUrl"] != "https://127.0.0.1:5001/" {
		t.Fatal("missing remote desktop metadata", item)
	}
	if _, ok := item["endpoint"]; ok {
		t.Fatal("agent endpoint exposed")
	}
	f.request(t, "PATCH", "/admin/containers/"+id, f.admin, gin.H{"webUrl": ""}, 200)
	items = data(f.request(t, "GET", "/me/containers", token, nil, 200))["items"].([]any)
	if _, ok := items[0].(map[string]any)["webUrl"]; ok {
		t.Fatal("desktop URL was not cleared")
	}
}

func TestCreateSessionRequiresExplicitWorkspace(t *testing.T) {
	f := setup(t)
	id, _, token := f.assign(t, "ws://127.0.0.1:4600", "/config")
	f.request(t, "POST", "/containers/"+id+"/sessions", token, gin.H{}, 422)
	var count int64
	if err := f.s.Repo.DB.Model(&storage.Session{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("session created without an explicit workspace", err, count)
	}
}

func TestRefreshAndDeviceLogoutKeepOtherDeviceStream(t *testing.T) {
	endpoint, _ := fakeAgent(t)
	f := setup(t)
	id, _, first := f.assign(t, endpoint, "/workspace")
	second := data(f.request(t, "POST", "/auth/login", "", gin.H{"username": "alice", "password": "alice-test-password", "deviceName": "second"}, 200))["tokens"].(map[string]any)
	ticket := data(f.request(t, "POST", "/ws-tickets", first, gin.H{"containerId": id}, 200))["ticket"].(string)
	socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(f.url, "http")+"/api/v1/events?ticket="+ticket, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	readEvent(t, socket, "connection.ready")
	f.request(t, "POST", "/containers/"+id+"/sessions", first, gin.H{"cwd": "/workspace"}, 201, "multi-login")
	assertConnected := func() {
		f.request(t, "GET", "/me", first, nil, 200)
		f.s.runtimeMu.Lock()
		attached := len(f.s.attached)
		f.s.runtimeMu.Unlock()
		if attached != 1 {
			t.Fatal("shared upstream disconnected", attached)
		}
		f.s.hub.Publish(events.Key{UserID: mustPrincipal(t, f.s, first).UserID, ContainerID: id}, events.Event{Type: "test.alive"})
		readEvent(t, socket, "test.alive")
	}
	refreshed := data(f.request(t, "POST", "/auth/refresh", "", gin.H{"refreshToken": second["refreshToken"]}, 200))
	assertConnected()
	f.request(t, "POST", "/auth/logout", refreshed["accessToken"].(string), gin.H{}, 204)
	assertConnected()
	// Replaying a refresh revokes only that login, never another device's stream.
	f.request(t, "POST", "/auth/refresh", "", gin.H{"refreshToken": second["refreshToken"]}, 401)
	assertConnected()
}

func mustPrincipal(t *testing.T, s *Service, token string) auth.Principal {
	t.Helper()
	p, err := s.Auth.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUnifiedInteractionsPermissionAskRecoveryAndOwnership(t *testing.T) {
	endpoint, _ := fakeAgent(t, false, true, true)
	f := setup(t)
	id, _, token := f.assign(t, endpoint, "/workspace")
	ticket := data(f.request(t, "POST", "/ws-tickets", token, gin.H{"containerId": id}, 200))["ticket"].(string)
	socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(f.url, "http")+"/api/v1/events?ticket="+ticket, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	readEvent(t, socket, "connection.ready")
	session := data(f.request(t, "POST", "/containers/"+id+"/sessions", token, gin.H{"cwd": "/workspace"}, 201, "interaction-session"))["id"].(string)
	for i := 0; i < 2; i++ {
		turn := data(f.request(t, "POST", "/sessions/"+session+"/turns", token, gin.H{"clientRequestId": fmt.Sprint("interaction", i), "content": []any{gin.H{"type": "text", "text": "ask"}}}, 202))["id"].(string)
		first := readEvent(t, socket, "interaction.requested")["payload"].(map[string]any)
		second := readEvent(t, socket, "interaction.requested")["payload"].(map[string]any)
		if first["kind"] != "permission" || second["kind"] != "ask" {
			t.Fatal(first, second)
		}
		items := data(f.request(t, "GET", "/containers/"+id+"/interactions", token, nil, 200))["items"].([]any)
		if len(items) != 2 {
			t.Fatal("pending snapshot", items)
		}
		f.request(t, "POST", "/sessions/"+session+"/load", token, gin.H{}, 200)
		other := data(f.request(t, "POST", "/containers/"+id+"/sessions", token, gin.H{"cwd": "/workspace"}, 201, fmt.Sprint("other", i)))["id"].(string)
		f.request(t, "POST", "/sessions/"+other+"/load", token, gin.H{}, 200)

		path := "/turns/" + turn + "/interactions/" + first["interactionId"].(string) + "/response"
		f.request(t, "POST", path, token, gin.H{"response": gin.H{"outcome": gin.H{"outcome": "selected", "optionId": "invented"}}}, 422)
		f.request(t, "POST", path, token, gin.H{"response": gin.H{"outcome": gin.H{"outcome": "selected", "optionId": "allow-once"}}}, 204)
		path = "/turns/" + turn + "/interactions/" + second["interactionId"].(string) + "/response"
		f.request(t, "POST", path, f.admin, gin.H{"response": gin.H{"outcome": "cancelled"}}, 409)
		f.request(t, "POST", path, token, gin.H{"response": gin.H{"outcome": "accepted", "answers": gin.H{"q": []any{1}}}}, 422)
		if i == 0 {
			f.request(t, "POST", path, token, gin.H{"response": gin.H{"outcome": "accepted", "answers": gin.H{"q": []string{"A"}}, "annotations": gin.H{"q": gin.H{"preview": "sample", "notes": "note"}}}}, 204)
			f.request(t, "POST", path, token, gin.H{"response": gin.H{"outcome": "cancelled"}}, 409)
			readEvent(t, socket, "turn.completed")
		} else {
			f.request(t, "POST", "/turns/"+turn+"/cancel", token, gin.H{}, 202)
			readEvent(t, socket, "turn.cancelled")
		}
		items = data(f.request(t, "GET", "/containers/"+id+"/interactions", token, nil, 200))["items"].([]any)
		if len(items) != 0 {
			t.Fatal("pending not cleaned", items)
		}
	}
}

func TestFilePreviewGatewayAuthorizationAndPaths(t *testing.T) {
	endpoint, _ := fakeAgent(t)
	f := setup(t)
	id, _, token := f.assign(t, endpoint, "/workspace")
	route := "/containers/" + id + "/file-content?path="
	got := data(f.request(t, "GET", route+"/workspace/a.txt", token, nil, 200))
	if got["content"] != "hello" {
		t.Fatal(got)
	}
	empty := data(f.request(t, "GET", route+"/workspace/empty", token, nil, 200))
	if empty["type"] != "directory" || len(empty["entries"].([]any)) != 0 {
		t.Fatal(empty)
	}
	f.request(t, "GET", route+"/workspace/a.txt", f.admin, nil, 404)
	for _, p := range []string{"/workspace/../etc/passwd", "/workspace-other/file", "relative", "/etc/passwd"} {
		f.request(t, "GET", route+p, token, nil, 422)
	}
}
