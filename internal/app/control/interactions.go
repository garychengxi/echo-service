package control

import (
	"encoding/json"
	"time"

	"echo-service/internal/agentconn"
	"echo-service/internal/events"
	"echo-service/internal/lib"
	"echo-service/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func cloneJSON(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var copy any
	if json.Unmarshal(raw, &copy) != nil {
		return nil
	}
	return copy
}

// Only explicitly supported client requests cross the authenticated gateway.
func isInteractionRequest(method string) bool {
	return method == "session/request_permission" || method == "_codeflow/ask_user_question" || method == "codeflow/ask_user_question"
}
func cancelledKind(kind string) gin.H {
	if kind == "permission" {
		return gin.H{"outcome": gin.H{"outcome": "cancelled"}}
	}
	return gin.H{"outcome": "cancelled"}
}
func cancelledInteraction(method string) gin.H {
	if method == "session/request_permission" {
		return cancelledKind("permission")
	}
	return cancelledKind("ask")
}
func interactionPayload(id string, a *approval) gin.H {
	return gin.H{"interactionId": id, "kind": a.kind, "params": redact(cloneJSON(a.params)), "expiresAt": a.expires.UTC().Format(time.RFC3339)}
}
func (s *Service) requestInteraction(conn *agentconn.Connection, msg agentconn.Message, params map[string]any, session storage.Session, turn storage.Turn, user, container string) {
	kind := "ask"
	ttl := 30 * time.Minute
	options := map[string]bool{}
	if msg.Method == "session/request_permission" {
		kind, ttl = "permission", 5*time.Minute
		raw, _ := params["options"].([]any)
		for _, option := range raw {
			if o, ok := option.(map[string]any); ok {
				if id, ok := o["optionId"].(string); ok && id != "" {
					options[id] = true
				}
			}
		}
	}
	questions, _ := params["questions"].([]any)
	tool, _ := params["toolCallId"].(string)
	if turn.ID == "" || turn.State != "running" && turn.State != "awaiting_approval" || kind == "permission" && len(options) == 0 || kind == "ask" && (len(questions) == 0 || tool == "") {
		_ = conn.Reply(msg.ID, cancelledKind(kind))
		return
	}
	id := uuid.NewString()
	// Do not expose upstream session identifiers or transport metadata.
	clean := map[string]any{}
	for _, key := range []string{"toolCall", "options", "toolCallId", "mode", "questions"} {
		if v, ok := params[key]; ok {
			clean[key] = cloneJSON(v)
		}
	}
	a := &approval{done: make(chan struct{}), conn: conn, rpcID: msg.ID, user: user, container: container, session: session.ID, turn: turn.ID, kind: kind, params: clean, options: options, expires: time.Now().Add(ttl)}
	s.runtimeMu.Lock()
	if len(s.approvals) >= 4096 {
		s.runtimeMu.Unlock()
		_ = conn.Reply(msg.ID, cancelledKind(kind))
		return
	}
	s.approvals[id] = a
	s.runtimeMu.Unlock()
	// Keep the existing active-turn state for compatibility; kind identifies the UI.
	_ = s.Repo.DB.Model(&turn).Where("state = ?", "running").Update("state", "awaiting_approval").Error
	key := events.Key{UserID: user, ContainerID: container}
	s.hub.Publish(key, events.Event{Type: "interaction.requested", SessionID: session.ID, TurnID: turn.ID, Payload: interactionPayload(id, a)})
	if kind == "permission" {
		s.hub.Publish(key, events.Event{Type: "approval.requested", SessionID: session.ID, TurnID: turn.ID, Payload: gin.H{"approvalId": id, "options": redact(cloneJSON(clean["options"])), "toolCall": redact(cloneJSON(clean["toolCall"])), "expiresAt": a.expires.UTC().Format(time.RFC3339)}})
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		timer := time.NewTimer(time.Until(a.expires))
		defer timer.Stop()
		select {
		case <-timer.C:
			s.runtimeMu.Lock()
			pending := s.approvals[id]
			delete(s.approvals, id)
			s.runtimeMu.Unlock()
			if pending != nil {
				_ = conn.Reply(a.rpcID, cancelledKind(kind))
				s.finishInteraction(id, a, "expired")
			}
		case <-a.done:
		case <-conn.Done():
		case <-s.ctx.Done():
		}
	}()
}
func (s *Service) listInteractions(c *gin.Context) {
	p := principal(c)
	container := c.Param("containerId")
	if _, _, err := s.ContainerFor(c.Request.Context(), p, container); err != nil {
		failed(c, err)
		return
	}
	items := []gin.H{}
	s.runtimeMu.Lock()
	for id, a := range s.approvals {
		if a.user == p.UserID && a.container == container && time.Now().Before(a.expires) {
			item := interactionPayload(id, a)
			item["sessionId"] = a.session
			item["turnId"] = a.turn
			items = append(items, item)
		}
	}
	s.runtimeMu.Unlock()
	lib.OK(c, gin.H{"items": items})
}
func (s *Service) replyInteraction(c *gin.Context) {
	var req struct {
		Response map[string]any `json:"response"`
	}
	if !decode(c, &req) {
		return
	}
	s.respondInteraction(c, c.Param("interactionId"), req.Response, "")
}
func validInteractionResponse(a *approval, r map[string]any) bool {
	if r == nil {
		return false
	}
	if a.kind == "permission" {
		o, ok := r["outcome"].(map[string]any)
		if !ok {
			return false
		}
		if o["outcome"] == "cancelled" {
			return true
		}
		id, _ := o["optionId"].(string)
		return o["outcome"] == "selected" && a.options[id]
	}
	outcome, _ := r["outcome"].(string)
	switch outcome {
	case "cancelled":
		return true
	case "accepted":
		answers, ok := r["answers"].(map[string]any)
		if !ok {
			return false
		}
		for _, v := range answers {
			switch v := v.(type) {
			case string:
			case []any:
				for _, s := range v {
					if _, ok := s.(string); !ok {
						return false
					}
				}
			default:
				return false
			}
		}
		if v, ok := r["annotations"]; ok {
			var out map[string]struct {
				Notes   *string `json:"notes"`
				Preview *string `json:"preview"`
			}
			b, _ := json.Marshal(v)
			if json.Unmarshal(b, &out) != nil {
				return false
			}
		}
		return true
	case "chat_about_this", "skip_interview":
		if v, ok := r["partial_answers"]; ok {
			obj, ok := v.(map[string]any)
			if !ok {
				return false
			}
			for _, v := range obj {
				if _, ok := v.(string); !ok {
					return false
				}
			}
		}
		return true
	}
	return false
}
func (s *Service) respondInteraction(c *gin.Context, id string, response map[string]any, requiredKind string) {
	s.access.RLock()
	defer s.access.RUnlock()
	p := principal(c)
	s.runtimeMu.Lock()
	a := s.approvals[id]
	s.runtimeMu.Unlock()
	if a == nil || a.user != p.UserID || a.turn != c.Param("turnId") || requiredKind != "" && a.kind != requiredKind {
		lib.Error(c, 409, "InteractionExpired", "Interaction is no longer pending")
		return
	}
	if _, _, err := s.ContainerFor(c.Request.Context(), p, a.container); err != nil {
		failed(c, err)
		return
	}
	if time.Now().After(a.expires) || !validInteractionResponse(a, response) {
		lib.Error(c, 422, "InvalidInteractionResponse", "Invalid or expired interaction response")
		return
	}
	s.runtimeMu.Lock()
	if s.approvals[id] != a {
		s.runtimeMu.Unlock()
		lib.Error(c, 409, "InteractionExpired", "Interaction already resolved")
		return
	}
	delete(s.approvals, id)
	close(a.done)
	s.runtimeMu.Unlock()
	if err := a.conn.Reply(a.rpcID, response); err != nil {
		lib.Error(c, 409, "InteractionExpired", "Original connection is unavailable")
		return
	}
	s.finishInteraction(id, a, "answered")
	_ = s.Repo.Audit(c.Request.Context(), p.UserID, "interaction.response", a.turn, "success")
	c.Status(204)
}
func (s *Service) finishInteraction(id string, a *approval, reason string) {
	s.runtimeMu.Lock()
	remaining := false
	for _, other := range s.approvals {
		if other.turn == a.turn {
			remaining = true
			break
		}
	}
	s.runtimeMu.Unlock()
	if !remaining {
		_ = s.Repo.DB.Model(&storage.Turn{}).Where("id=? AND state=?", a.turn, "awaiting_approval").Update("state", "running").Error
	}
	key := events.Key{UserID: a.user, ContainerID: a.container}
	s.hub.Publish(key, events.Event{Type: "interaction.resolved", SessionID: a.session, TurnID: a.turn, Payload: gin.H{"interactionId": id, "reason": reason}})
	if a.kind == "permission" {
		s.hub.Publish(key, events.Event{Type: "approval.resolved", SessionID: a.session, TurnID: a.turn, Payload: gin.H{"approvalId": id}})
	}
}
