package middleware

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"code.cestc.cn/ai-socket/server-go/errors"

	"github.com/gin-gonic/gin"

	"code.cestc.cn/ai-socket/logger-go"
	"code.cestc.cn/ai-socket/server-go/constants"
	"code.cestc.cn/ai-socket/server-go/idempotence"
	core "code.cestc.cn/ai-socket/server-go/pkg"
)

func Idempotence() gin.HandlerFunc {
	return func(c *gin.Context) {
		contentType := c.Request.Header.Get(constants.HeaderContentType)
		method := c.Request.Method
		clientToken, ok := c.GetQuery(constants.QueryClientToken)
		// 只有请求 content type 为 application/json 并且 query parameter参数中包含 clientToken 的 POST 请求才会进入幂等逻辑
		if ok && method == http.MethodPost && strings.Contains(contentType, constants.ContentTypeJSON) {
			idempotentController := idempotence.GetIdempotenceController()
			requestURL := c.Request.URL.String()
			var requestBody []byte
			requestBody, _ = c.GetRawData()
			requestID := c.GetHeader(constants.HeaderRequestID)
			ctx := logger.WithContext(c)
			if requestBody != nil {
				c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
				if len(requestBody) > constants.SizeMB {
					core.HTTPLogger.Warnf(ctx, "idempotence middleware receive request %s which body size larger than 1MB", requestID)
				}
			}
			if item := idempotentController.GetIdempotentItem(clientToken); item != nil {
				if idempotence.CalculateIdempotenceMD5(requestURL, requestBody) != item.MD5 {
					_ = c.AbortWithError(http.StatusForbidden, errors.ErrorIdempotentParameterMismatch)
					core.HTTPLogger.Errorf(ctx, "idempotence middleware receive request %s which body changed from last emit", requestID)
				} else {
					c.String(item.StatueCode, item.ResponseBuf.String())
					c.Abort()
				}
				return
			}
			var writer core.BufferedWriter
			// 缓存response，这里为了和log middleware 合并使用，需要封装为core.BufferedWriter，并且需要避免多次封装
			if w, ok := c.Writer.(core.BufferedWriter); !ok {
				bufferedWriter := core.BufferedWriter{
					Buf:            bytes.NewBufferString(""),
					ResponseWriter: c.Writer,
				}
				c.Writer = bufferedWriter
				writer = bufferedWriter
			} else {
				writer = w
			}

			c.Next()

			idempotenceItem := &idempotence.IdempotentItem{
				RequestURL:  requestURL,
				RequestBody: bytes.NewBuffer(requestBody),
				StatueCode:  c.Writer.Status(),
				ResponseBuf: writer.Buf,
				MD5:         idempotence.CalculateIdempotenceMD5(requestURL, requestBody),
			}
			idempotentController.SetIdempotentItem(clientToken, idempotenceItem)
		} else {
			c.Next()
		}
	}
}
