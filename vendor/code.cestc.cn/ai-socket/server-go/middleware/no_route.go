package middleware

import (
	"code.cestc.cn/ai-socket/server-go/errors"
	"github.com/gin-gonic/gin"
)

func APINotFound() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Abort()
		_ = c.Error(errors.ErrorAPINotFound)
	}
}
