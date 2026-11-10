package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"echo-service/internal/agentconn"
	"echo-service/internal/auth"
	"echo-service/internal/events"
	"echo-service/internal/lib"
	"echo-service/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type approval struct {
	conn                           *agentconn.Connection
	rpcID                          json.RawMessage
	user, container, session, turn string
	done                           chan struct{}
	kind                           string
	params                         map[string]any
	options                        map[string]bool
	expires                        time.Time
}
type ticket struct {
	principal auth.Principal
	container string
	expires   time.Time
}

func (s *Service) registerRuntime(api *gin.RouterGroup, secured *gin.RouterGroup) {
	api.GET("/events", s.eventSocket)
	secured = secured.Group("")
	secured.Use(func(c *gin.Context) {
		if c.Request.Method == http.MethodGet || strings.HasSuffix(c.FullPath(), "/response") || strings.HasSuffix(c.FullPath(), "/cancel") {
			c.Next()
			return
		}
		h := sha256.Sum256([]byte(principal(c).UserID))
		lock := &s.commands[int(h[0])%len(s.commands)]
		lock.Lock()
		defer lock.Unlock()
		c.Next()
	})
	secured.POST("/ws-tickets", s.wsTicket)
	secured.GET("/containers/:containerId/projects", s.listProjects)
	secured.POST("/containers/:containerId/projects", s.createProject)
	secured.GET("/projects/:projectId", s.getProject)
	secured.PATCH("/projects/:projectId", s.updateProject)
	secured.DELETE("/projects/:projectId", s.deleteProject)
	secured.GET("/containers/:containerId/sessions", s.listSessions)
	secured.POST("/containers/:containerId/sessions", s.createSession)
	secured.GET("/sessions/:sessionId", s.getSession)
	secured.PATCH("/sessions/:sessionId", s.renameSession)
	secured.DELETE("/sessions/:sessionId", s.deleteSession)
	secured.POST("/sessions/:sessionId/fork", s.forkSession)
	secured.POST("/sessions/:sessionId/load", s.loadSession)
	secured.POST("/sessions/:sessionId/turns", s.prompt)
	secured.PUT("/sessions/:sessionId/model", s.setSessionModel)
	secured.PUT("/sessions/:sessionId/mode", s.setSessionMode)
	secured.GET("/turns/:turnId", s.getTurn)
	secured.POST("/turns/:turnId/cancel", s.cancelTurn)
	secured.POST("/turns/:turnId/approvals/:approvalId/response", s.replyApproval)
	secured.POST("/turns/:turnId/interactions/:interactionId/response", s.replyInteraction)
	secured.GET("/containers/:containerId/interactions", s.listInteractions)
	secured.GET("/containers/:containerId/files", s.listFiles)
	secured.GET("/containers/:containerId/file-content", s.readFile)
	secured.GET("/containers/:containerId/agent-settings", s.agentSettings)
	secured.PATCH("/containers/:containerId/agent-settings", s.agentSettings)
	secured.GET("/containers/:containerId/mcp-servers", s.agentRead("_list_mcp", nil))
}

