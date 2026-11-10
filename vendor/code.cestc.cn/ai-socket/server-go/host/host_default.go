package host

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"syscall"
	"time"

	"code.cestc.cn/ai-socket/logger-go"
	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
	"github.com/go-resty/resty/v2"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"code.cestc.cn/ai-socket/server-go/auth"
	"code.cestc.cn/ai-socket/server-go/conf"
	"code.cestc.cn/ai-socket/server-go/console"
	"code.cestc.cn/ai-socket/server-go/errors"
	"code.cestc.cn/ai-socket/server-go/metrics"
	"code.cestc.cn/ai-socket/server-go/middleware"
	core "code.cestc.cn/ai-socket/server-go/pkg"
	"code.cestc.cn/ai-socket/server-go/pkg/binder"
	"code.cestc.cn/ai-socket/server-go/pubsub"
	"code.cestc.cn/ai-socket/server-go/swagger"
	"code.cestc.cn/ai-socket/server-go/version"
)

const (
	DefaultReadTimeout    = 20000  // 20s
	DefaultWriteTimeout   = 10000  // 10s
	DefaultIdleTimeout    = 10000  // 10s
	DefaultMaxHeaderBytes = 100000 // 100k
)

type Service interface {
	LifeCycle

	// Initialize 完成数据库连接等初始化
	Initialize(e *core.Engine)
}

var _ Host = &HTTPHost{}

type HTTPHost struct {
	Server   *http.Server
	services []Service
	engine   *core.Engine
	config   *conf.Config
}

func NewHTTPHost(config *conf.Config) *HTTPHost {
	server := NewHTTPServer(config)
	engine := NewGinEngine(config)
	if config.HTTPServer.UseH2C {
		server.Handler = h2c.NewHandler(engine, &http2.Server{})
	} else {
		server.Handler = engine
	}

	host := &HTTPHost{
		Server: server,
		engine: engine,
		config: config,
	}

	if config == conf.Default() {
		conf.RegisterConfigContentChanged(func(newConfig *conf.Config, changedKeys []string) {
			host.reloadServer(newConfig)
		})
	}

	return host
}

func NewGinEngine(config *conf.Config) *core.Engine {
	gin.SetMode(config.RunMode)
	engine := gin.New()

	// 支持HTTP2无SSL
	if config.HTTPServer.UseH2C {
		engine.UseH2C = true
	}

	// 设置公共中间件
	engine.NoRoute(middleware.APINotFound())
	engine.Use(middleware.RequestID())

	switch config.RunMode {
	case conf.RunModeDebug, conf.RunModeTest:
		engine.Use(middleware.Debug(config.HTTPServer.Trace.PrefixFilters, config.HTTPServer.Trace.SuffixFilters))
	}

	if config.HTTPServer.PprofEnable {
		pprof.Register(engine, "/pprof")
	}
	engine.GET("/metrics", metrics.Metrics())
	engine.GET("/ping", middleware.Ping())
	engine.GET("/healthz", middleware.Healthz())
	engine.GET("/version", version.Version())
	if config.HTTPServer.SwaggerEnable {
		engine.GET("/swagger/*any", swagger.Swagger())
	}
	engine.GET("/feature", middleware.Feature(config.Feature))

	middleware.Asset(config.HTTPServer.Assets, false, engine)

	if config.HTTPServer.IdempotenceEnable {
		// 使用幂等中间件时，同一个请求的两次返回requestid可能不同，考虑到还需要可追踪的日志，所以接受这个差异
		engine.Use(middleware.Idempotence())
	}

	if config.HTTPServer.ConsoleEnable {
		console.RegisterConsoleAPI(engine, config)
	}

	auth.Use(engine, config)

	middleware.Asset(config.HTTPServer.Assets, true, engine)

	engine.Use(gin.Recovery())
	// ErrorHandler 统一处理错误，内部集成了 AutoHandleAPIError 的功能
	engine.Use(errors.ErrorHandler(config))

	engine.GET("dapr/subscribe", pubsub.Subscribe(engine))

	// 注册自定义参数校验
	binder.RegisterValidator()

	// 为所有接口配置统一前缀
	if config.HTTPServer.APIBasePath != "" {
		router := engine.Group(config.HTTPServer.APIBasePath)
		engine.RouterGroup = *router
	}

	return engine
}

