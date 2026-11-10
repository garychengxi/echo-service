package console

import (
	core "code.cestc.cn/ai-socket/server-go/pkg"
	"echo-service/internal/lib"
)

// RegisterRoute follows the reference service's POST action API convention.
func RegisterRoute(group *core.RouterGroup, svc Service) {
	group.POST("/v1/GetServiceInfo", func(c *core.Context) { lib.OK(c, svc.GetServiceInfo()) })
}
