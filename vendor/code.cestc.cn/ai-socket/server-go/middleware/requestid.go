package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"code.cestc.cn/ai-socket/server-go/constants"
)

func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		_ = GetRequestID(c)
		c.Next()
	}
}

func GetRequestID(c *gin.Context) string {
	requestID := c.GetHeader(constants.HeaderRequestID)
	if requestID == "" {
		requestID = uuid.NewString()
		c.Request.Header.Add(constants.HeaderRequestID, requestID)
	}

	c.Set(constants.KeyRequestID, requestID)
	c.Set(constants.HeaderRequestID, requestID)
	return requestID
}
