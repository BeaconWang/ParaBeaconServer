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

type updateProfileRequest struct {
	DisplayName *string `json:"display_name"`
}

func (h *UserHandler) UpdateProfile(c *gin.Context) {
	value, ok := c.Get(middleware.AuthenticatedUserIDKey)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	userID, ok := value.(uuid.UUID)
	if !ok || userID == uuid.Nil {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	var request updateProfileRequest
	if err := c.ShouldBindJSON(&request); err != nil || request.DisplayName == nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	user, err := h.auth.UpdateDisplayName(c.Request.Context(), userID, *request.DisplayName)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidDisplayName):
			respondError(c, http.StatusBadRequest, "invalid display name")
		case errors.Is(err, service.ErrNotFound):
			respondError(c, http.StatusNotFound, "user not found")
		default:
			respondInternalError(c)
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": toPublicUser(user.ID.String(), user.Username, user.DisplayName, user.PilotNumber, user.Email, user.CreatedAt)})
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
	c.JSON(http.StatusOK, gin.H{"user": toPublicUser(user.ID.String(), user.Username, user.DisplayName, user.PilotNumber, user.Email, user.CreatedAt)})
}