func (s *Service) connection(ctx context.Context, p auth.Principal, container string) (*agentconn.Connection, error) {
	item, a, e := s.ContainerFor(ctx, p, container)
	if e != nil {
		return nil, e
	}
	conn, e := s.Connections.Acquire(ctx, agentconn.Key{UserID: p.UserID, AssignmentID: a.ID, ContainerID: item.ID}, item.Endpoint)
	if e != nil {
		return nil, e
	}
	s.runtimeMu.Lock()
	if !s.attached[conn] {
		s.attached[conn] = true
		s.wg.Add(1)
		go s.readEvents(conn, p.UserID, container)
	}
	s.runtimeMu.Unlock()
	return conn, nil
}
func (s *Service) readEvents(conn *agentconn.Connection, user, container string) {
	defer s.wg.Done()
	// Child ACP sessions are runtime identities, not independently authorized
	// database sessions. Only route children announced by an owned parent on
	// this connection; discard these mappings when the connection closes.
	children := make(map[string]storage.Session)
	defer func() {
		s.runtimeMu.Lock()
		delete(s.attached, conn)
		for id, c := range s.loaded {
			if c == conn {
				delete(s.loaded, id)
			}
		}
		for id, a := range s.approvals {
			if a.conn == conn {
				delete(s.approvals, id)
				close(a.done)
			}
		}
		s.runtimeMu.Unlock()
		s.hub.Publish(events.Key{UserID: user, ContainerID: container}, events.Event{Type: "stream.reset", Payload: gin.H{"reason": "upstreamDisconnected", "requiresLoad": true}})
	}()
	for msg := range conn.Events() {
		var params map[string]any
		if json.Unmarshal(msg.Params, &params) != nil {
			continue
		}
		remote, _ := params["sessionId"].(string)
		var session storage.Session
		childSessionID := ""
		if e := s.Repo.DB.WithContext(s.ctx).First(&session, "remote_id=? AND container_id=? AND user_id=?", remote, container, user).Error; e != nil {
			parent, known := children[remote]
			if known && (msg.Method == "session/update" || isInteractionRequest(msg.Method)) {
				session = parent
				childSessionID = remote
			} else {
				if len(msg.ID) > 0 && isInteractionRequest(msg.Method) {
					_ = conn.Reply(msg.ID, cancelledInteraction(msg.Method))
				}
				continue
			}
		}
		var turn storage.Turn
		requestID := ""
		if meta, ok := params["_meta"].(map[string]any); ok {
			requestID, _ = meta["request_id"].(string)
		}
		if requestID != "" {
			_ = s.Repo.DB.WithContext(s.ctx).First(&turn, "id=? AND session_id=?", requestID, session.ID).Error
		} else {
			_ = s.Repo.DB.WithContext(s.ctx).Where("session_id=? AND state IN ?", session.ID, []string{"running", "awaiting_approval", "cancelling"}).First(&turn).Error
		}
		if isInteractionRequest(msg.Method) && len(msg.ID) > 0 {
			s.requestInteraction(conn, msg, params, session, turn, user, container)
		} else if msg.Method == "session/update" || msg.Method == "_codeflow/session_notification" || strings.HasPrefix(msg.Method, "codeflow/") || strings.HasPrefix(msg.Method, "x.ai/") {
			if update, ok := params["update"].(map[string]any); ok && update["sessionUpdate"] == "subagent_spawned" {
				if child, ok := update["child_session_id"].(string); ok && child != "" && len(children) < 4096 {
					children[child] = session
				}
			}
			delete(params, "sessionId")
			if childSessionID != "" {
				params["childSessionId"] = childSessionID
			}
			s.hub.Publish(events.Key{UserID: user, ContainerID: container}, events.Event{Type: "session.update", SessionID: session.ID, TurnID: turn.ID, Payload: redact(params)})
		}
	}
}
func (s *Service) rpc(c *gin.Context, conn *agentconn.Connection, method string, params any) (json.RawMessage, bool) {
	raw, e := conn.Call(c.Request.Context(), method, params)
	if e != nil {
		var re *agentconn.RPCError
		if errors.As(e, &re) && re.Code == -32601 {
			lib.Error(c, 501, "CapabilityUnsupported", "Capability is unsupported")
		} else if errors.Is(e, context.DeadlineExceeded) {
			lib.Error(c, 504, "RemoteTimeout", "Container request timed out; mutation outcome may be unknown")
		} else {
			lib.Error(c, 502, "RemoteError", "Container operation failed")
		}
		return nil, false
	}
	return raw, true
}
func (s *Service) project(c *gin.Context) (storage.Project, *agentconn.Connection, bool) {
	p := principal(c)
	var item storage.Project
	if e := s.Repo.DB.WithContext(c.Request.Context()).First(&item, "id=? AND user_id=?", c.Param("projectId"), p.UserID).Error; e != nil {
		failed(c, storage.ErrNotFound)
		return item, nil, false
	}
	conn, e := s.connection(c.Request.Context(), p, item.ContainerID)
	if e != nil {
		failed(c, storage.ErrNotFound)
		return item, nil, false
	}
	return item, conn, true
}
func projectDTO(p storage.Project) gin.H {
	var roots any
	_ = json.Unmarshal([]byte(p.RootsJSON), &roots)
	return gin.H{"executionType": "remote", "id": p.ID, "containerId": p.ContainerID, "name": p.Name, "roots": roots, "createdAt": time.UnixMilli(p.CreatedAt).UTC().Format(time.RFC3339Nano)}
}
func (s *Service) listProjects(c *gin.Context) {
	p := principal(c)
	if _, _, e := s.ContainerFor(c.Request.Context(), p, c.Param("containerId")); e != nil {
		failed(c, e)
		return
	}
	limit, offset := pagination(c)
	var rows []storage.Project
	if e := s.Repo.DB.WithContext(c.Request.Context()).Where("user_id=? AND container_id=?", p.UserID, c.Param("containerId")).Order("created_at,id").Limit(limit).Offset(offset).Find(&rows).Error; e != nil {
		failed(c, e)
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, v := range rows {
		items = append(items, projectDTO(v))
	}
	lib.OK(c, gin.H{"items": items, "source": "platformIndex", "limit": limit, "offset": offset})
}
func (s *Service) createProject(c *gin.Context) {
	var req struct {
		Name  string `json:"name"`
		Roots []struct {
			Path string `json:"path"`
		} `json:"roots"`
	}
	if !decode(c, &req) {
		return
	}
	key := c.GetHeader("Idempotency-Key")
	if key == "" || len(key) > 128 || strings.TrimSpace(req.Name) == "" || len(req.Name) > 128 || len(req.Roots) < 1 || len(req.Roots) > 16 {
		lib.Error(c, 422, "ValidationFailed", "Idempotency-Key, name and roots are required")
		return
	}
	s.access.RLock()
	defer s.access.RUnlock()
	p := principal(c)
	container, a, e := s.ContainerFor(c.Request.Context(), p, c.Param("containerId"))
	if e != nil {
		failed(c, e)
		return
	}
	var allowed []string
	_ = json.Unmarshal([]byte(container.WorkspaceRootsJSON), &allowed)
	for _, r := range req.Roots {
		if !allowedPath(r.Path, allowed) {
			lib.Error(c, 422, "ValidationFailed", "Project roots must be within container workspace roots")
			return
		}
	}
	roots, _ := json.Marshal(req.Roots)
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(p.UserID+":"+container.ID+":"+key)).String()
	var existing storage.Project
	if e := s.Repo.DB.WithContext(c.Request.Context()).First(&existing, "id=?", id).Error; e == nil {
		if existing.Name != req.Name || existing.RootsJSON != string(roots) {
			lib.Error(c, 409, "IdempotencyConflict", "Idempotency-Key payload differs")
			return
		}
		lib.OK(c, projectDTO(existing))
		return
	}
	conn, e := s.connection(c.Request.Context(), p, container.ID)
	if e != nil {
		lib.Error(c, 503, "ContainerUnavailable", "Container is unavailable")
		return
	}
	raw, ok := s.rpc(c, conn, "_project/create", gin.H{"name": req.Name, "roots": req.Roots, "idempotencyKey": id})
	if !ok {
		return
	}
	var body struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Project.ID == "" {
		lib.Error(c, 502, "RemoteProtocolError", "Invalid project response")
		return
	}
	record := storage.Project{ID: id, UserID: p.UserID, AssignmentID: a.ID, ContainerID: container.ID, RemoteID: body.Project.ID, Name: req.Name, RootsJSON: string(roots)}
	if e := s.Repo.DB.WithContext(c.Request.Context()).Create(&record).Error; e != nil {
		lib.Error(c, 409, "ProjectIndexConflict", "Remote project may exist; retry with the same Idempotency-Key")
		return
	}
	respond(c, 201, projectDTO(record))
}
func (s *Service) getProject(c *gin.Context) {
	s.access.RLock()
	defer s.access.RUnlock()
	record, conn, ok := s.project(c)
	if !ok {
		return
	}
	raw, ok := s.rpc(c, conn, "_project/read", gin.H{"projectId": record.RemoteID})
	if !ok {
		return
	}
	var data map[string]any
	_ = json.Unmarshal(raw, &data)
	lib.OK(c, gin.H{"project": projectDTO(record), "runtime": redact(data)})
}
func (s *Service) deleteProject(c *gin.Context) {
	s.access.RLock()
	defer s.access.RUnlock()
	record, conn, ok := s.project(c)
	if !ok {
		return
	}
	var active int64
	_ = s.Repo.DB.WithContext(c.Request.Context()).Table("turns").Joins("JOIN sessions ON sessions.id=turns.session_id").Where("sessions.project_id=? AND turns.state IN ?", record.ID, []string{"running", "awaiting_approval", "cancelling"}).Count(&active).Error
	if active > 0 {
		lib.Error(c, 409, "TurnAlreadyRunning", "Project has active turns")
		return
	}
	if _, ok = s.rpc(c, conn, "_project/delete", gin.H{"projectId": record.RemoteID}); !ok {
		return
	}
	if e := s.Repo.DB.WithContext(c.Request.Context()).Delete(&record).Error; e != nil {
		failed(c, e)
		return
	}
	c.Status(204)
}
func allowedPath(p string, roots []string) bool {
	if !path.IsAbs(p) || path.Clean(p) != p || strings.ContainsRune(p, 0) {
		return false
	}
	for _, root := range roots {
		if p == root || root == "/" || strings.HasPrefix(p, strings.TrimSuffix(root, "/")+"/") {
			return true
		}
	}
	return false
}
func (s *Service) session(c *gin.Context, id string) (storage.Session, *agentconn.Connection, bool) {
	p := principal(c)
	var row storage.Session
	if e := s.Repo.DB.WithContext(c.Request.Context()).First(&row, "id=? AND user_id=?", id, p.UserID).Error; e != nil {
		failed(c, storage.ErrNotFound)
		return row, nil, false
	}
	conn, e := s.connection(c.Request.Context(), p, row.ContainerID)
	if e != nil {
		lib.Error(c, 503, "ContainerUnavailable", "Container is unavailable or access was revoked")
		return row, nil, false
	}
	return row, conn, true
}
func (s *Service) listSessions(c *gin.Context) {
	p := principal(c)
	if _, _, e := s.ContainerFor(c.Request.Context(), p, c.Param("containerId")); e != nil {
		failed(c, e)
		return
	}
	limit, offset := pagination(c)
	rows := []storage.Session{}
	q := s.Repo.DB.WithContext(c.Request.Context()).Where("user_id=? AND container_id=?", p.UserID, c.Param("containerId"))
	if id := c.Query("projectId"); id != "" {
		q = q.Where("project_id=?", id)
	}
	if e := q.Order("created_at,id").Limit(limit).Offset(offset).Find(&rows).Error; e != nil {
		failed(c, e)
		return
	}
	lib.OK(c, gin.H{"items": rows, "limit": limit, "offset": offset})
}
func (s *Service) createSession(c *gin.Context) {
	var req struct {
		ProjectID   string `json:"projectId"`
		SessionType string `json:"sessionType"`
		Cwd         string `json:"cwd"`
	}
	if !decode(c, &req) {
		return
	}
	s.access.RLock()
	defer s.access.RUnlock()
	p := principal(c)
	container, a, e := s.ContainerFor(c.Request.Context(), p, c.Param("containerId"))
	if e != nil {
		failed(c, e)
		return
	}
	params := gin.H{"mcpServers": []any{}}
	var projectID *string
	var roots []string
	_ = json.Unmarshal([]byte(container.WorkspaceRootsJSON), &roots)
	if req.ProjectID != "" {
		var project storage.Project
		if s.Repo.DB.WithContext(c.Request.Context()).First(&project, "id=? AND user_id=? AND container_id=?", req.ProjectID, p.UserID, container.ID).Error != nil {
			failed(c, storage.ErrNotFound)
			return
		}
		var list []struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal([]byte(project.RootsJSON), &list)
		roots = nil
		for _, r := range list {
			roots = append(roots, r.Path)
		}
		id := project.ID
		projectID = &id
		params["_meta"] = gin.H{"codeflow/session": gin.H{"projectId": project.RemoteID}}
	}
	if req.Cwd == "" && projectID != nil && len(roots) > 0 {
		req.Cwd = roots[0]
	}
	if req.SessionType != "" && req.SessionType != "project_local" && req.SessionType != "project_work" {
		lib.Error(c, 422, "ValidationFailed", "Invalid sessionType")
		return
	}
	if req.SessionType == "project_work" && (req.Cwd != "" || req.ProjectID != "") {
		lib.Error(c, 422, "ValidationFailed", "Work sessions must not specify a project or cwd")
		return
	}
	if req.Cwd == "" && req.SessionType != "project_work" {
		lib.Error(c, 422, "ValidationFailed", "Select a workspace directory before creating a session")
		return
	}
	if req.SessionType != "project_work" && !allowedPath(req.Cwd, roots) {
		lib.Error(c, 422, "ValidationFailed", "Invalid session cwd")
		return
	}
	if req.SessionType != "project_work" {
		params["cwd"] = req.Cwd
	} else {
		// ACP requires cwd on session/new; an empty path activates Echo's
		// project_work default without selecting or exposing a workspace.
		params["cwd"] = ""
		params["_meta"] = gin.H{"codeflow/session": gin.H{"type": "project_work"}}
	}
	op, proceed := s.reserveOperation(c, container.ID, "session.new", params)
	if !proceed {
		return
	}
	conn, e := s.connection(c.Request.Context(), p, container.ID)
	if e != nil {
		lib.Error(c, 503, "ContainerUnavailable", "Container is unavailable")
		return
	}
	raw, ok := s.rpc(c, conn, "session/new", params)
	if !ok {
		return
	}
	var result struct {
		SessionID string `json:"sessionId"`
		Models    struct {
			CurrentModelID string `json:"currentModelId"`
		} `json:"models"`
	}
	if json.Unmarshal(raw, &result) != nil || result.SessionID == "" {
		lib.Error(c, 502, "RemoteProtocolError", "Invalid session response")
		return
	}
	// A container's existing defaults must never bypass gateway permission policy.
	if _, ok := s.rpc(c, conn, "session/set_mode", gin.H{"sessionId": result.SessionID, "modeId": "always-ask"}); !ok {
		return
	}
	row := storage.Session{ID: uuid.NewString(), UserID: p.UserID, AssignmentID: a.ID, ContainerID: container.ID, RemoteID: result.SessionID, ProjectID: projectID, Title: "New session", Cwd: req.Cwd, ModelID: result.Models.CurrentModelID, ModeID: "always-ask"}
	if e := s.saveSession(c.Request.Context(), &row, op); e != nil {
		lib.Error(c, 500, "SessionIndexFailed", "Session may exist remotely; do not automatically retry")
		return
	}
	s.runtimeMu.Lock()
	s.loaded[row.ID] = conn
	s.runtimeMu.Unlock()
	respond(c, 201, row)
}
func (s *Service) getSession(c *gin.Context) {
	var row storage.Session
	p := principal(c)
	if s.Repo.DB.WithContext(c.Request.Context()).First(&row, "id=? AND user_id=?", c.Param("sessionId"), p.UserID).Error != nil {
		failed(c, storage.ErrNotFound)
		return
	}
	if _, _, e := s.ContainerFor(c.Request.Context(), p, row.ContainerID); e != nil {
		failed(c, e)
		return
	}
	lib.OK(c, row)
}
func (s *Service) loadSession(c *gin.Context) {
	s.access.RLock()
	defer s.access.RUnlock()
	row, conn, ok := s.session(c, c.Param("sessionId"))
	if !ok {
		return
	}
	if s.hasActiveTurn(row.ID) {
		s.runtimeMu.Lock()
		same := s.loaded[row.ID] == conn
		s.runtimeMu.Unlock()
		if same {
			// Reattach the browser without replaying or resetting an active ACP session.
			lib.OK(c, gin.H{"sessionId": row.ID, "loaded": true, "active": true})
		} else {
			lib.Error(c, 409, "TurnOutcomeUnknown", "Original connection was lost")
		}
		return
	}
	// The deployed ACP server cannot load history while a prompt is waiting
	// for a client response on the same connection. Fail promptly so controls
	// for that turn remain usable instead of queuing loads behind a command lock.
	var active int64
	if err := s.Repo.DB.WithContext(c.Request.Context()).Table("turns").Joins("JOIN sessions ON sessions.id=turns.session_id").Where("turns.user_id=? AND sessions.container_id=? AND turns.state IN ?", row.UserID, row.ContainerID, []string{"running", "awaiting_approval", "cancelling"}).Count(&active).Error; err != nil {
		failed(c, err)
		return
	}
	if active > 0 {
		lib.Error(c, 409, "ContainerBusy", "Answer or stop the active task before loading another session")
		return
	}
	key := events.Key{UserID: row.UserID, ContainerID: row.ContainerID}
	s.hub.Publish(key, events.Event{Type: "session.replay.started", SessionID: row.ID})
	if _, ok := s.rpc(c, conn, "session/load", gin.H{"sessionId": row.RemoteID, "cwd": row.Cwd, "mcpServers": []any{}}); !ok {
		return
	}
	if _, ok := s.rpc(c, conn, "session/set_mode", gin.H{"sessionId": row.RemoteID, "modeId": row.ModeID}); !ok {
		return
	}
	s.runtimeMu.Lock()
	s.loaded[row.ID] = conn
	s.runtimeMu.Unlock()
	s.hub.Publish(key, events.Event{Type: "session.replay.completed", SessionID: row.ID})
	lib.OK(c, gin.H{"sessionId": row.ID, "loaded": true})
}
func (s *Service) setSessionModel(c *gin.Context) { s.setSession(c, "session/set_model", "modelId") }
func (s *Service) setSessionMode(c *gin.Context)  { s.setSession(c, "session/set_mode", "modeId") }
func (s *Service) setSession(c *gin.Context, method, field string) {
	var req map[string]string
	if !decode(c, &req) {
		return
	}
	if len(req) != 1 || req[field] == "" {
		lib.Error(c, 422, "ValidationFailed", "Invalid session setting")
		return
	}
	if field == "modeId" && req[field] != "always-ask" && req[field] != "llm-approve" && req[field] != "always-approve" {
		lib.Error(c, 403, "PermissionDenied", "This permission mode is not allowed")
		return
	}
	s.access.RLock()
	defer s.access.RUnlock()
	row, conn, ok := s.session(c, c.Param("sessionId"))
	if !ok {
		return
	}
	if s.hasActiveTurn(row.ID) {
		lib.Error(c, 409, "TurnAlreadyRunning", "Session has an active turn")
		return
	}
	s.runtimeMu.Lock()
	loaded := s.loaded[row.ID] == conn
	s.runtimeMu.Unlock()
	if !loaded {
		lib.Error(c, 409, "SessionNotLoaded", "Load the session before changing settings")
		return
	}
	if _, ok := s.rpc(c, conn, method, gin.H{"sessionId": row.RemoteID, field: req[field]}); !ok {
		return
	}
	if field == "modelId" {
		_ = s.Repo.DB.WithContext(c.Request.Context()).Model(&row).Update("model_id", req[field]).Error
	}
	if field == "modeId" {
		if err := s.Repo.DB.WithContext(c.Request.Context()).Model(&row).Update("mode_id", req[field]).Error; err != nil {
			failed(c, err)
			return
		}
	}
	lib.OK(c, gin.H{"sessionId": row.ID, field: req[field]})
}
func (s *Service) hasActiveTurn(id string) bool {
	var n int64
	e := s.Repo.DB.Model(&storage.Turn{}).Where("session_id=? AND state IN ?", id, []string{"running", "awaiting_approval", "cancelling"}).Count(&n).Error
	return e != nil || n > 0
}
func (s *Service) prompt(c *gin.Context) {
	var req struct {
		ClientRequestID string `json:"clientRequestId"`
		Content         []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		CollaborationMode string `json:"collaborationMode"`
	}
	if !decode(c, &req) {
		return
	}
	if req.ClientRequestID == "" || len(req.ClientRequestID) > 128 || len(req.Content) == 0 || len(req.Content) > 32 {
		lib.Error(c, 422, "ValidationFailed", "clientRequestId and text content are required")
		return
	}
	size := 0
	for _, part := range req.Content {
		size += len(part.Text)
		if part.Type != "text" || part.Text == "" {
			lib.Error(c, 422, "ValidationFailed", "Only nonempty text content is supported")
			return
		}
	}
	if size > 256<<10 {
		lib.Error(c, 422, "ValidationFailed", "Prompt is too large")
		return
	}
	if req.CollaborationMode != "" && req.CollaborationMode != "default" && req.CollaborationMode != "plan" {
		lib.Error(c, 422, "ValidationFailed", "Invalid collaborationMode")
		return
	}
	s.access.RLock()
	defer s.access.RUnlock()
	row, conn, ok := s.session(c, c.Param("sessionId"))
	if !ok {
		return
	}
	b, _ := json.Marshal(req)
	hash := sha256.Sum256(b)
	fingerprint := hex.EncodeToString(hash[:])
	var old storage.Turn
	if e := s.Repo.DB.WithContext(c.Request.Context()).First(&old, "session_id=? AND client_request_id=?", row.ID, req.ClientRequestID).Error; e == nil {
		if old.PayloadHash != fingerprint {
			lib.Error(c, 409, "IdempotencyConflict", "clientRequestId payload differs")
			return
		}
		lib.OK(c, old)
		return
	}
	s.runtimeMu.Lock()
	loaded := s.loaded[row.ID] == conn
	s.runtimeMu.Unlock()
	if !loaded {
		lib.Error(c, 409, "SessionNotLoaded", "Load the session before sending a message")
		return
	}
	if req.CollaborationMode == "plan" && row.ModelID == "" {
		lib.Error(c, 422, "ValidationFailed", "Select a model before Plan")
		return
	}
	turn := storage.Turn{ID: uuid.NewString(), SessionID: row.ID, UserID: row.UserID, ClientRequestID: req.ClientRequestID, PayloadHash: fingerprint, State: "running"}
	if e := s.Repo.DB.WithContext(c.Request.Context()).Create(&turn).Error; e != nil {
		lib.Error(c, 409, "TurnAlreadyRunning", "Session has an active or duplicate turn")
		return
	}
	meta := gin.H{"request_id": turn.ID}
	if req.CollaborationMode == "plan" {
		meta["collaborationMode"] = gin.H{"mode": "plan", "settings": gin.H{"model": row.ModelID}}
	}
	params := gin.H{"sessionId": row.RemoteID, "prompt": req.Content, "_meta": meta}
	s.wg.Add(1)
	go func() { defer s.wg.Done(); s.runTurn(conn, row, turn, params) }()
	respond(c, 202, turn)
}
func (s *Service) runTurn(conn *agentconn.Connection, session storage.Session, turn storage.Turn, params any) {
	key := events.Key{UserID: session.UserID, ContainerID: session.ContainerID}
	s.hub.Publish(key, events.Event{Type: "turn.started", SessionID: session.ID, TurnID: turn.ID})
	result, e := conn.Call(s.ctx, "session/prompt", params)
	state, reason := "completed", ""
	if e != nil {
		state = "unknown"
		var rpc *agentconn.RPCError
		if errors.As(e, &rpc) {
			state = "failed"
		}
	} else {
		var body struct {
			StopReason string `json:"stopReason"`
		}
		if json.Unmarshal(result, &body) != nil {
			state = "unknown"
		} else {
			reason = body.StopReason
			if reason == "cancelled" {
				state = "cancelled"
			}
		}
	}
	// Cleanup is bounded and independent from a cancelled application context.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Repo.DB.WithContext(ctx).Model(&turn).Updates(map[string]any{"state": state, "stop_reason": reason, "finished_at": time.Now().UnixMilli()}).Error; err != nil {
		state = "unknown"
	}
	s.runtimeMu.Lock()
	for id, a := range s.approvals {
		if a.turn == turn.ID {
			_ = a.conn.Reply(a.rpcID, cancelledKind(a.kind))
			delete(s.approvals, id)
			close(a.done)
		}
	}
	s.runtimeMu.Unlock()
	s.hub.Publish(key, events.Event{Type: "turn." + state, SessionID: session.ID, TurnID: turn.ID, Payload: gin.H{"state": state, "stopReason": reason}})
}
func (s *Service) getTurn(c *gin.Context) {
	var t storage.Turn
	if s.Repo.DB.WithContext(c.Request.Context()).First(&t, "id=? AND user_id=?", c.Param("turnId"), principal(c).UserID).Error != nil {
		failed(c, storage.ErrNotFound)
		return
	}
	var session storage.Session
	if s.Repo.DB.WithContext(c.Request.Context()).First(&session, "id=?", t.SessionID).Error != nil {
		failed(c, storage.ErrNotFound)
		return
	}
	if _, _, e := s.ContainerFor(c.Request.Context(), principal(c), session.ContainerID); e != nil {
		failed(c, e)
		return
	}
	lib.OK(c, t)
}
func (s *Service) cancelTurn(c *gin.Context) {
	var turn storage.Turn
	if s.Repo.DB.WithContext(c.Request.Context()).First(&turn, "id=? AND user_id=?", c.Param("turnId"), principal(c).UserID).Error != nil {
		failed(c, storage.ErrNotFound)
		return
	}
	s.access.RLock()
	defer s.access.RUnlock()
	row, conn, ok := s.session(c, turn.SessionID)
	if !ok {
		return
	}
	s.runtimeMu.Lock()
	same := s.loaded[row.ID] == conn
	s.runtimeMu.Unlock()
	if !same {
		lib.Error(c, 409, "TurnOutcomeUnknown", "Original connection was lost")
		return
	}
	r := s.Repo.DB.WithContext(c.Request.Context()).Model(&turn).Where("state IN ?", []string{"running", "awaiting_approval"}).Update("state", "cancelling")
	if r.Error != nil {
		failed(c, r.Error)
		return
	}
	if r.RowsAffected == 0 {
		lib.Error(c, 409, "TurnNotActive", "Turn is no longer active")
		return
	}
	if e := conn.Notify(c.Request.Context(), "session/cancel", gin.H{"sessionId": row.RemoteID}); e != nil {
		lib.Error(c, 502, "RemoteError", "Cancellation outcome is unknown")
		return
	}
	respond(c, 202, gin.H{"turnId": turn.ID, "state": "cancelling"})
}
func (s *Service) replyApproval(c *gin.Context) {
	var req struct {
		OptionID string `json:"optionId"`
	}
	if !decode(c, &req) {
		return
	}
	s.respondInteraction(c, c.Param("approvalId"), gin.H{"outcome": map[string]any{"outcome": "selected", "optionId": req.OptionID}}, "permission")
}
func (s *Service) listFiles(c *gin.Context) {
	s.access.RLock()
	defer s.access.RUnlock()
	p := principal(c)
	container, _, e := s.ContainerFor(c.Request.Context(), p, c.Param("containerId"))
	if e != nil {
		failed(c, e)
		return
	}
	var roots []string
	_ = json.Unmarshal([]byte(container.WorkspaceRootsJSON), &roots)
	if len(roots) == 0 {
		lib.Error(c, 422, "ValidationFailed", "Container has no workspace roots")
		return
	}
	relative := c.DefaultQuery("path", ".")
	if path.IsAbs(relative) || strings.ContainsRune(relative, 0) || relative == ".." || strings.HasPrefix(path.Clean(relative), "../") {
		lib.Error(c, 422, "ValidationFailed", "A relative workspace path is required")
		return
	}
	target := path.Join(roots[0], relative)
	conn, e := s.connection(c.Request.Context(), p, container.ID)
	if e != nil {
		lib.Error(c, 503, "ContainerUnavailable", "Container is unavailable")
		return
	}
	raw, ok := s.rpc(c, conn, "_fs/readDirectory", gin.H{"path": target})
	if !ok {
		return
	}
	var body any
	if json.Unmarshal(raw, &body) != nil {
		lib.Error(c, 502, "RemoteProtocolError", "Invalid directory response")
		return
	}
	lib.OK(c, body)
}
func (s *Service) readFile(c *gin.Context) {
	s.access.RLock()
	defer s.access.RUnlock()
	p := principal(c)
	container, _, err := s.ContainerFor(c.Request.Context(), p, c.Param("containerId"))
	if err != nil {
		failed(c, err)
		return
	}
	filePath := c.Query("path")
	var roots []string
	_ = json.Unmarshal([]byte(container.WorkspaceRootsJSON), &roots)
	if filePath == "" || !allowedPath(filePath, roots) {
		lib.Error(c, 422, "ValidationFailed", "File must be inside an assigned workspace root")
		return
	}
	conn, err := s.connection(c.Request.Context(), p, container.ID)
	if err != nil {
		lib.Error(c, 503, "ContainerUnavailable", "Container is unavailable")
		return
	}
	root := ""
	for _, candidate := range roots {
		if allowedPath(filePath, []string{candidate}) {
			root = candidate
			break
		}
	}
	raw, err := conn.Call(c.Request.Context(), "_fs/readFile", gin.H{"path": filePath, "root": root})
	if err != nil {
		var rpc *agentconn.RPCError
		if errors.As(err, &rpc) && rpc.Code == -32601 {
			lib.Error(c, 501, "CapabilityUnsupported", "Container does not support file preview")
		} else {
			lib.Error(c, 502, "RemoteError", "Container file read failed")
		}
		return
	}
	var body struct {
		Path      string `json:"path"`
		Type      string `json:"type,omitempty"`
		Entries   []any  `json:"entries,omitempty"`
		Content   string `json:"content"`
		Size      int64  `json:"size"`
		Truncated bool   `json:"truncated"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Path != filePath {
		lib.Error(c, 502, "RemoteProtocolError", "Invalid file response")
		return
	}
	if body.Size < 0 || len(body.Content) > 1<<20 {
		lib.Error(c, 413, "FileTooLarge", "File exceeds the preview limit")
		return
	}
	if body.Type == "directory" {
		if body.Entries == nil {
			body.Entries = []any{}
		}
		lib.OK(c, gin.H{"path": body.Path, "type": "directory", "entries": body.Entries})
		return
	}
	lib.OK(c, body)
}
func (s *Service) wsTicket(c *gin.Context) {
	var req struct {
		ContainerID string `json:"containerId"`
	}
	if !decode(c, &req) {
		return
	}
	p := principal(c)
	if _, _, e := s.ContainerFor(c.Request.Context(), p, req.ContainerID); e != nil {
		failed(c, e)
		return
	}
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	now := time.Now()
	for id, t := range s.tickets {
		if now.After(t.expires) {
			delete(s.tickets, id)
		}
	}
	if len(s.tickets) >= 1000 {
		lib.Error(c, 429, "RateLimited", "Too many pending connections")
		return
	}
	raw := uuid.NewString() + uuid.NewString()
	hash := sha256.Sum256([]byte(raw))
	s.tickets[hex.EncodeToString(hash[:])] = ticket{p, req.ContainerID, now.Add(30 * time.Second)}
	lib.OK(c, gin.H{"ticket": raw, "expiresIn": 30})
}
func (s *Service) eventSocket(c *gin.Context) {
	raw := c.Query("ticket")
	if len(raw) > 128 {
		lib.Error(c, 401, "InvalidTicket", "Invalid connection ticket")
		return
	}
	hash := sha256.Sum256([]byte(raw))
	key := hex.EncodeToString(hash[:])
	s.runtimeMu.Lock()
	t, ok := s.tickets[key]
	delete(s.tickets, key)
	s.runtimeMu.Unlock()
	if !ok || time.Now().After(t.expires) {
		lib.Error(c, 401, "InvalidTicket", "Invalid connection ticket")
		return
	}
	if _, _, e := s.ContainerFor(c.Request.Context(), t.principal, t.container); e != nil {
		lib.Error(c, 401, "InvalidTicket", "Connection authorization expired")
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, e := url.Parse(origin)
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		return e == nil && u.Host == r.Host && u.Scheme == scheme
	}}
	socket, e := upgrader.Upgrade(c.Writer, c.Request, nil)
	if e != nil {
		return
	}
	defer socket.Close()
	socket.SetReadLimit(4096)
	_ = socket.SetReadDeadline(time.Now().Add(60 * time.Second))
	socket.SetPongHandler(func(string) error { return socket.SetReadDeadline(time.Now().Add(60 * time.Second)) })
	subscriberKey := events.Key{UserID: t.principal.UserID, ContainerID: t.container}
	sub := s.hub.Subscribe(subscriberKey)
	defer s.hub.Unsubscribe(subscriberKey, sub)
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			if _, _, err := socket.ReadMessage(); err != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	_ = socket.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if socket.WriteJSON(gin.H{"type": "connection.ready", "containerId": t.container, "requiresLoad": true}) != nil {
		return
	}
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-sub.Done:
			return
		case <-closed:
			return
		case <-ticker.C:
			if _, _, e := s.ContainerFor(s.ctx, t.principal, t.container); e != nil {
				return
			}
		case <-ping.C:
			if socket.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
				return
			}
		case event := <-sub.Events:
			// Check SQL auth immediately before each delivery, including cached metadata paths.
			if _, _, e := s.ContainerFor(s.ctx, t.principal, t.container); e != nil {
				return
			}
			_ = socket.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if socket.WriteJSON(event) != nil {
				return
			}
		}
	}
}
