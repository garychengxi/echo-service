package auth

import (
	"github.com/gin-gonic/gin"

	"code.cestc.cn/ai-socket/server-go/constants"
)

// Impersonate
// 效果类似STS鉴权方式，提供上游服务代理用户调用下游服务的能力。
// 限于服务内部模块间调用，调用方通过header将需要鉴权通过的orgID和userID传给下游服务。
// 调用方仍然用常规的AKSK鉴权方式发起调用，例如用服务/资源账号，同时通过header指定要代表的租户。
// 被调用方收到请求，检查调用账号是一个服务账号，将impersonate header指定的租户账号设置为鉴权账号。
func Impersonate() gin.HandlerFunc {
	return func(c *gin.Context) {
		orgID := c.GetString(constants.KeyOrgID)
		userID := c.GetString(constants.KeyUserID)
		if orgID != "" && userID != "" {
			c.Next()
			return
		}

		if val := c.Request.Header.Get(constants.HeaderImpersonateOrgID); val != "" {
			c.Set(constants.KeyImpersonateOriginOrgID, val)
			c.Set(constants.KeyOrgID, val)
		}

		if val := c.Request.Header.Get(constants.HeaderImpersonateUserID); val != "" {
			c.Set(constants.KeyImpersonateOriginUserID, val)
			c.Set(constants.KeyUserID, val)
		}

		orgID = c.GetString(constants.KeyOrgID)
		userID = c.GetString(constants.KeyUserID)
		if orgID != "" && userID != "" {
			c.Set(constants.KeyAuthMode, "Impersonate")
		}

		c.Next()
	}
}
