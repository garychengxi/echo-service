package control

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"echo-service/internal/agentconn"
	"echo-service/internal/lib"
	"github.com/gin-gonic/gin"
)

func (s *Service) registerExtensions(api *gin.RouterGroup) {
	api.POST("/containers/:containerId/skills", s.extension("skill.create", s.createSkill))
	api.PUT("/containers/:containerId/skills/:name", s.extension("skill.update", s.updateSkill))
	api.DELETE("/containers/:containerId/skills/:name", s.extension("skill.delete", s.deleteSkill))
	api.GET("/containers/:containerId/mcp-servers", s.extension("", s.listMcpServers))
	api.POST("/containers/:containerId/mcp-servers", s.extension("mcp.save", s.saveMcpServer))
	api.DELETE("/containers/:containerId/mcp-servers/:key", s.extension("mcp.delete", s.deleteMcpServer))
	api.PUT("/containers/:containerId/mcp-servers/:key/enabled", s.extension("mcp.enable", s.enableMcpServer))
	api.POST("/containers/:containerId/mcp-servers/:key/test", s.extension("mcp.test", s.testMcpServer))
}

// Hold the same access lock used by revocation across catalog lookup and mutation.
func (s *Service) extension(action string, handle func(*gin.Context, *agentconn.Connection)) gin.HandlerFunc {
	return func(c *gin.Context) {
		s.access.RLock()
		defer s.access.RUnlock()
		p := principal(c)
		container := c.Param("containerId")
		if _, _, err := s.ContainerFor(c.Request.Context(), p, container); err != nil {
			failed(c, err)
			return
		}
		conn, err := s.connection(c.Request.Context(), p, container)
		if err != nil {
			lib.Error(c, 503, "ContainerUnavailable", "Container connection is unavailable")
			return
		}
		handle(c, conn)
		if action != "" {
			outcome := "success"
			if c.Writer.Status() >= 400 {
				outcome = "failed"
			}
			_ = s.Repo.Audit(c.Request.Context(), p.UserID, action, container, outcome)
		}
	}
}

func validSkillName(name string) bool {
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return false
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

type skillWrite struct {
	Description string `json:"description"`
	Instruction string `json:"instruction"`
}

func (w skillWrite) valid() bool {
	return strings.TrimSpace(w.Description) != "" && utf8.RuneCountInString(w.Description) <= 200 &&
		strings.TrimSpace(w.Instruction) != "" && utf8.RuneCountInString(w.Instruction) <= 80000
}

type runtimeSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Scope       string `json:"scope"`
	Source      string `json:"source"`
}

func (s *Service) userSkills(c *gin.Context, conn *agentconn.Connection) ([]runtimeSkill, bool) {
	raw, ok := s.rpc(c, conn, "_list_skills", gin.H{"scope": "user"})
	if !ok {
		return nil, false
	}
	var catalog struct {
		Skills []runtimeSkill `json:"skills"`
	}
	if json.Unmarshal(raw, &catalog) != nil || catalog.Skills == nil {
		lib.Error(c, 502, "RemoteProtocolError", "Invalid skill catalog")
		return nil, false
	}
	return catalog.Skills, true
}
func findUserSkill(skills []runtimeSkill, name string) (runtimeSkill, int) {
	var found runtimeSkill
	count := 0
	for _, skill := range skills {
		if skill.Name == name && skill.Scope == "user" {
			found = skill
			count++
		}
	}
	return found, count
}
func (s *Service) createSkill(c *gin.Context, conn *agentconn.Connection) {
	var req struct {
		Name string `json:"name"`
		skillWrite
	}
	if !decode(c, &req) {
		return
	}
	if !validSkillName(req.Name) || !req.valid() {
		lib.Error(c, 422, "ValidationFailed", "Invalid skill name, description or instruction")
		return
	}
	skills, ok := s.userSkills(c, conn)
	if !ok {
		return
	}
	if _, count := findUserSkill(skills, req.Name); count != 0 {
		lib.Error(c, 409, "Conflict", "User skill already exists")
		return
	}
	if _, ok = s.rpc(c, conn, "_add_skill", gin.H{"name": req.Name, "description": req.Description, "content": req.Instruction, "scope": "user"}); !ok {
		return
	}
	respond(c, 201, gin.H{"name": req.Name})
}
func (s *Service) resolveSkill(c *gin.Context, conn *agentconn.Connection) (runtimeSkill, bool) {
	name := c.Param("name")
	if !validSkillName(name) {
		lib.Error(c, 422, "ValidationFailed", "Invalid skill name")
		return runtimeSkill{}, false
	}
	skills, ok := s.userSkills(c, conn)
	if !ok {
		return runtimeSkill{}, false
	}
	skill, count := findUserSkill(skills, name)
	if count == 0 {
		lib.Error(c, 404, "ResourceNotFound", "User skill not found")
		return runtimeSkill{}, false
	}
	if count != 1 || skill.Source == "" {
		lib.Error(c, 409, "Conflict", "User skill identity is ambiguous")
		return runtimeSkill{}, false
	}
	return skill, true
}
func (s *Service) updateSkill(c *gin.Context, conn *agentconn.Connection) {
	var req skillWrite
	if !decode(c, &req) {
		return
	}
	if !req.valid() {
		lib.Error(c, 422, "ValidationFailed", "Description and instruction are required")
		return
	}
	skill, ok := s.resolveSkill(c, conn)
	if !ok {
		return
	}
	if _, ok = s.rpc(c, conn, "_edit_skill", gin.H{"name": skill.Name, "description": req.Description, "instruction": req.Instruction, "old_scope": "user", "new_scope": "user"}); !ok {
		return
	}
	lib.OK(c, gin.H{"name": skill.Name})
}
func (s *Service) deleteSkill(c *gin.Context, conn *agentconn.Connection) {
	skill, ok := s.resolveSkill(c, conn)
	if !ok {
		return
	}
	if _, ok = s.rpc(c, conn, "_delete_skill", gin.H{"name": skill.Name, "source": skill.Source, "scope": "user"}); !ok {
		return
	}
	c.Status(http.StatusNoContent)
}

