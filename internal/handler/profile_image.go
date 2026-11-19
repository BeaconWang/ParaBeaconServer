package handler

import (
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/parabeacon/server/internal/service"
	"gorm.io/gorm"
)

type ProfileImageHandler struct {
	images *service.ProfileImageService
}

func NewProfileImageHandler(images *service.ProfileImageService) *ProfileImageHandler {
	return &ProfileImageHandler{images: images}
}

func (h *ProfileImageHandler) Upload(c *gin.Context) {
	userID, ok := authenticatedUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxProfileImageUploadBytes+(1<<20))
	if err := c.Request.ParseMultipartForm(1 << 20); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			respondError(c, http.StatusRequestEntityTooLarge, "profile image is too large")
		} else {
			respondError(c, http.StatusBadRequest, "invalid image upload")
		}
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	file, _, err := c.Request.FormFile("image")
	if err != nil {
		respondError(c, http.StatusBadRequest, "profile image is required")
		return
	}
	defer file.Close()
	user, err := h.images.Upload(c.Request.Context(), userID, file)
	if err != nil {
		respondProfileImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": toPublicUser(user.ID.String(), user.Username, user.DisplayName, user.Introduction, user.Gender, user.Birthday, user.PilotNumber, user.ChannelNumber, user.AvatarID, user.ProfileImageKey, user.Email, user.CreatedAt)})
}

func (h *ProfileImageHandler) Remove(c *gin.Context) {
	userID, ok := authenticatedUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	user, err := h.images.Remove(c.Request.Context(), userID)
	if err != nil {
		respondProfileImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": toPublicUser(user.ID.String(), user.Username, user.DisplayName, user.Introduction, user.Gender, user.Birthday, user.PilotNumber, user.ChannelNumber, user.AvatarID, user.ProfileImageKey, user.Email, user.CreatedAt)})
}

func (h *ProfileImageHandler) Get(c *gin.Context) {
	imageKey := c.Param("image_key")
	file, info, err := h.images.Open(imageKey)
	if err != nil {
		respondProfileImageError(c, err)
		return
	}
	defer file.Close()
	c.Header("Content-Type", "image/jpeg")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "public, max-age=300, must-revalidate")
	etag := `"` + imageKey + `"`
	c.Header("ETag", etag)
	if strings.TrimSpace(c.GetHeader("If-None-Match")) == etag {
		c.Status(http.StatusNotModified)
		return
	}
	http.ServeContent(c.Writer, c.Request, "profile-image.jpg", info.ModTime(), file)
}

func respondProfileImageError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidProfileImage):
		respondError(c, http.StatusBadRequest, "invalid profile image")
	case errors.Is(err, service.ErrProfileImageNotFound), errors.Is(err, gorm.ErrRecordNotFound), errors.Is(err, os.ErrNotExist):
		respondError(c, http.StatusNotFound, "profile image not found")
	default:
		respondInternalError(c)
	}
}
