package middleware

import (
	"context"
	"net/http"
	"sync"

	licensemanager "code.cestc.cn/ai-socket/licence-manager/sdk/go/license-manager"
	"code.cestc.cn/ai-socket/logger-go"
	"github.com/gin-gonic/gin"

	"code.cestc.cn/ai-socket/server-go/errors"
	core "code.cestc.cn/ai-socket/server-go/pkg"
)

var (
	authClient     *licensemanager.AuthClient
	authClientOnce sync.Once
)

// LicenseAuth 创建license鉴权中间件
// config: AuthClientConfig配置，如果client未初始化，会自动创建并启动client
func LicenseAuth(config *licensemanager.AuthClientConfig) gin.HandlerFunc {
	// 确保client已初始化
	initAuthClient(config)

	return func(c *gin.Context) {
		if authClient == nil {
			errors.AbortWithError(c, errors.NewError(http.StatusInternalServerError, "LicenseAuthFailed", "license鉴权客户端未初始化"))
			return
		}

		// 检查授权状态
		// AuthClient在后台定期认证，这里检查当前授权状态
		// GetAuthorization() 返回 true 表示已获得授权，false 表示未获得授权
		if !authClient.GetAuthorization() {
			// 获取错误信息并打印日志
			errorMsg := authClient.ErrorMessage()
			core.HTTPLogger.Errorf(c, "LicenseAuthError err: %s", errorMsg)

			errors.AbortWithError(c, errors.NewError(http.StatusUnauthorized, "LicenseAuthFailed", "license鉴权失败，未获得授权"))
			return
		}

		// 鉴权成功，继续处理请求
		c.Next()
	}
}

// initAuthClient 初始化AuthClient，使用sync.Once确保只初始化一次
func initAuthClient(config *licensemanager.AuthClientConfig) {
	authClientOnce.Do(func() {
		if config == nil {
			logger.Fatalf(context.Background(), "init auth client failed, config is nil")
		}

		// 检查ModuleName是否为空
		if config.ModuleName == "" {
			logger.Fatalf(context.Background(), "init auth client failed, config.ModuleName is empty")
		}

		client, err := licensemanager.NewAuthClient(config)
		if err != nil {
			logger.Fatalf(context.Background(), "init auth client failed, err: %v", err)
		}

		// 启动客户端（开始定期认证）
		client.Start()
		authClient = client
	})
}