type runtimeMcp struct {
	Key       string         `json:"key"`
	Source    string         `json:"source"`
	Enabled   bool           `json:"enabled"`
	Status    *string        `json:"status"`
	RawConfig map[string]any `json:"raw_config"`
	Functions []struct {
		Name string `json:"name"`
	} `json:"functions"`
}

func validMcpKey(key string) bool {
	return key != "" && len(key) <= 200 && strings.TrimSpace(key) == key && !strings.ContainsAny(key, "/\\\x00\r\n")
}
func (s *Service) mcpCatalog(c *gin.Context, conn *agentconn.Connection) (map[string]runtimeMcp, bool) {
	raw, ok := s.rpc(c, conn, "_list_mcp", gin.H{})
	if !ok {
		return nil, false
	}
	var catalog struct {
		Servers map[string]runtimeMcp `json:"mcpServers"`
	}
	if json.Unmarshal(raw, &catalog) != nil || catalog.Servers == nil {
		lib.Error(c, 502, "RemoteProtocolError", "Invalid MCP catalog")
		return nil, false
	}
	for key, server := range catalog.Servers {
		if !validMcpKey(key) || server.Key != key || (server.Source != "user" && server.Source != "sys") || server.RawConfig == nil ||
			(server.Status != nil && *server.Status != "success" && *server.Status != "error") {
			lib.Error(c, 502, "RemoteProtocolError", "Invalid MCP entry")
			return nil, false
		}
	}
	return catalog.Servers, true
}

