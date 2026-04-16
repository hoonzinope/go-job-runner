package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type ImageHandler struct{}

func NewImageHandler() *ImageHandler {
	return &ImageHandler{}
}

func (h *ImageHandler) ListImages(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "image source handling is not implemented yet",
	})
}

func (h *ImageHandler) ListImageCandidates(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "image source handling is not implemented yet",
	})
}

func (h *ImageHandler) ResolveImage(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "image source handling is not implemented yet",
	})
}
