package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"echo-service/internal/lib"
	"echo-service/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func (s *Service) reserveOperation(c *gin.Context, container, kind string, payload any) (string, bool) {
	key := c.GetHeader("Idempotency-Key")
	if key == "" || len(key) > 128 {
		lib.Error(c, 422, "ValidationFailed", "Idempotency-Key is required")
		return "", false
	}
	p := principal(c)
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(p.UserID+":"+container+":"+kind+":"+key)).String()
	b, _ := json.Marshal(payload)
	h := sha256.Sum256(b)
	fingerprint := hex.EncodeToString(h[:])
	var old storage.Operation
	err := s.Repo.DB.WithContext(c.Request.Context()).First(&old, "id=?", id).Error
	if err == nil {
		if old.Fingerprint != fingerprint {
			lib.Error(c, 409, "IdempotencyConflict", "Idempotency-Key payload differs")
			return "", false
		}
		if old.ResourceID == "" {
			lib.Error(c, 409, "OperationOutcomeUnknown", "Operation was already submitted; reconcile before retrying")
			return "", false
		}
		var row storage.Session
		if s.Repo.DB.WithContext(c.Request.Context()).First(&row, "id=? AND user_id=?", old.ResourceID, p.UserID).Error != nil {
			failed(c, storage.ErrNotFound)
			return "", false
		}
		lib.OK(c, row)
		return "", false
	}
	if err != gorm.ErrRecordNotFound {
		failed(c, err)
		return "", false
	}
	if err := s.Repo.DB.WithContext(c.Request.Context()).Create(&storage.Operation{ID: id, UserID: p.UserID, ContainerID: container, Kind: kind, Fingerprint: fingerprint}).Error; err != nil {
		failed(c, storage.ErrConflict)
		return "", false
	}
	return id, true
}

func (s *Service) saveSession(ctx context.Context, row *storage.Session, operation string) error {
	return s.Repo.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		return tx.Model(&storage.Operation{}).Where("id=?", operation).Update("resource_id", row.ID).Error
	})
}

func (s *Service) updateProject(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
	}
	if !decode(c, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" || len(req.Name) > 128 {
		lib.Error(c, 422, "ValidationFailed", "A project name of at most 128 bytes is required")
		return
	}
	s.access.RLock()
	defer s.access.RUnlock()
	row, conn, ok := s.project(c)
	if !ok {
		return
	}
	if _, ok := s.rpc(c, conn, "_project/update", gin.H{"projectId": row.RemoteID, "name": req.Name}); !ok {
		return
	}
	if err := s.Repo.DB.WithContext(c.Request.Context()).Model(&row).Update("name", req.Name).Error; err != nil {
		failed(c, err)
		return
	}
	lib.OK(c, projectDTO(row))
}

func (s *Service) renameSession(c *gin.Context) {
	var req struct {
		Title string `json:"title"`
	}
	if !decode(c, &req) {
		return
	}
	if strings.TrimSpace(req.Title) == "" || len(req.Title) > 80 {
		lib.Error(c, 422, "ValidationFailed", "Title must be 1..80 UTF-8 bytes")
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
	if _, ok := s.rpc(c, conn, "_rename_session", gin.H{"sessionId": row.RemoteID, "title": req.Title}); !ok {
		return
	}
	if err := s.Repo.DB.WithContext(c.Request.Context()).Model(&row).Update("title", req.Title).Error; err != nil {
		failed(c, err)
		return
	}
	lib.OK(c, row)
}

func (s *Service) deleteSession(c *gin.Context) {
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
	if _, ok := s.rpc(c, conn, "_delete_session", gin.H{"sessionId": row.RemoteID}); !ok {
		return
	}
	err := s.Repo.DB.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("session_id=?", row.ID).Delete(&storage.Turn{}).Error; err != nil {
			return err
		}
		return tx.Delete(&row).Error
	})
	if err != nil {
		failed(c, err)
		return
	}
	s.runtimeMu.Lock()
	delete(s.loaded, row.ID)
	s.runtimeMu.Unlock()
	c.Status(http.StatusNoContent)
}

func (s *Service) forkSession(c *gin.Context) {
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
	params := gin.H{"sessionId": row.RemoteID, "cwd": row.Cwd, "mcpServers": []any{}}
	op, proceed := s.reserveOperation(c, row.ContainerID, "session.fork", params)
	if !proceed {
		return
	}
	raw, ok := s.rpc(c, conn, "session/fork", params)
	if !ok {
		return
	}
	var result struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(raw, &result) != nil || result.SessionID == "" {
		lib.Error(c, 502, "RemoteProtocolError", "Invalid fork response")
		return
	}
	row.ID = uuid.NewString()
	row.RemoteID = result.SessionID
	row.Title = "Fork"
	row.CreatedAt = 0
	if err := s.saveSession(c.Request.Context(), &row, op); err != nil {
		failed(c, err)
		return
	}
	// Fork results must be explicitly loaded before prompting.
	respond(c, 201, row)
}

// Only expose the session-default permission mode. Provider credentials and
// arbitrary runtime configuration are deliberately outside this API contract.
func (s *Service) agentSettings(c *gin.Context) {
	s.access.RLock()
	defer s.access.RUnlock()
	conn, err := s.connection(c.Request.Context(), principal(c), c.Param("containerId"))
	if err != nil {
		failed(c, storage.ErrNotFound)
		return
	}
	method := "_get_common_settings"
	params := gin.H{}
	if c.Request.Method == http.MethodPatch {
		var req struct {
			Mode string `json:"mode"`
		}
		if !decode(c, &req) {
			return
		}
		if req.Mode != "always-ask" && req.Mode != "llm-approve" {
			lib.Error(c, 422, "ValidationFailed", "Unsupported default permission mode")
			return
		}
		method = "_edit_common_settings"
		params["mode"] = req.Mode
	}
	raw, ok := s.rpc(c, conn, method, params)
	if !ok {
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if json.Unmarshal(raw, &body) != nil {
		lib.Error(c, 502, "RemoteProtocolError", "Invalid settings response")
		return
	}
	if body.Mode == "" {
		body.Mode = "llm-approve"
	}
	lib.OK(c, gin.H{"mode": body.Mode})
}
