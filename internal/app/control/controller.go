package control

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"echo-service/internal/agentconn"
	"echo-service/internal/auth"
	"echo-service/internal/lib"
	"echo-service/internal/storage"
	"echo-service/internal/table"
	sdk "echo-service/sdk/go/echoservicev1"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const principalKey = "echo.principal"

func (s *Service) Register(engine *gin.Engine) {
	api := engine.Group("/api/v1")
	api.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		c.Next()
	})
	api.POST("/auth/login", s.login)
	api.POST("/auth/refresh", s.refresh)
	secured := api.Group("")
	secured.Use(s.authenticate)
	s.registerRuntime(api, secured)
	secured.GET("/me", s.me)
	secured.GET("/me/auth-sessions", s.authSessions)
	secured.DELETE("/me/auth-sessions/:authSessionId", s.revokeAuthSession)
	secured.PATCH("/me", s.updateMe)
	secured.PUT("/me/password", s.password)
	secured.POST("/auth/logout", s.logout(false))
	secured.POST("/auth/logout-all", s.logout(true))
	secured.GET("/me/containers", s.myContainers)
	secured.GET("/containers/:containerId/status", s.containerStatus)
	secured.GET("/containers/:containerId/models", s.agentRead("_list_models", nil))
	secured.GET("/containers/:containerId/agents", s.agentRead("_list_agents", nil))
	secured.GET("/containers/:containerId/skills", s.skills)
	secured.GET("/containers/:containerId/plugins", s.agentRead("_echo/plugins/installed", nil))
	secured.GET("/containers/:containerId/environment", s.agentRead("_environment/info", map[string]any{"environmentId": "local"}))
	admin := secured.Group("/admin")
	admin.Use(func(c *gin.Context) {
		if principal(c).Role != "admin" {
			lib.Error(c, 403, "PermissionDenied", "Administrator role required")
			return
		}
		c.Next()
	})
	admin.GET("/users", s.listUsers)
	admin.POST("/users", s.createUser)
	admin.GET("/users/:userId", s.getUser)
	admin.PATCH("/users/:userId", s.updateUser)
	admin.GET("/containers", s.listContainers)
	admin.POST("/containers", s.createContainer)
	admin.GET("/containers/:containerId", s.getContainer)
	admin.PATCH("/containers/:containerId", s.updateContainer)
	admin.GET("/containers/:containerId/users", s.listAssignments)
	admin.PUT("/containers/:containerId/users/:userId", s.assign)
	admin.DELETE("/containers/:containerId/users/:userId", s.revoke)
	admin.POST("/containers/:containerId/probe", s.probe)
	admin.GET("/audit-events", s.auditEvents)
}
func (s *Service) authenticate(c *gin.Context) {
	h := strings.Fields(c.GetHeader("Authorization"))
	if len(h) != 2 || !strings.EqualFold(h[0], "Bearer") {
		lib.Error(c, 401, "Unauthorized", "A bearer token is required")
		return
	}
	p, e := s.Auth.Authenticate(c.Request.Context(), h[1])
	if e != nil {
		if errors.Is(e, auth.ErrToken) {
			lib.Error(c, 401, "InvalidToken", "Token is invalid, expired or revoked")
		} else {
			lib.Error(c, 503, "AuthenticationUnavailable", "Authentication is unavailable")
		}
		return
	}
	c.Set(principalKey, p)
	c.Next()
}
func principal(c *gin.Context) auth.Principal { p, _ := c.Get(principalKey); return p.(auth.Principal) }
func decode(c *gin.Context, v any) bool {
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		lib.Error(c, 422, "ValidationFailed", "Invalid JSON request")
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		lib.Error(c, 422, "ValidationFailed", "One JSON object is required")
		return false
	}
	return true
}
func respond(c *gin.Context, status int, data any) {
	c.JSON(status, sdk.Response[any]{Data: data, Error: sdk.Error{RequestID: c.GetHeader("Request-ID")}})
}
func failed(c *gin.Context, e error) {
	switch {
	case errors.Is(e, storage.ErrNotFound) || errors.Is(e, gorm.ErrRecordNotFound):
		lib.Error(c, 404, "ResourceNotFound", "Resource not found")
	case errors.Is(e, storage.ErrConflict):
		lib.Error(c, 409, "Conflict", "Operation conflicts with current state")
	default:
		lib.Error(c, 500, "InternalError", "Operation could not be completed")
	}
}
func pagination(c *gin.Context) (int, int) {
	limit := 50
	if v, e := strconv.Atoi(c.Query("limit")); e == nil && v > 0 && v <= 200 {
		limit = v
	}
	offset := 0
	if v, e := strconv.Atoi(c.Query("offset")); e == nil && v > 0 {
		offset = v
	}
	return limit, offset
}
func (s *Service) login(c *gin.Context) {
	var req sdk.LoginRequest
	if !decode(c, &req) {
		return
	}
	host, _, _ := net.SplitHostPort(c.Request.RemoteAddr)
	name, _ := auth.NormalizeUsername(req.Username)
	if !s.limiter.allow("login:ip:"+host) || !s.limiter.allow("login:user:"+name) {
		lib.Error(c, 429, "RateLimited", "Try again later")
		return
	}
	if e := s.acquireHash(c.Request.Context()); e != nil {
		lib.Error(c, 429, "RateLimited", "Try again later")
		return
	}
	defer func() { <-s.hashSlots }()
	tokens, u, e := s.Auth.Login(c.Request.Context(), req.Username, req.Password, req.DeviceName)
	if e != nil {
		if errors.Is(e, auth.ErrCredentials) {
			lib.Error(c, 401, "InvalidCredentials", "Invalid username or password")
		} else {
			failed(c, e)
		}
		return
	}
	_ = s.Repo.Audit(c.Request.Context(), u.ID, "auth.login", u.ID, "success")
	lib.OK(c, gin.H{"tokens": tokens, "user": userDTO(u)})
}
func (s *Service) refresh(c *gin.Context) {
	var req sdk.RefreshRequest
	if !decode(c, &req) {
		return
	}
	host, _, _ := net.SplitHostPort(c.Request.RemoteAddr)
	if !s.limiter.allow("refresh:" + host) {
		lib.Error(c, 429, "RateLimited", "Try again later")
		return
	}
	tokens, _, e := s.Auth.Refresh(c.Request.Context(), req.RefreshToken)
	if e != nil {
		if errors.Is(e, auth.ErrToken) || errors.Is(e, auth.ErrReplay) {
			lib.Error(c, 401, "InvalidToken", "Refresh token is invalid or revoked")
		} else {
			failed(c, e)
		}
		return
	}
	// Each socket rechecks its own access token; rotating one login must not
	// close the user-scoped upstream connection or other devices.
	lib.OK(c, tokens)
}
func (s *Service) logout(all bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		s.access.Lock()
		defer s.access.Unlock()
		p := principal(c)
		if e := s.Auth.Logout(c.Request.Context(), p, all); e != nil {
			failed(c, e)
			return
		}
		if all {
			s.invalidate(p.UserID, "")
		}
		c.Status(204)
	}
}
func (s *Service) me(c *gin.Context) {
	p := principal(c)
	u, e := s.Directory.User(c.Request.Context(), p.UserID)
	if e != nil {
		failed(c, e)
		return
	}
	lib.OK(c, gin.H{"user": userDTO(u), "permissions": permissions(p.Role)})
}
func permissions(role string) []string {
	p := []string{"profile:read", "profile:write", "assigned-container:use"}
	if role == "admin" {
		p = append(p, "user:manage", "container:manage", "assignment:manage", "audit:read")
	}
	return p
}
func (s *Service) updateMe(c *gin.Context) {
	var req struct {
		DisplayName string `json:"displayName"`
	}
	if !decode(c, &req) {
		return
	}
	if len(req.DisplayName) > 128 {
		lib.Error(c, 422, "ValidationFailed", "displayName is too long")
		return
	}
	if e := s.Directory.UpdateUser(c.Request.Context(), principal(c).UserID, map[string]any{"display_name": req.DisplayName}); e != nil {
		failed(c, e)
		return
	}
	s.me(c)
}
func (s *Service) password(c *gin.Context) {
	var req sdk.PasswordRequest
	if !decode(c, &req) {
		return
	}
	if len(req.NewPassword) < 12 || len(req.NewPassword) > 256 {
		lib.Error(c, 422, "ValidationFailed", "Password must contain 12..256 bytes")
		return
	}
	if s.acquireHash(c.Request.Context()) != nil {
		lib.Error(c, 429, "RateLimited", "Try again later")
		return
	}
	defer func() { <-s.hashSlots }()
	s.access.Lock()
	defer s.access.Unlock()
	p := principal(c)
	if e := s.Auth.ChangePassword(c.Request.Context(), p, req.OldPassword, req.NewPassword); e != nil {
		if errors.Is(e, auth.ErrCredentials) {
			lib.Error(c, 401, "InvalidCredentials", "Current password is invalid")
		} else {
			failed(c, e)
		}
		return
	}
	s.invalidate(p.UserID, "")
	c.Status(204)
}
func (s *Service) myContainers(c *gin.Context) {
	items, e := s.Repo.UserContainers(c.Request.Context(), principal(c).UserID)
	if e != nil {
		failed(c, e)
		return
	}
	out := make([]sdk.Container, 0, len(items))
	for _, v := range items {
		out = append(out, containerDTO(v, false))
	}
	lib.OK(c, gin.H{"items": out})
}
func (s *Service) listUsers(c *gin.Context) {
	limit, offset := pagination(c)
	items, e := s.Repo.Users(c.Request.Context(), limit, offset)
	if e != nil {
		failed(c, e)
		return
	}
	out := make([]sdk.User, 0, len(items))
	for _, v := range items {
		out = append(out, userDTO(v))
	}
	lib.OK(c, gin.H{"items": out, "limit": limit, "offset": offset})
}
func (s *Service) getUser(c *gin.Context) {
	u, e := s.Directory.User(c.Request.Context(), c.Param("userId"))
	if e != nil {
		failed(c, e)
		return
	}
	lib.OK(c, userDTO(u))
}
func (s *Service) createUser(c *gin.Context) {
	var req sdk.CreateUserRequest
	if !decode(c, &req) {
		return
	}
	if _, ok := auth.NormalizeUsername(req.Username); !ok || len(req.Password) < 12 || len(req.Password) > 256 || len(req.DisplayName) > 128 {
		lib.Error(c, 422, "ValidationFailed", "Invalid username, password or displayName")
		return
	}
	if req.PlatformRole == "" {
		req.PlatformRole = "user"
	}
	if req.PlatformRole != "admin" && req.PlatformRole != "user" {
		lib.Error(c, 422, "ValidationFailed", "Invalid platformRole")
		return
	}
	if s.acquireHash(c.Request.Context()) != nil {
		lib.Error(c, 429, "RateLimited", "Try again later")
		return
	}
	defer func() { <-s.hashSlots }()
	u, e := s.Auth.CreateUser(c.Request.Context(), req.Username, req.DisplayName, req.Password, req.PlatformRole)
	if e != nil {
		lib.Error(c, 409, "UserConflict", "User could not be created")
		return
	}
	_ = s.Repo.Audit(c.Request.Context(), principal(c).UserID, "user.create", u.ID, "success")
	respond(c, 201, userDTO(u))
}
func (s *Service) updateUser(c *gin.Context) {
	var req sdk.UpdateUserRequest
	if !decode(c, &req) {
		return
	}
	v := map[string]any{}
	if req.DisplayName != nil {
		if len(*req.DisplayName) > 128 {
			lib.Error(c, 422, "ValidationFailed", "Invalid displayName")
			return
		}
		v["display_name"] = *req.DisplayName
	}
	if req.Status != nil {
		if *req.Status != "active" && *req.Status != "disabled" {
			lib.Error(c, 422, "ValidationFailed", "Invalid status")
			return
		}
		v["status"] = *req.Status
	}
	if req.PlatformRole != nil {
		if *req.PlatformRole != "admin" && *req.PlatformRole != "user" {
			lib.Error(c, 422, "ValidationFailed", "Invalid role")
			return
		}
		v["platform_role"] = *req.PlatformRole
	}
	if len(v) == 0 {
		lib.Error(c, 422, "ValidationFailed", "No fields provided")
		return
	}
	s.access.Lock()
	defer s.access.Unlock()
	id := c.Param("userId")
	if e := s.Directory.UpdateUser(c.Request.Context(), id, v); e != nil {
		failed(c, e)
		return
	}
	s.invalidate(id, "")
	_ = s.Repo.Audit(c.Request.Context(), principal(c).UserID, "user.update", id, "success")
	s.getUser(c)
}
func (s *Service) listContainers(c *gin.Context) {
	limit, offset := pagination(c)
	items, e := s.Repo.Containers(c.Request.Context(), limit, offset)
	if e != nil {
		failed(c, e)
		return
	}
	out := make([]sdk.Container, 0, len(items))
	for _, v := range items {
		out = append(out, containerDTO(v, true))
	}
	lib.OK(c, gin.H{"items": out, "limit": limit, "offset": offset})
}
func (s *Service) getContainer(c *gin.Context) {
	item, e := s.Directory.Container(c.Request.Context(), c.Param("containerId"))
	if e != nil {
		failed(c, e)
		return
	}
	lib.OK(c, containerDTO(item, true))
}
func (s *Service) createContainer(c *gin.Context) {
	var req sdk.CreateContainerRequest
	if !decode(c, &req) {
		return
	}
	if len(req.Name) == 0 || len(req.Name) > 128 || agentconn.ValidateEndpoint(req.Endpoint) != nil || len(req.WorkspaceRoots) < 1 || len(req.WorkspaceRoots) > 16 {
		lib.Error(c, 422, "ValidationFailed", "Invalid container name, private IP endpoint or workspaceRoots")
		return
	}
	for _, root := range req.WorkspaceRoots {
		if !path.IsAbs(root) || path.Clean(root) != root || strings.ContainsRune(root, 0) {
			lib.Error(c, 422, "ValidationFailed", "workspaceRoots must be clean absolute container paths")
			return
		}
	}
	if !validWebURL(req.WebURL) {
		lib.Error(c, 422, "ValidationFailed", "webUrl must be an HTTP(S) URL without credentials")
		return
	}
	roots, _ := json.Marshal(req.WorkspaceRoots)
	item := table.Container{ID: uuid.NewString(), Name: req.Name, Endpoint: req.Endpoint, WebURL: req.WebURL, WorkspaceRootsJSON: string(roots), Status: "enabled"}
	if e := s.Repo.CreateContainer(c.Request.Context(), &item); e != nil {
		lib.Error(c, 409, "ContainerConflict", "Container could not be registered")
		return
	}
	_ = s.Repo.Audit(c.Request.Context(), principal(c).UserID, "container.create", item.ID, "success")
	respond(c, 201, containerDTO(item, true))
}
func (s *Service) updateContainer(c *gin.Context) {
	var req sdk.UpdateContainerRequest
	if !decode(c, &req) {
		return
	}
	values := map[string]any{}
	if req.WebURL != nil {
		if !validWebURL(*req.WebURL) {
			lib.Error(c, 422, "ValidationFailed", "webUrl must be an HTTP(S) URL without credentials")
			return
		}
		values["web_url"] = *req.WebURL
	}
	if req.Name != nil {
		if len(*req.Name) == 0 || len(*req.Name) > 128 {
			lib.Error(c, 422, "ValidationFailed", "Invalid name")
			return
		}
		values["name"] = *req.Name
	}
	if req.Status != nil {
		if *req.Status != "enabled" && *req.Status != "disabled" {
			lib.Error(c, 422, "ValidationFailed", "Invalid status")
			return
		}
		values["status"] = *req.Status
	}
	if len(values) == 0 {
		lib.Error(c, 422, "ValidationFailed", "No fields provided")
		return
	}
	s.access.Lock()
	defer s.access.Unlock()
	id := c.Param("containerId")
	if e := s.Directory.UpdateContainer(c.Request.Context(), id, values); e != nil {
		failed(c, e)
		return
	}
	s.invalidate("", id)
	_ = s.Repo.Audit(c.Request.Context(), principal(c).UserID, "container.update", id, "success")
	s.getContainer(c)
}
func (s *Service) listAssignments(c *gin.Context) {
	a, e := s.Repo.Assignments(c.Request.Context(), c.Param("containerId"))
	if e != nil {
		failed(c, e)
		return
	}
	out := make([]sdk.Assignment, 0, len(a))
	for _, v := range a {
		out = append(out, assignmentDTO(v))
	}
	lib.OK(c, gin.H{"items": out})
}
func (s *Service) assign(c *gin.Context) {
	var req struct {
		Role string `json:"role"`
	}
	if !decode(c, &req) {
		return
	}
	if req.Role != "owner" {
		lib.Error(c, 422, "ValidationFailed", "Only owner assignments are supported")
		return
	}
	s.access.Lock()
	defer s.access.Unlock()
	a, e := s.Repo.Assign(c.Request.Context(), c.Param("userId"), c.Param("containerId"))
	if e != nil {
		failed(c, e)
		return
	}
	_ = s.Repo.Audit(c.Request.Context(), principal(c).UserID, "assignment.create", a.ID, "success")
	lib.OK(c, assignmentDTO(a))
}
func (s *Service) revoke(c *gin.Context) {
	s.access.Lock()
	defer s.access.Unlock()
	if e := s.Repo.RevokeAssignment(c.Request.Context(), c.Param("userId"), c.Param("containerId")); e != nil {
		failed(c, e)
		return
	}
	s.invalidate(c.Param("userId"), c.Param("containerId"))
	_ = s.Repo.Audit(c.Request.Context(), principal(c).UserID, "assignment.revoke", c.Param("containerId"), "success")
	c.Status(204)
}
func (s *Service) containerStatus(c *gin.Context) {
	p := principal(c)
	item, a, e := s.ContainerFor(c.Request.Context(), p, c.Param("containerId"))
	if e != nil {
		failed(c, e)
		return
	}
	lib.OK(c, gin.H{"container": containerDTO(item, false), "connectivity": s.Connections.Status(agentconn.Key{UserID: p.UserID, AssignmentID: a.ID, ContainerID: item.ID})})
}
func (s *Service) probe(c *gin.Context) {
	item, e := s.Repo.Container(c.Request.Context(), c.Param("containerId"))
	if e != nil {
		failed(c, e)
		return
	}
	conn, e := agentconn.Dial(c.Request.Context(), item.Endpoint, s.ConnectionsConfig())
	if e != nil {
		lib.Error(c, 503, "ContainerUnavailable", "Container probe failed")
		return
	}
	defer conn.Close()
	lib.OK(c, gin.H{"ready": true})
}
func (s *Service) agentRead(method string, params map[string]any) gin.HandlerFunc {
	return func(c *gin.Context) {
		s.access.RLock()
		defer s.access.RUnlock()
		p := principal(c)
		conn, e := s.connection(c.Request.Context(), p, c.Param("containerId"))
		if e != nil {
			lib.Error(c, 503, "ContainerUnavailable", "Container connection is unavailable")
			return
		}
		args := make(map[string]any, len(params))
		for k, v := range params {
			args[k] = v
		}
		result, e := conn.Call(c.Request.Context(), method, args)
		if e != nil {
			var rpc *agentconn.RPCError
			if errors.As(e, &rpc) && rpc.Code == -32601 {
				lib.Error(c, 501, "CapabilityUnsupported", "Container does not support this capability")
			} else {
				lib.Error(c, 502, "RemoteError", "Container request failed")
			}
			return
		}
		var body any
		if json.Unmarshal(result, &body) != nil {
			lib.Error(c, 502, "RemoteProtocolError", "Invalid container response")
			return
		}
		lib.OK(c, redact(body))
	}
}
func redact(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, value := range x {
			lower := strings.ToLower(k)
			if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "api_key") || strings.Contains(lower, "apikey") || lower == "headers" || lower == "env" || lower == "endpoint" || lower == "base_url" {
				delete(x, k)
			} else {
				x[k] = redact(value)
			}
		}
	case []any:
		for i := range x {
			x[i] = redact(x[i])
		}
	}
	return v
}
func (s *Service) auditEvents(c *gin.Context) {
	limit, offset := pagination(c)
	var rows []table.AuditEvent
	if e := s.Repo.DB.WithContext(c.Request.Context()).Order("created_at desc,id").Limit(limit).Offset(offset).Find(&rows).Error; e != nil {
		failed(c, e)
		return
	}
	lib.OK(c, gin.H{"items": rows, "limit": limit, "offset": offset})
}

func validWebURL(raw string) bool {
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}