func NewHTTPServer(config *conf.Config) *http.Server {
	readTimeout := config.HTTPServer.ReadTimeout
	writeTimeout := config.HTTPServer.WriteTimeout
	idleTimeout := config.HTTPServer.IdleTimeout
	maxHeaderBytes := config.HTTPServer.MaxHeaderBytes

	if readTimeout == 0 {
		readTimeout = DefaultReadTimeout
	}
	if writeTimeout == 0 {
		writeTimeout = DefaultWriteTimeout
	}
	if idleTimeout == 0 {
		idleTimeout = DefaultIdleTimeout
	}
	if maxHeaderBytes == 0 {
		maxHeaderBytes = DefaultMaxHeaderBytes
	}

	if config.HTTPServer.Listen == "" {
		listen, err := net.Listen("tcp", "localhost:0")
		if err != nil {
			logger.Fatalf(context.Background(), "get random listen address error: %v", err)
		}
		config.HTTPServer.Listen = listen.Addr().String()
	}

	s := &http.Server{
		Addr:              config.HTTPServer.Listen,
		ReadTimeout:       time.Millisecond * time.Duration(readTimeout),
		ReadHeaderTimeout: time.Millisecond * time.Duration(readTimeout),
		WriteTimeout:      time.Millisecond * time.Duration(writeTimeout),
		IdleTimeout:       time.Millisecond * time.Duration(idleTimeout),
		MaxHeaderBytes:    maxHeaderBytes,
	}

	return s
}

func (h *HTTPHost) AddService(services ...LifeCycle) {
	for _, s := range services {
		hs, ok := s.(Service)
		if ok {
			hs.Initialize(h.engine)
			h.services = append(h.services, hs)
		}
	}
}

func (h *HTTPHost) ReloadService() {
	for _, s := range h.services {
		s.Initialize(h.engine)
	}
}

func (h *HTTPHost) Run() {
	h.RunWithContext(context.Background())
}

// RunWithContext 启动服务， context被取消或收到Ctrl+C信号时，服务停止
func (h *HTTPHost) RunWithContext(ctx context.Context) {
	logger.Infof(ctx, version.GetVersionString())

	for _, svc := range h.services {
		logger.Debugf(ctx, "Service starting %s", reflect.TypeOf(svc))
		svc.StartAsync()
	}

	h.startServer()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-quit:
	case <-ctx.Done():
	}

	for _, svc := range h.services {
		logger.Debugf(ctx, "Service stopping %s", reflect.TypeOf(svc))
		svc.StopAsync()
	}

	h.shutdownServer()
}

func (h *HTTPHost) startServer() {
	go func() {
		if err := h.Server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalf(context.Background(), "StartServerError: %v", err)
		}
	}()
	logger.Infof(context.Background(), "Server started at: %s", h.Server.Addr)
}

func (h *HTTPHost) shutdownServer() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.Server.Shutdown(ctx); err != nil {
		logger.Errorf(context.Background(), "ShutdownServerError: %v", err)
	}

	logger.Infof(context.Background(), "Server exited: %s", h.Server.Addr)
}

func (h *HTTPHost) reloadServer(newConfig *conf.Config) {
	if newConfig == nil {
		return
	}

	if reflect.DeepEqual(conf.Default().HTTPServer, newConfig.HTTPServer) {
		return
	}

	logger.Infof(context.Background(), "Server reloading...")
	h.shutdownServer()

	server := NewHTTPServer(newConfig)
	engine := NewGinEngine(newConfig)
	if newConfig.HTTPServer.UseH2C {
		server.Handler = h2c.NewHandler(engine, &http2.Server{})
	} else {
		server.Handler = engine
	}

	h.Server = server
	h.engine = engine
	h.config = newConfig

	h.ReloadService()
	h.Run()
}

func (h *HTTPHost) LoopPing(interval time.Duration, maxRetries int) bool {
	for i := 0; i < maxRetries; i++ {
		_, err := resty.New().R().EnableTrace().Get("http://" + h.Server.Addr + "/ping")
		if err == nil {
			return true
		}
		time.Sleep(interval)
	}
	return false
}
