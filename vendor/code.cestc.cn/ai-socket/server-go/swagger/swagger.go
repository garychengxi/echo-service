package swagger

import (
	"code.cestc.cn/ai-socket/logger-go"
	"context"
	"github.com/gin-gonic/gin"
	"github.com/swaggo/files"
	"github.com/swaggo/gin-swagger"
)

var (
	DocPrefix = "unknown"
)

func Swagger() gin.HandlerFunc {
	return ginSwagger.WrapHandler(swaggerFiles.Handler, func(config *ginSwagger.Config) {
		logger.Debugf(context.Background(), "SwaggerInstanceName: %s", DocPrefix)
		config.InstanceName = DocPrefix
	})
}
