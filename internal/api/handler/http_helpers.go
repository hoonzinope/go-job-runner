package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func badRequest(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
}

func notFound(c *gin.Context, err error) {
	c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
}

func internalError(c *gin.Context, err error) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

func parseIntParam(c *gin.Context, name string) (int64, error) {
	return strconv.ParseInt(c.Param(name), 10, 64)
}

func parsePageQuery(c *gin.Context) (page int, size int) {
	page = 1
	size = 20
	if v := c.Query("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	if v := c.Query("size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			size = n
		}
	}
	return page, size
}

func parseBoolPtr(value string) (*bool, error) {
	if value == "" {
		return nil, nil
	}
	switch value {
	case "true":
		v := true
		return &v, nil
	case "false":
		v := false
		return &v, nil
	default:
		return nil, strconv.ErrSyntax
	}
}
