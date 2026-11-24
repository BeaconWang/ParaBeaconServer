package handler

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/service"
	"gorm.io/gorm"
)

type ChatMediaHandler struct {
	media *service.ChatMediaService
}

func NewChatMediaHandler(media *service.ChatMediaService) *ChatMediaHandler {
	return &ChatMediaHandler{media: media}
}

func (h *ChatMediaHandler) Upload(c *gin.Context) {
	userID, ok := chatRequesterID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxChatVoiceBytes+(1<<20))
	if err := c.Request.ParseMultipartForm(1 << 20); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			respondError(c, http.StatusRequestEntityTooLarge, "chat media is too large")
		} else {
			respondError(c, http.StatusBadRequest, "invalid chat media upload")
		}
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	kind := strings.TrimSpace(c.PostForm("kind"))
	scope := strings.TrimSpace(c.PostForm("scope"))
	channelNumber, err := optionalChatNumber(c.PostForm("channel_number"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid channel number")
		return
	}
	pilotNumber, err := optionalChatNumber(c.PostForm("pilot_number"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid pilot number")
		return
	}
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		respondError(c, http.StatusBadRequest, "chat media file is required")
		return
	}
	defer file.Close()
	mediaID, err := h.media.Upload(c.Request.Context(), userID, scope, channelNumber, pilotNumber, kind, file)
	if err != nil {
		respondChatMediaError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"media_id": mediaID.String()})
}

func (h *ChatMediaHandler) Get(c *gin.Context) {
	userID, ok := chatRequesterID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	mediaID, err := uuid.Parse(c.Param("media_id"))
	if err != nil || mediaID == uuid.Nil || mediaID.String() != c.Param("media_id") {
		respondError(c, http.StatusNotFound, "chat media not found")
		return
	}
	file, info, contentType, err := h.media.Open(c.Request.Context(), userID, mediaID)
	if err != nil {
		respondChatMediaError(c, err)
		return
	}
	defer file.Close()
	c.Header("Content-Type", contentType)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", "inline")
	c.Header("Cache-Control", "private, no-store")
	c.Header("Content-Length", strconv.FormatInt(info.Size(), 10))
	name := "chat-image.jpg"
	if contentType == "audio/mp4" {
		name = "chat-voice.m4a"
	}
	http.ServeContent(c.Writer, c.Request, name, info.ModTime(), file)
}

func respondChatMediaError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidChatMedia), errors.Is(err, service.ErrInvalidChatRequest), errors.Is(err, service.ErrChatMediaUploadExpired):
		respondError(c, http.StatusBadRequest, "invalid chat media")
	case errors.Is(err, service.ErrChatMediaNotFound), errors.Is(err, gorm.ErrRecordNotFound), errors.Is(err, os.ErrNotExist):
		respondError(c, http.StatusNotFound, "chat media not found")
	default:
		respondInternalError(c)
	}
}
