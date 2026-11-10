package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"code.cestc.cn/ai-socket/server-go/constants"
	core "code.cestc.cn/ai-socket/server-go/pkg"
	"github.com/gin-gonic/gin"
)

const (
	maxLength = 8192
)

func shouldLogBody(c *gin.Context) bool {
	// Usually chunked or compressed, not readable
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Transfer-Encoding
	transferEncoding := c.Request.Header.Get("Transfer-Encoding")
	if transferEncoding != "" {
		return false
	}

	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Range_requests
	hRange := c.Request.Header.Get("Range")
	if hRange != "" {
		return false
	}

	contentType := c.Request.Header.Get(constants.HeaderContentType)
	// SDK request header does not contain application/json
	if contentType == "" {
		return true
	}

	// API request body is json
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Basics_of_HTTP/MIME_types
	if strings.Contains(contentType, "application/json") {
		return true
	}

	// https://stackoverflow.com/questions/4007969/application-x-www-form-urlencoded-or-multipart-form-data
	if strings.Contains(contentType, "application/x-www-form-urlencoded") {
		return true
	}

	if strings.Contains(contentType, "text/plain") {
		return true
	}

	return false
}

func noLog(c *gin.Context, prefixFilters []string, suffixFilters []string) bool {
	for _, prefix := range prefixFilters {
		if strings.HasPrefix(c.Request.RequestURI, prefix) {
			return true
		}
	}
	for _, suffix := range suffixFilters {
		if strings.HasSuffix(c.Request.RequestURI, suffix) {
			return true
		}
	}
	return false
}

func Debug(prefixFilters []string, suffixFilters []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if noLog(c, prefixFilters, suffixFilters) {
			c.Next()
			return
		}

		var rawRequest []byte
		if shouldLogBody(c) {
			// 为了输出body，需要将body读取出来，然后再赋值回去
			rawRequest, _ = c.GetRawData()
			if rawRequest != nil {
				c.Request.Body = io.NopCloser(bytes.NewBuffer(rawRequest))
			}
		}

		var writer core.BufferedWriter
		// 为了输出response，需要将response缓存一份，这里为了和幂等 middleware 合并使用，需要封装为core.BufferedWriter，并且需要避免多次封装
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

		startTime := time.Now() // 请求开始时间

		c.Next()

		endTime := time.Now()

		reqHeader := header("> ", &c.Request.Header)
		var reqBody string
		if rawRequest != nil {
			var buf bytes.Buffer
			if err := json.Compact(&buf, rawRequest); err == nil {
				reqBody = buf.String()
			}
		}

		statusCode := c.Writer.Status()
		h := c.Writer.Header()
		respHeader := header("< ", &h)
		respBody := ""
		contentType := c.Writer.Header().Get("Content-Type")
		if strings.ToLower(contentType) != "application/zip" {
			respBody = strings.Trim(writer.Buf.String(), constants.LineBreak)
		}

		message := fmt.Sprintf("\nlatency: %v, [C]%s --> [S]%s  %s\n%s %s\n%s \n> %s\n=================\n%d %s\n%s\n< %s",
			endTime.Sub(startTime),
			c.ClientIP(),
			c.Request.Host,
			c.Request.Proto,
			c.Request.Method,
			c.Request.RequestURI,
			reqHeader,
			body(reqBody),
			statusCode,
			http.StatusText(statusCode),
			respHeader,
			body(respBody),
		)

		log(c, message, statusCode)
	}
}

func log(ctx context.Context, message string, statusCode int) {
	switch statusCode / 100 { // reduce 2xx status to 2
	case 1:
		core.HTTPLogger.Infof(ctx, message)
	case 2:
		core.HTTPLogger.Infof(ctx, message)
	case 3:
		core.HTTPLogger.Infof(ctx, message)
	case 4:
		core.HTTPLogger.Warnf(ctx, message)
	case 5:
		core.HTTPLogger.Errorf(ctx, message)
	default:
		core.HTTPLogger.Errorf(ctx, message)
	}
}

func body(body string) string {
	if len(body) > maxLength {
		body = body[:(maxLength - 1)]
	}
	return body
}

func header(direction string, h *http.Header) string {
	headers := make([]string, 0, len(*h))
	for k, vs := range *h {
		for _, v := range vs {
			headers = append(headers, direction+k+"="+v)
		}
	}
	return strings.Join(headers, "\n")
}
