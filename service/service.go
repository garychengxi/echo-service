// Package service composes modules using the reference host.Service lifecycle.
package service

import (
	"code.cestc.cn/ai-socket/server-go/host"
	core "code.cestc.cn/ai-socket/server-go/pkg"
	"echo-service/internal/app/console"
	"echo-service/internal/app/control"
	"echo-service/internal/app/gateway"
)

type EchoService struct {
	engine  *core.Engine
	info    console.Service
	control *control.Service
}

var _ host.Service = (*EchoService)(nil)

func NewEchoService(name, version string, api ...*control.Service) *EchoService {
	s := &EchoService{info: console.Service{Name: name, Version: version}}
	if len(api) > 0 {
		s.control = api[0]
		s.info.AuthenticationImplemented = true
	}
	return s
}
func (s *EchoService) Initialize(e *core.Engine) { s.engine = e }
func (s *EchoService) StartAsync() {
	if s.control != nil {
		s.control.Register(s.engine)
	}
	console.RegisterRoute(s.engine.Group("/echo-service/api"), s.info)
	console.RegisterRoute(s.engine.Group("/echo-service/openapi"), s.info)
	gateway.RegisterRoute(s.engine.Group("/echo-service/gateway"))
}
func (s *EchoService) StopAsync() {}
