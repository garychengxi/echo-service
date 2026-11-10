// Package gateway owns future provider routing, credentials and streaming adapters.
package gateway

import (
	"net/http"

	core "code.cestc.cn/ai-socket/server-go/pkg"
	"echo-service/internal/lib"
)

// No generic reverse proxy is exposed before authentication and routing exist.
func RegisterRoute(group *core.RouterGroup) {
	group.Any("/v1/*path", func(c *core.Context) {
		lib.Error(c, http.StatusNotImplemented, "GatewayNotImplemented", "Gateway forwarding is not implemented")
	})
}
