package auth

import (
	core "code.cestc.cn/ai-socket/server-go/pkg"
	"github.com/gin-gonic/gin"

	"code.cestc.cn/ai-socket/server-go/conf"
	"code.cestc.cn/ai-socket/server-go/constants"
)

func Use(engine *core.Engine, config *conf.Config) {
	engine.Use(Impersonate())

	// 优先级：Fake > AuthCenter
	switch config.HTTPServer.AuthType {
	case conf.AuthTypeFake:
		engine.Use(FakeAuthWithID(
			config.FakeAuth.OrgID,
			config.FakeAuth.UserID,
			config.FakeAuth.UserName,
			config.FakeAuth.UserRole))
	case conf.AuthTypeAuthCenter:
		engine.Use(AuthWithAuthCenter())
	}
}

func FakeAuth() gin.HandlerFunc {
	return FakeAuthWithID("", "", "", "")
}

func FakeAuthWithID(orgID, userID, userName, userRole string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if orgID == "" {
			orgID = "test-org-id"
		}
		c.Set(constants.KeyOrgID, orgID)

		if userID == "" {
			userID = "test-user-id"
		}
		c.Set(constants.KeyUserID, userID)

		if userName == "" {
			userName = "test-user-name"
		}
		c.Set(constants.KeyUserName, userName)

		if userRole == "" {
			userRole = "test-user-role"
		}
		c.Set(constants.KeyUserRole, userRole)

		c.Next()
	}
}

func AuthWithAuthCenter() gin.HandlerFunc {
	return func(c *gin.Context) {
		orgID := c.GetHeader(constants.HeaderOrgID)
		c.Set(constants.KeyOrgID, orgID)

		userID := c.GetHeader(constants.HeaderUserID)
		c.Set(constants.KeyUserID, userID)

		c.Next()
	}
}