// Config values, args, URLs and upstream diagnostics can all contain credentials.
// Return only public status/tool names and a transport hint, never raw config.
func publicMcp(catalog map[string]runtimeMcp) gin.H {
	servers := gin.H{}
	for key, server := range catalog {
		transport := mcpTransport(server.RawConfig)
		config := gin.H{}
		if transport == "stdio" || transport == "http" || transport == "sse" {
			config["transport"] = transport
		}
		functions := server.Functions
		if functions == nil {
			functions = make([]struct {
				Name string `json:"name"`
			}, 0)
		}
		servers[key] = gin.H{"key": key, "source": server.Source, "enabled": server.Enabled, "status": server.Status,
			"config": config, "raw_config": config, "functions": functions, "hasProtectedConfig": true}
	}
	return gin.H{"mcpServers": servers}
}
func (s *Service) listMcpServers(c *gin.Context, conn *agentconn.Connection) {
	catalog, ok := s.mcpCatalog(c, conn)
	if ok {
		lib.OK(c, publicMcp(catalog))
	}
}
func mcpTransport(config map[string]any) string {
	if v, ok := config["transport"].(string); ok {
		return v
	}
	if v, ok := config["type"].(string); ok {
		return v
	}
	if _, ok := config["command"]; ok {
		return "stdio"
	}
	if _, ok := config["url"]; ok {
		return "http"
	}
	return ""
}
func validMcpConfig(config map[string]any) bool {
	if config == nil {
		return false
	}
	switch mcpTransport(config) {
	case "stdio":
		command, ok := config["command"].(string)
		if !ok || strings.TrimSpace(command) == "" {
			return false
		}
		if args, exists := config["args"]; exists {
			values, ok := args.([]any)
			if !ok {
				return false
			}
			for _, v := range values {
				if _, ok := v.(string); !ok {
					return false
				}
			}
		}
	case "http", "sse":
		raw, ok := config["url"].(string)
		if !ok {
			return false
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			return false
		}
	default:
		return false
	}
	for _, field := range []string{"env", "headers"} {
		if value, exists := config[field]; exists {
			object, ok := value.(map[string]any)
			if !ok {
				return false
			}
			for _, v := range object {
				if _, ok := v.(string); !ok {
					return false
				}
			}
		}
	}
	return true
}
func credentialField(key string) bool {
	lower := strings.ToLower(key)
	return lower == "env" || lower == "headers" || strings.Contains(lower, "token") || strings.Contains(lower, "secret") ||
		strings.Contains(lower, "password") || strings.Contains(lower, "api_key") || strings.Contains(lower, "apikey")
}
func preserveCredentials(next, old map[string]any) {
	for key, value := range old {
		replacement, exists := next[key]
		if !exists && credentialField(key) {
			next[key] = value
			continue
		}
		oldObject, oldOK := value.(map[string]any)
		nextObject, nextOK := replacement.(map[string]any)
		if oldOK && nextOK {
			if key == "env" || key == "headers" {
				for name, secret := range oldObject {
					if _, ok := nextObject[name]; !ok {
						nextObject[name] = secret
					}
				}
			} else {
				preserveCredentials(nextObject, oldObject)
			}
		}
	}
}
func (s *Service) saveMcpServer(c *gin.Context, conn *agentconn.Connection) {
	var req struct {
		Config string `json:"config"`
	}
	if !decode(c, &req) {
		return
	}
	var input struct {
		Servers map[string]map[string]any `json:"mcpServers"`
	}
	if len(req.Config) > 1000000 || json.Unmarshal([]byte(req.Config), &input) != nil || len(input.Servers) != 1 {
		lib.Error(c, 422, "ValidationFailed", "Supply one MCP configuration")
		return
	}
	var key string
	var config map[string]any
	for k, v := range input.Servers {
		key = k
		config = v
	}
	if !validMcpKey(key) || key == "." || key == ".." || !validMcpConfig(config) {
		lib.Error(c, 422, "ValidationFailed", "Invalid MCP configuration")
		return
	}
	catalog, ok := s.mcpCatalog(c, conn)
	if !ok {
		return
	}
	if old, exists := catalog[key]; exists {
		if old.Source != "user" {
			lib.Error(c, 403, "PermissionDenied", "System MCP configuration is read-only")
			return
		}
		preserveCredentials(config, old.RawConfig)
	}
	encoded, err := json.Marshal(gin.H{"mcpServers": gin.H{key: config}})
	if err != nil {
		lib.Error(c, 422, "ValidationFailed", "Invalid MCP configuration")
		return
	}
	if _, ok = s.rpc(c, conn, "_edit_mcp", gin.H{"config": string(encoded), "source": "user"}); !ok {
		return
	}
	s.listMcpServers(c, conn)
}
func (s *Service) resolveMcp(c *gin.Context, conn *agentconn.Connection) (runtimeMcp, bool) {
	key := c.Param("key")
	if !validMcpKey(key) {
		lib.Error(c, 422, "ValidationFailed", "Invalid MCP key")
		return runtimeMcp{}, false
	}
	catalog, ok := s.mcpCatalog(c, conn)
	if !ok {
		return runtimeMcp{}, false
	}
	server, exists := catalog[key]
	if !exists {
		lib.Error(c, 404, "ResourceNotFound", "MCP server not found")
		return runtimeMcp{}, false
	}
	return server, true
}
func (s *Service) deleteMcpServer(c *gin.Context, conn *agentconn.Connection) {
	server, ok := s.resolveMcp(c, conn)
	if !ok {
		return
	}
	if server.Source != "user" {
		lib.Error(c, 403, "PermissionDenied", "System MCP configuration is read-only")
		return
	}
	if _, ok = s.rpc(c, conn, "_delete_mcp", gin.H{"key": server.Key}); ok {
		c.Status(http.StatusNoContent)
	}
}
func (s *Service) enableMcpServer(c *gin.Context, conn *agentconn.Connection) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if !decode(c, &req) {
		return
	}
	if req.Enabled == nil {
		lib.Error(c, 422, "ValidationFailed", "enabled is required")
		return
	}
	server, ok := s.resolveMcp(c, conn)
	if !ok {
		return
	}
	if _, ok = s.rpc(c, conn, "_set_mcp_enabled", gin.H{"key": server.Key, "enabled": *req.Enabled}); !ok {
		return
	}
	s.listMcpServers(c, conn)
}
func (s *Service) testMcpServer(c *gin.Context, conn *agentconn.Connection) {
	server, ok := s.resolveMcp(c, conn)
	if !ok {
		return
	}
	if _, ok = s.rpc(c, conn, "_test_mcp", gin.H{"key": server.Key}); !ok {
		return
	}
	s.listMcpServers(c, conn)
}
