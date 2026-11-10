package middleware

import (
	"github.com/gin-gonic/gin"

	"code.cestc.cn/ai-socket/server-go/conf"
)

func Asset(assets []conf.HTTPServerAssetConfig, authEnable bool, engine *gin.Engine) {
	for _, asset := range assets {
		if asset.AuthEnable != authEnable {
			continue
		}
		if asset.FilePath == "" {
			continue
		}
		if asset.URLPath == "" {
			asset.URLPath = "/assets"
		}
		engine.Static(asset.URLPath, asset.FilePath)
	}
}
