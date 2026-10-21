package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/middleware"
	"github.com/parabeacon/server/internal/service"
)

type UserHandler struct {
	auth *service.AuthService
}

func NewUserHandler(auth *service.AuthService) *UserHandler {
	return &UserHandler{auth: auth}
}

func (h *UserHandler) Profile(c *gin.Context) {
	value, ok := c.Get(middleware.AuthenticatedUserIDKey)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	userID, ok := value.(uuid.UUID)
	if !ok {
		respondInternalError(c)
		return
	}
	user, err := h.auth.Profile(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			respondError(c, http.StatusNotFound, "user not found")
			return
		}
		respondInternalError(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": toPublicUser(user.ID.String(), user.Username, user.Email, user.CreatedAt)})
}
