package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"echo-service/internal/agentconn"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type extensionAgent struct {
	mu      sync.Mutex
	calls   []agentconn.Message
	skills  map[string]gin.H
	servers map[string]gin.H
}

func extensionFixture(t *testing.T) (fixture, *extensionAgent, string, string, string) {
	t.Helper()
	a := &extensionAgent{skills: map[string]gin.H{}, servers: map[string]gin.H{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var msg agentconn.Message
			if conn.ReadJSON(&msg) != nil {
				return
			}
			a.mu.Lock()
			a.calls = append(a.calls, msg)
			var p map[string]any
			_ = json.Unmarshal(msg.Params, &p)
			var result any = gin.H{}
			var rpcErr *agentconn.RPCError
			name, _ := p["name"].(string)
			key, _ := p["key"].(string)
			switch msg.Method {
			case "initialize":
				result = gin.H{"protocolVersion": 1}
			case "_list_skills":
				rows := []gin.H{}
				for _, skill := range a.skills {
					rows = append(rows, skill)
				}
				result = gin.H{"skills": rows}
			case "_add_skill":
				a.skills[name] = gin.H{"name": name, "description": p["description"], "content": p["content"], "scope": "user", "source": "/agent/skills/" + name + "/SKILL.md"}
				result = gin.H{"skill": a.skills[name]}
			case "_edit_skill":
				a.skills[name]["description"] = p["description"]
				a.skills[name]["content"] = p["instruction"]
				result = gin.H{"skill": a.skills[name]}
			case "_delete_skill":
				delete(a.skills, name)
			case "_list_mcp":
				result = gin.H{"mcpServers": a.servers}
			case "_edit_mcp":
				var config struct {
					Servers map[string]map[string]any `json:"mcpServers"`
				}
				_ = json.Unmarshal([]byte(p["config"].(string)), &config)
				for k, v := range config.Servers {
					a.servers[k] = gin.H{"key": k, "source": p["source"], "enabled": true, "status": "success", "config": v, "raw_config": v, "functions": []gin.H{}}
				}
			case "_set_mcp_enabled":
				a.servers[key]["enabled"] = p["enabled"]
			case "_test_mcp":
				if key == "unsupported" {
					rpcErr = &agentconn.RPCError{Code: -32601, Message: "secret-from-agent"}
				} else {
					a.servers[key]["status"] = "success"
				}
			case "_delete_mcp":
				delete(a.servers, key)
			default:
				rpcErr = &agentconn.RPCError{Code: -32601, Message: "unsupported"}
			}
			raw, _ := json.Marshal(result)
			response := agentconn.Message{JSONRPC: "2.0", ID: msg.ID, Result: raw, Error: rpcErr}
			a.mu.Unlock()
			if conn.WriteJSON(response) != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	f := setup(t)
	id, user, token := f.assign(t, "ws"+strings.TrimPrefix(server.URL, "http"), "/workspace")
	return f, a, id, user, token
}

func (a *extensionAgent) mutations() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	count := 0
	for _, msg := range a.calls {
		if msg.Method != "initialize" && !strings.HasPrefix(msg.Method, "_list_") {
			count++
		}
	}
	return count
}

func TestExtensionSkillCRUDAndIsolation(t *testing.T) {
	f, a, id, user, token := extensionFixture(t)
	base := "/containers/" + id + "/skills"
	body := gin.H{"name": "review", "description": "Review code", "instruction": "Inspect changes."}
	f.request(t, "POST", base, "", body, 401)
	f.request(t, "POST", base, f.admin, body, 404)
	if a.mutations() != 0 {
		t.Fatal("unauthorized request reached Agent")
	}
	f.request(t, "POST", base, token, body, 201)
	f.request(t, "POST", base, token, body, 409)
	rows := data(f.request(t, "GET", base+"?scope=user", token, nil, 200))["skills"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["content"] != "Inspect changes." {
		t.Fatal(rows)
	}
	body["instruction"] = "Check tests too."
	delete(body, "name")
	f.request(t, "PUT", base+"/review", token, body, 200)
	a.mu.Lock()
	if a.skills["review"]["content"] != "Check tests too." {
		t.Error("edit did not reach runtime")
	}
	for _, msg := range a.calls {
		if msg.Method == "_edit_skill" || msg.Method == "_delete_skill" {
			var p map[string]any
			_ = json.Unmarshal(msg.Params, &p)
			if msg.Method == "_edit_skill" && (p["old_scope"] != "user" || p["new_scope"] != "user") {
				t.Error("wrong edit scope", p)
			}
		}
	}
	a.mu.Unlock()
	f.request(t, "DELETE", base+"/review", token, nil, 204)
	f.request(t, "DELETE", base+"/review", token, nil, 404)
	f.request(t, "DELETE", "/admin/containers/"+id+"/users/"+user, f.admin, nil, 204)
	before := a.mutations()
	f.request(t, "POST", base, token, gin.H{"name": "after-revoke", "description": "d", "instruction": "i"}, 404)
	if a.mutations() != before {
		t.Fatal("revoked request reached runtime")
	}
}

func TestExtensionRejectsInvalidSkillWrites(t *testing.T) {
	f, a, id, _, token := extensionFixture(t)
	base := "/containers/" + id + "/skills"
	for _, body := range []gin.H{
		{"name": "..", "description": "d", "instruction": "i"},
		{"name": "a/b", "description": "d", "instruction": "i"},
		{"name": "review", "description": "", "instruction": "i"},
		{"name": "review", "description": "d", "instruction": ""},
		{"name": "review", "description": "d", "instruction": "i", "source": "/etc/passwd"},
		{"name": "review", "description": "d", "instruction": "i", "scope": "enterprise"},
	} {
		f.request(t, "POST", base, token, body, 422)
	}
	if a.mutations() != 0 {
		t.Fatal("invalid payload mutated runtime")
	}
	a.mu.Lock()
	a.skills["org"] = gin.H{"name": "org", "scope": "enterprise", "source": "/org/SKILL.md", "content": "read-only"}
	a.mu.Unlock()
	f.request(t, "PUT", base+"/org", token, gin.H{"description": "d", "instruction": "i"}, 404)
	f.request(t, "DELETE", base+"/org", token, nil, 404)
}

func TestExtensionMcpManagementProtectsCredentials(t *testing.T) {
	f, a, id, _, token := extensionFixture(t)
	base := "/containers/" + id + "/mcp-servers"
	config := `{"mcpServers":{"demo":{"command":"runner","args":["--private=arg-secret"],"env":{"TOKEN":"env-secret"},"headers":{"Authorization":"header-secret"}}}}`
	f.request(t, "POST", base, token, gin.H{"config": config}, 200)
	listed := f.request(t, "GET", base, token, nil, 200)
	raw, _ := json.Marshal(listed)
	for _, secret := range []string{"arg-secret", "env-secret", "header-secret"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("credential leaked: %s", secret)
		}
	}
	changed := `{"mcpServers":{"demo":{"command":"new-runner"}}}`
	f.request(t, "POST", base, token, gin.H{"config": changed}, 200)
	a.mu.Lock()
	stored := a.servers["demo"]["raw_config"].(map[string]any)
	if stored["env"].(map[string]any)["TOKEN"] != "env-secret" {
		t.Error("hidden credential erased")
	}
	if stored["command"] != "new-runner" {
		t.Error("command not updated")
	}
	a.mu.Unlock()
	f.request(t, "PUT", base+"/demo/enabled", token, gin.H{"enabled": false}, 200)
	a.mu.Lock()
	enabled := a.servers["demo"]["enabled"]
	a.mu.Unlock()
	if enabled != false {
		t.Fatal("toggle not applied")
	}
	f.request(t, "POST", base+"/demo/test", token, nil, 200)
	f.request(t, "DELETE", base+"/demo", token, nil, 204)
	a.mu.Lock()
	remaining := len(a.servers)
	a.mu.Unlock()
	if remaining != 0 {
		t.Fatal("MCP not deleted")
	}
}

func TestExtensionMcpValidationAndUnsupported(t *testing.T) {
	f, a, id, _, token := extensionFixture(t)
	base := "/containers/" + id + "/mcp-servers"
	for _, config := range []string{
		`{}`, `{"mcpServers":{}}`, `{"mcpServers":{"a":{"command":"a"},"b":{"command":"b"}}}`,
		`{"mcpServers":{"a":{"url":"file:///etc/passwd"}}}`, `{"mcpServers":{"a":{"command":""}}}`,
		`{"mcpServers":{"a":{"command":"run","args":[1]}}}`,
		`{"mcpServers":{".":{"command":"run"}}}`, `{"mcpServers":{"..":{"command":"run"}}}`,
	} {
		f.request(t, "POST", base, token, gin.H{"config": config}, 422)
	}
	f.request(t, "POST", base, token, gin.H{"config": `{"mcpServers":{"a":{"command":"run"}}}`, "source": "sys"}, 422)
	if a.mutations() != 0 {
		t.Fatal("invalid MCP mutated runtime")
	}
	a.mu.Lock()
	a.servers["."] = gin.H{"key": ".", "source": "user", "raw_config": gin.H{"command": "legacy"}, "enabled": false, "functions": []gin.H{}}
	a.mu.Unlock()
	f.request(t, "GET", base, token, nil, 200)
	a.mu.Lock()
	a.servers["unsupported"] = gin.H{"key": "unsupported", "source": "user", "raw_config": gin.H{"command": "run"}, "enabled": true, "functions": []gin.H{}}
	a.mu.Unlock()
	response := f.request(t, "POST", base+"/unsupported/test", token, nil, 501)
	raw, _ := json.Marshal(response)
	if strings.Contains(string(raw), "secret-from-agent") {
		t.Fatal("upstream error leaked")
	}
}
