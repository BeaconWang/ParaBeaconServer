package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
)

func RequireReleaseAdmin(configuredToken string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if len(configuredToken) < 32 {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "release publishing is not configured"})
			return
		}
		provided := c.GetHeader("X-Release-Admin-Token")
		if len(provided) != len(configuredToken) || subtle.ConstantTimeCompare([]byte(provided), []byte(configuredToken)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}
