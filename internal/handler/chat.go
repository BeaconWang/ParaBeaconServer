package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/middleware"
	"github.com/parabeacon/server/internal/realtime"
	"github.com/parabeacon/server/internal/service"
)

type ChatHandler struct {
	chat *service.ChatService
	hub  *realtime.ChatHub
}

func NewChatHandler(chat *service.ChatService, hub *realtime.ChatHub) *ChatHandler {
	return &ChatHandler{chat: chat, hub: hub}
}

func (h *ChatHandler) IssueSocketTicket(c *gin.Context) {
	userID, ok := chatRequesterID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	ticket, err := h.hub.IssueTicket(c.Request.Context(), userID)
	if err != nil {
		respondInternalError(c)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"ticket": ticket, "expires_in": 30})
}

func (h *ChatHandler) WebSocket(allowedOrigins []string) gin.HandlerFunc {
	return h.hub.ServeHTTP(allowedOrigins)
}

func (h *ChatHandler) PilotLocationStatus(c *gin.Context) {
	userID, ok := chatRequesterID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	pilotNumber, err := strconv.Atoi(c.Param("pilot_number"))
	if err != nil || pilotNumber < 100000 || pilotNumber > 999999 {
		respondError(c, http.StatusBadRequest, "invalid pilot number")
		return
	}
	sharing, err := h.chat.PublicLocationStatus(c.Request.Context(), userID, pilotNumber)
	if err != nil {
		respondChatError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"actively_sharing_location": sharing})
}

func (h *ChatHandler) SearchAccounts(c *gin.Context) {
	userID, ok := chatRequesterID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	limit := service.ChatAccountPageSize
	if rawLimit := c.Query("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 || parsed > service.ChatAccountMaxPageSize {
			respondError(c, http.StatusBadRequest, "invalid account search limit")
			return
		}
		limit = parsed
	}
	page, err := h.chat.SearchAccounts(
		c.Request.Context(),
		userID,
		c.Query("q"),
		c.Query("cursor"),
		limit,
	)
	if err != nil {
		respondChatError(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

func (h *ChatHandler) ListMessages(c *gin.Context) {
	userID, ok := chatRequesterID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	channel, err := optionalChatNumber(c.Query("channel_number"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid channel number")
		return
	}
	pilot, err := optionalChatNumber(c.Query("pilot_number"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid pilot number")
		return
	}
	page, err := h.chat.History(c.Request.Context(), userID, c.Query("scope"), channel, pilot, c.Query("cursor"))
	if err != nil {
		respondChatError(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

func chatRequesterID(c *gin.Context) (uuid.UUID, bool) {
	value, ok := c.Get(middleware.AuthenticatedUserIDKey)
	if !ok {
		return uuid.Nil, false
	}
	userID, ok := value.(uuid.UUID)
	return userID, ok && userID != uuid.Nil
}

func optionalChatNumber(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < 0 || number > 999999 {
		return 0, service.ErrInvalidChatRequest
	}
	return number, nil
}

func respondChatError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidChatRequest):
		respondError(c, http.StatusBadRequest, "invalid chat request")
	case errors.Is(err, service.ErrChatUserNotFound):
		respondError(c, http.StatusNotFound, "pilot not found")
	default:
		respondInternalError(c)
	}
}
