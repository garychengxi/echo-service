package lib

import (
	"net/http"

	"code.cestc.cn/ai-socket/server-go/middleware"
	core "code.cestc.cn/ai-socket/server-go/pkg"
	sdk "echo-service/sdk/go/echoservicev1"
)

func OK(c *core.Context, data any) {
	c.JSON(http.StatusOK, sdk.Response[any]{Data: data, Error: sdk.Error{RequestID: middleware.GetRequestID(c)}})
}

// Error uses real HTTP status codes; internal errors must be mapped to public messages.
func Error(c *core.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, sdk.Response[any]{Error: sdk.Error{
		Code: code, RequestID: middleware.GetRequestID(c), Message: message, StatusCode: status,
	}})
}
