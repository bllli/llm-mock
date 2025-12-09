package internal

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func ErrorLogger(c *gin.Context) {
	start := time.Now()
	c.Next()
	latency := time.Since(start)
	statusCode := c.Writer.Status()
	clientIP := c.ClientIP()
	method := c.Request.Method
	path := c.Request.URL.Path

	if statusCode != 200 {
		errMsg := fmt.Sprintf("gin: %d | %s | %s | %s | %s ", statusCode, latency, clientIP, method, path)
		GinLogger.Error(errMsg)
	}
}

func NewServer() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(ErrorLogger)

	// Prometheus metrics endpoint
	engine.GET("/metrics", gin.WrapH(promhttp.Handler()))

	v1Group := engine.Group("/v1")
	{
		v1Group.GET("/models", ModelsHandler)
		v1Group.POST("/chat/completions", ChatHandler)
	}

	return engine
}
