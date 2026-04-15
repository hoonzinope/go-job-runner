package handler

import (
	"time"

	"github.com/gin-gonic/gin"
)

func HealthzHandler(c *gin.Context) {
	c.JSON(200, gin.H{
		"message":   "API is healthy",
		"status":    "ok",
		"timestamp": time.Now().Format("2006-01-02 15:04:05"),
	})
}
