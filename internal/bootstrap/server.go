// Package bootstrap adapts server-go's host with bounded, ordered shutdown.
package bootstrap

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"code.cestc.cn/ai-socket/server-go/host"
	"code.cestc.cn/ai-socket/server-go/middleware"
	"echo-service/internal/app/control"
	"echo-service/internal/client"
	"echo-service/internal/config"
	"echo-service/internal/lib"
	"echo-service/service"
	"github.com/gin-gonic/gin"
)

func NewServer(c *config.Config, clients *client.Clients, version string, api ...*control.Service) *http.Server {
	// A snapshot prevents host.reloadServer from discarding handler wrappers or
	// reinitializing clients while requests are using them. Restart to reconfigure.
	snapshot := c.Server
	// Suppress framework debug logs of request headers and bodies in every mode.
	snapshot.HTTPServer.Trace.PrefixFilters = []string{"/"}
	h := host.NewHTTPHost(&snapshot)
	svc := service.NewEchoService(snapshot.AppName, version, api...)
	h.AddService(svc)
	svc.StartAsync()

	base := h.Server.Handler
	health := gin.New()
	health.Use(middleware.RequestID())
	health.GET("/livez", func(ctx *gin.Context) { lib.OK(ctx, gin.H{"status": "alive"}) })
	health.GET("/healthz", func(ctx *gin.Context) { lib.OK(ctx, gin.H{"status": "alive"}) })
	health.GET("/readyz", func(ctx *gin.Context) {
		check, cancel := context.WithTimeout(ctx.Request.Context(), time.Second)
		defer cancel()
		if err := clients.Ready(check); err != nil {
			lib.Error(ctx, http.StatusServiceUnavailable, "DependencyUnavailable", "Database is unavailable")
			return
		}
		lib.OK(ctx, gin.H{"status": "ready"})
	})
	h.Server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metrics", "/feature", "/dapr/subscribe":
			http.NotFound(w, r)
		case "/livez", "/healthz", "/readyz":
			health.ServeHTTP(w, r)
		default:
			base.ServeHTTP(w, r)
		}
	})
	return h.Server
}

// Serve reports bind/serve errors and drains requests before callers close clients.
func Serve(ctx context.Context, server *http.Server, timeout time.Duration) error {
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	return ServeListener(ctx, server, listener, timeout)
}

func ServeListener(ctx context.Context, server *http.Server, listener net.Listener, timeout time.Duration) error {
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return err
		}
		err := <-result
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
