package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"code.cestc.cn/ai-socket/server-go/constants"
	"code.cestc.cn/ai-socket/server-go/errors"
)

func Abort(c *gin.Context, err error) {
	if errDetail, ok := err.(*errors.Error); ok {
		c.Status(errDetail.Status)
		c.Abort()
		_ = c.Error(errDetail)
		return
	}

	c.Status(http.StatusInternalServerError)
	c.Abort()
	_ = c.Error(err)
}

func OK(c *gin.Context, resp any) {
	if resp == nil {
		c.Data(http.StatusOK, constants.ContentTypeJSON, nil)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// OKN means message delivered in Dapr
func OKN(c *gin.Context) {
	c.Data(http.StatusNoContent, constants.ContentTypeJSON, nil)
}

// Forbidden means message forbidden by access controls in Dapr
func Forbidden(c *gin.Context) {
	c.Data(http.StatusForbidden, constants.ContentTypeJSON, nil)
}

// NotFound means no pubsub name or topic give in Dapr
func NotFound(c *gin.Context) {
	c.Data(http.StatusNotFound, constants.ContentTypeJSON, nil)
}

// InternalError means Delivery failed in Dapr
func InternalError(c *gin.Context) {
	c.Data(http.StatusInternalServerError, constants.ContentTypeJSON, nil)
}
