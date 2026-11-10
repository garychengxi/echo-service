package control

import (
	"encoding/json"
	"time"

	"echo-service/internal/lib"
	"echo-service/internal/table"
	"github.com/gin-gonic/gin"
)

func (s *Service) authSessions(c *gin.Context) {
	p := principal(c)
	limit, offset := pagination(c)
	var rows []table.AuthSession
	if err := s.Repo.DB.WithContext(c.Request.Context()).Where("user_id=? AND revoked_at IS NULL AND expires_at>?", p.UserID, time.Now().UnixMilli()).Order("created_at DESC,id").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		failed(c, err)
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		items = append(items, gin.H{"id": r.ID, "deviceName": r.DeviceName, "createdAt": r.CreatedAt, "expiresAt": r.ExpiresAt, "current": r.ID == p.SessionID})
	}
	lib.OK(c, gin.H{"items": items, "limit": limit, "offset": offset})
}

func (s *Service) revokeAuthSession(c *gin.Context) {
	s.access.Lock()
	defer s.access.Unlock()
	p := principal(c)
	if err := s.Repo.DB.WithContext(c.Request.Context()).Model(&table.AuthSession{}).Where("id=? AND user_id=?", c.Param("authSessionId"), p.UserID).Update("revoked_at", time.Now().UnixMilli()).Error; err != nil {
		failed(c, err)
		return
	}
	// Upstream connections are user-scoped, so conservatively close this user's
	// transport; still-valid devices can explicitly reconnect and load.
	s.invalidate(p.UserID, "")
	c.Status(204)
}

func (s *Service) skills(c *gin.Context) {
	scope := c.DefaultQuery("scope", "user")
	if scope != "user" && scope != "project" && scope != "enterprise" && scope != "all" {
		lib.Error(c, 422, "ValidationFailed", "Invalid skill scope")
		return
	}
	params := map[string]any{"scope": scope}
	if id := c.Query("projectId"); id != "" {
		var project struct{ RootsJSON string }
		if err := s.Repo.DB.WithContext(c.Request.Context()).Table("projects").Select("roots_json").Where("id=? AND user_id=? AND container_id=?", id, principal(c).UserID, c.Param("containerId")).Take(&project).Error; err != nil {
			lib.Error(c, 404, "ResourceNotFound", "Project not found")
			return
		}
		var roots []struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(project.RootsJSON), &roots) != nil || len(roots) == 0 {
			lib.Error(c, 422, "ValidationFailed", "Project roots are unavailable")
			return
		}
		params["cwd"] = roots[0].Path
	} else if scope == "project" {
		lib.Error(c, 422, "ValidationFailed", "projectId is required for project skills")
		return
	}
	s.agentRead("_list_skills", params)(c)
}
