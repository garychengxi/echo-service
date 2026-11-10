package console

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"time"

	"code.cestc.cn/ai-socket/logger-go"
	"github.com/gin-gonic/gin"
	"github.com/gookit/goutil/arrutil"

	"code.cestc.cn/ai-socket/server-go/conf"
	"code.cestc.cn/ai-socket/server-go/constants"
	serrors "code.cestc.cn/ai-socket/server-go/errors"
	core "code.cestc.cn/ai-socket/server-go/pkg"
)

func RegisterConsoleAPI(engine *core.Engine, config *conf.Config) {
	names := config.HTTPServer.ConsoleProxyNames
	if !arrutil.Contains(names, config.AppName) {
		names = append(names, config.AppName)
	}

	for _, name := range names {
		if name == "" {
			continue
		}
		// 传入 engine 到 Reverse，用于 WebSocket 重路由
		engine.GET("/"+name+"/api/*any", Reverse(&config.HTTPServer, engine))
		engine.POST("/"+name+"/api/*any", Reverse(&config.HTTPServer, engine))
	}
}

func Reverse(config *conf.HTTPServerConfig, engine *core.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		// WebSocket 请求不能通过普通反向代理，需要直接路由
		// 检测 Upgrade: websocket 头，如果是 WebSocket 则重写路径并触发重新路由
		if isWebSocketUpgrade(c.Request) {
			// 将 /api 路径转换为 /internal 路径
			newPath := strings.Replace(c.Request.URL.Path, "/api", "/internal", 1)
			c.Request.URL.Path = newPath
			c.Request.URL.RawPath = newPath

			// 手动触发 Gin 引擎重新处理这个请求
			// 这样会让请求重新匹配路由，找到真正的 WebSocket handler
			engine.HandleContext(c)
			c.Abort()
			return
		}

		if _, err := parseRequest(c.Request); err != nil {
			_ = c.Error(err)
			c.Abort()
			return
		}

		proxy := NewConsoleProxy(config)
		proxy.ServeHTTP(c.Writer, c.Request)
		c.Abort()
	}
}

// isWebSocketUpgrade 检查请求是否为 WebSocket 升级请求
func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

// NewConsoleProxy
// 由于OpenAPI和前端使用的API规范对同一个API的HTTP协议使用方式要求不同，故微服务会提供OpenAPI和FE API两套API
// ConsoleProxy能够将一个FE请求适配到OpenAPI，并将一个OpenAPI Response适配到FE响应上
// 通过在微服务中集成ConsoleProxy的反向代理，即可实现BCE FE API到OpenAPI的适配，eg: engine.POST("/example/api/*any", console.Reverse())
func NewConsoleProxy(config *conf.HTTPServerConfig) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director:       modifyRequest,
		ModifyResponse: modifyResponse,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: time.Millisecond * time.Duration(config.WriteTimeout), // 连接超时
			}).DialContext,
			ResponseHeaderTimeout: time.Millisecond * time.Duration(config.WriteTimeout), // 响应头超时
			IdleConnTimeout:       time.Millisecond * time.Duration(config.IdleTimeout),  // 空闲连接超时
		},
		ErrorHandler: func(writer http.ResponseWriter, request *http.Request, err error) {
			core.HTTPLogger.Errorf(context.Background(), "ProxyError req: %#v, err: %v", request, err)
			writer.WriteHeader(http.StatusBadGateway)
		}}
}

func modifyRequest(r *http.Request) {
	defer core.HTTPLogger.Debugf(context.Background(), "ProxyModifiedRequest: %#v, URL: %#v", r, r.URL)

	r.URL.Scheme = "http"
	r.URL.Host = conf.Default().HTTPServer.Listen

	path, err := parseRequest(r)
	if err != nil {
		core.HTTPLogger.Errorf(context.Background(), "ProxyError url path is invalid: %s", r.URL.Path)
		return
	}
	r.URL.Path = path
	r.RequestURI = r.URL.Path

	// 屏蔽静态资源的if-modified-since header头
	if _, ok := r.Header["If-Modified-Since"]; ok {
		r.Header["If-Modified-Since"] = []string{""}
	}
}

func modifyResponse(resp *http.Response) error {
	logger.Infof(context.Background(), "ProxyModifiedResponse url: %s", resp.Request.URL)
	ignoredContentType := []string{"application/zip", "application/pdf", "application/octet-stream", "text/event-stream"}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if arrutil.Contains(ignoredContentType, contentType) {
		logger.Infof(context.Background(), "ProxyModifiedResponse ignored content type %s", contentType)
		return nil
	}

	buffer, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// 如果buffer不是json string，则报错
	if len(buffer) > 0 && !json.Valid(buffer) {
		wrong := serrors.Error{
			Code:      "InappropriateJSON",
			Message:   string(buffer),
			RequestID: resp.Header.Get(constants.HeaderRequestID),
			Status:    http.StatusInternalServerError,
		}
		wrong.SetStatusCode()
		setResponse(resp, Response{Error: &wrong})
		return nil
	}

	// error
	if resp.StatusCode/100 != 2 && resp.StatusCode/100 != 3 {
		var wrong serrors.Error
		err = json.Unmarshal(buffer, &wrong)
		if err != nil {
			return err
		}
		if wrong.Status == 0 {
			wrong.Status = resp.StatusCode
		}
		wrong.SetStatusCode()
		setResponse(resp, Response{Error: &wrong})
		return nil
	}

	// success
	var content any
	_ = json.Unmarshal(buffer, &content)
	result := Response{
		Data: content,
		Error: &serrors.Error{
			Code:      "Success",
			RequestID: resp.Header.Get(constants.HeaderRequestID),
		},
	}
	setResponse(resp, result)
	return nil
}

func parseRequest(r *http.Request) (string, error) {
	// eg: POST /example/api/v1/GetProject => POST /example/internal/v1/GetProject
	// 注意：只转换路径，HTTP方法保持不变
	path := strings.Replace(r.URL.Path, "/api", "/internal", 1)
	if path == r.URL.Path {
		return "", serrors.ErrorInvalidURI
	}
	return path, nil
}

func setResponse(resp *http.Response, content any) {
	resp.StatusCode = http.StatusOK
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(content); err != nil {
		return
	}
	buffer := bytes.TrimRight(buf.Bytes(), "\n")
	reader := bytes.NewReader(buffer)
	resp.Body = io.NopCloser(reader)
	contentLength := len(buffer)
	resp.ContentLength = int64(contentLength)
	resp.Header.Set("Content-Length", strconv.Itoa(contentLength))
}
