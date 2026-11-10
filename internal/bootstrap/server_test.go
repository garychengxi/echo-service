package bootstrap

import (
	"code.cestc.cn/ai-socket/logger-go"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	frameworkconf "code.cestc.cn/ai-socket/server-go/conf"
	"echo-service/internal/client"
	"echo-service/internal/config"
	sdk "echo-service/sdk/go/echoservicev1"
)

func TestMain(m *testing.M) {
	logger.SetDefaultConfig(&logger.Config{Outputs: []logger.Output{{Type: "Console", Level: "Error"}}})
	code := m.Run()
	_ = logger.Sync()
	os.Exit(code)
}

func testServer(t *testing.T) *http.Server {
	c, err := client.Open(config.Database{Path: ":memory:", BusyTimeout: time.Second, MaxOpenConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return NewServer(&config.Config{Server: frameworkconf.Config{AppName: "echo", AppCode: 100, RunMode: "release", HTTPServer: frameworkconf.HTTPServerConfig{Listen: "127.0.0.1:0", AuthType: "Fake"}}}, c, "test-version")
}

func TestRoutesAndContracts(t *testing.T) {
	server := testServer(t)
	for _, tt := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/livez", 200}, {"GET", "/healthz", 200}, {"GET", "/readyz", 200},
		{"POST", "/echo-service/api/v1/GetServiceInfo", 200},
		{"POST", "/echo-service/openapi/v1/GetServiceInfo", 200},
		{"POST", "/echo-service/gateway/v1/chat/completions", 501},
		{"GET", "/metrics", 404}, {"GET", "/feature", 404}, {"GET", "/dapr/subscribe", 404},
	} {
		t.Run(tt.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader("{}"))
			req.Header.Set("Request-ID", "contract-test")
			server.Handler.ServeHTTP(w, req)
			if w.Code != tt.status {
				t.Fatalf("status %d, want %d: %s", w.Code, tt.status, w.Body.String())
			}
			if tt.status == 404 {
				return
			}
			var body sdk.Response[json.RawMessage]
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.RequestID != "contract-test" {
				t.Fatalf("request correlation lost: %+v", body.Error)
			}
			if tt.status == 501 && body.Error.Code != "GatewayNotImplemented" {
				t.Fatal("gateway falsely reported success")
			}
			if strings.Contains(tt.path, "GetServiceInfo") {
				var info sdk.ServiceInfo
				if err := json.Unmarshal(body.Data, &info); err != nil {
					t.Fatal(err)
				}
				if info.Version != "test-version" || info.GatewayImplemented || info.AuthenticationImplemented {
					t.Fatalf("incorrect capabilities: %+v", info)
				}
			}
		})
	}
}

func TestServeDrainsInflightRequest(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte("finished"))
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer server.Close()
	finished := make(chan error, 1)
	go func() { finished <- ServeListener(ctx, server, listener, time.Second) }()
	request := make(chan error, 1)
	go func() {
		c := &http.Client{Timeout: 2 * time.Second}
		resp, err := c.Get("http://" + listener.Addr().String())
		if err != nil {
			request <- err
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err == nil && string(body) != "finished" {
			err = io.ErrUnexpectedEOF
		}
		request <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	select {
	case err := <-finished:
		t.Fatalf("stopped before request drained: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-request; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not finish")
	}
}

func TestBindFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := Serve(context.Background(), &http.Server{Addr: listener.Addr().String()}, time.Second); err == nil {
		t.Fatal("bind error was swallowed")
	}
}
