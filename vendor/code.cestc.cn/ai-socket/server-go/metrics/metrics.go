package metrics

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func Metrics() gin.HandlerFunc {
	return gin.WrapH(promhttp.Handler())
}
