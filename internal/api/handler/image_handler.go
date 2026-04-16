package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoonzinope/go-job-runner/internal/image"
)

type ImageHandler struct {
	resolver *image.Resolver
}

func NewImageHandler(resolver *image.Resolver) *ImageHandler {
	return &ImageHandler{resolver: resolver}
}

func (h *ImageHandler) ListImages(c *gin.Context) {
	sourceType := c.Query("sourceType")
	if sourceType == "" {
		sourceType = h.resolver.DefaultSource()
	}
	q := c.Query("q")
	prefix := c.Query("prefix")

	candidates, err := h.resolver.ListCandidates(c.Request.Context(), sourceType, q, prefix)
	if err != nil {
		badRequest(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": candidates, "total": len(candidates)})
}

func (h *ImageHandler) ListImageCandidates(c *gin.Context) {
	sourceType := c.Param("sourceType")
	q := c.Query("q")
	prefix := c.Query("prefix")

	candidates, err := h.resolver.ListCandidates(c.Request.Context(), sourceType, q, prefix)
	if err != nil {
		badRequest(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": candidates, "total": len(candidates)})
}

func (h *ImageHandler) ResolveImage(c *gin.Context) {
	sourceType := c.Query("sourceType")
	if sourceType == "" {
		sourceType = h.resolver.DefaultSource()
	}
	imageRef := c.Query("imageRef")
	if imageRef == "" {
		badRequest(c, errRequired("imageRef"))
		return
	}

	candidate, err := h.resolver.Resolve(c.Request.Context(), sourceType, imageRef)
	if err != nil {
		badRequest(c, err)
		return
	}
	c.JSON(http.StatusOK, candidate)
}

func errRequired(field string) error {
	return &fieldError{field: field}
}

type fieldError struct {
	field string
}

func (e *fieldError) Error() string {
	return e.field + " is required"
}
