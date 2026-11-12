package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/parabeacon/server/internal/service"
)

type AuthHandler struct {
	auth *service.AuthService
}

func NewAuthHandler(auth *service.AuthService) *AuthHandler {
	return &AuthHandler{auth: auth}
}

type registerRequest struct {
	Username string `json:"username" binding:"required,min=3,max=32"`
	Email    string `json:"email" binding:"omitempty,email,max=254"`
	Password string `json:"password" binding:"required,min=6,max=72"`
}

type loginRequest struct {
	Identifier string `json:"identifier" binding:"omitempty,max=254"`
	Email      string `json:"email" binding:"omitempty,email,max=254"`
	Password   string `json:"password" binding:"required,min=1,max=72"`
}

type publicUser struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Email     *string   `json:"email,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func toPublicUser(userID, username string, email *string, createdAt time.Time) publicUser {
	return publicUser{ID: userID, Username: username, Email: email, CreatedAt: createdAt}
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	user, err := h.auth.Register(c.Request.Context(), req.Username, req.Email, req.Password)
	if err != nil {
		if errors.Is(err, service.ErrConflict) {
			respondError(c, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, service.ErrInvalidInput) {
			respondError(c, http.StatusBadRequest, "invalid registration details")
			return
		}
		respondInternalError(c)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"user": toPublicUser(user.ID.String(), user.Username, user.Email, user.CreatedAt)})
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	identifier := req.Identifier
	if identifier == "" {
		identifier = req.Email
	} else if req.Email != "" {
		respondError(c, http.StatusBadRequest, "provide either identifier or email")
		return
	}
	if identifier == "" {
		respondError(c, http.StatusBadRequest, "login identifier is required")
		return
	}
	result, err := h.auth.Login(c.Request.Context(), identifier, req.Password)
	if err != nil {
		if errors.Is(err, service.ErrInvalidCredentials) {
			respondError(c, http.StatusUnauthorized, "invalid email or password")
			return
		}
		respondInternalError(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"access_token": result.Token,
		"token_type":   "Bearer",
		"expires_at":   result.ExpiresAt,
		"expires_in":   int(time.Until(result.ExpiresAt).Seconds()),
		"user":         toPublicUser(result.User.ID.String(), result.User.Username, result.User.Email, result.User.CreatedAt),
	})
}
