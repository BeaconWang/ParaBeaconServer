package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type errorResponse struct {
	Error string `json:"error"`
}

func respondError(c *gin.Context, status int, message string) {
	c.JSON(status, errorResponse{Error: message})
}

func respondInternalError(c *gin.Context) {
	respondError(c, http.StatusInternalServerError, "internal server error")
}
