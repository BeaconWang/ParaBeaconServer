package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func NotImplemented(module string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusNotImplemented, gin.H{
			"error":   "not_implemented",
			"module":  module,
			"message": "this module is reserved for a future milestone",
		})
	}
}
