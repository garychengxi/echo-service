package middleware

import (
	"github.com/gin-gonic/gin"

	"code.cestc.cn/ai-socket/server-go"
)

func Feature(obj any) gin.HandlerFunc {
	return func(c *gin.Context) {
		server.OK(c, obj)
	}
}
