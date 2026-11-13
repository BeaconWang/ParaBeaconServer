package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/repository"
	"github.com/parabeacon/server/internal/service"
)

type AppReleaseService interface {
	ListPublished(ctx context.Context) ([]model.AppRelease, error)
	Latest(ctx context.Context) (*model.AppRelease, error)
	Publish(ctx context.Context, versionName string, versionCode int64, source io.Reader) (*model.AppRelease, error)
	Delete(ctx context.Context, versionCode int64) error
	FindPublished(ctx context.Context, versionCode int64) (*model.AppRelease, string, error)
}

type AppReleaseHandler struct {
	releases AppReleaseService
}

func NewAppReleaseHandler(releases AppReleaseService) *AppReleaseHandler {
	return &AppReleaseHandler{releases: releases}
}

func setNoStoreHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
}

type latestReleaseResponse struct {
	VersionName string     `json:"version_name"`
	VersionCode int64      `json:"version_code"`
	PackageName string     `json:"package_name"`
	SizeBytes   int64      `json:"size_bytes"`
	SHA256      string     `json:"sha256"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	DownloadURL string     `json:"download_url"`
}

func (h *AppReleaseHandler) List(c *gin.Context) {
	releases, err := h.releases.ListPublished(c.Request.Context())
	if err != nil {
		respondInternalError(c)
		return
	}
	response := make([]latestReleaseResponse, 0, len(releases))
	for _, release := range releases {
		response = append(response, latestReleaseResponse{
			VersionName: release.VersionName,
			VersionCode: release.VersionCode,
			PackageName: release.PackageName,
			SizeBytes:   release.SizeBytes,
			SHA256:      release.SHA256,
			PublishedAt: release.PublishedAt,
			DownloadURL: fmt.Sprintf("/api/v1/app/releases/%d/download", release.VersionCode),
		})
	}
	setNoStoreHeaders(c)
	c.JSON(http.StatusOK, gin.H{"releases": response})
}

func (h *AppReleaseHandler) Latest(c *gin.Context) {
	release, err := h.releases.Latest(c.Request.Context())
	if err != nil {
		if service.IsAppReleaseNotFound(err) {
			respondError(c, http.StatusNotFound, "no app release available")
			return
		}
		respondInternalError(c)
		return
	}
	setNoStoreHeaders(c)
	c.JSON(http.StatusOK, latestReleaseResponse{
		VersionName: release.VersionName,
		VersionCode: release.VersionCode,
		PackageName: release.PackageName,
		SizeBytes:   release.SizeBytes,
		SHA256:      release.SHA256,
		PublishedAt: release.PublishedAt,
		DownloadURL: fmt.Sprintf("/api/v1/app/releases/%d/download", release.VersionCode),
	})
}

func (h *AppReleaseHandler) Download(c *gin.Context) {
	versionCode, err := strconv.ParseInt(c.Param("version_code"), 10, 64)
	if err != nil || versionCode < 1 {
		respondError(c, http.StatusBadRequest, "invalid app version code")
		return
	}
	release, path, err := h.releases.FindPublished(c.Request.Context(), versionCode)
	if err != nil {
		if service.IsAppReleaseNotFound(err) {
			respondError(c, http.StatusNotFound, "app release not found")
			return
		}
		respondInternalError(c)
		return
	}
	setNoStoreHeaders(c)
	c.Header("Content-Type", "application/vnd.android.package-archive")
	c.FileAttachment(path, fmt.Sprintf("parabeacon-%s.apk", release.VersionName))
}

func (h *AppReleaseHandler) Delete(c *gin.Context) {
	versionCode, err := strconv.ParseInt(c.Param("version_code"), 10, 64)
	if err != nil || versionCode < 1 {
		respondError(c, http.StatusBadRequest, "invalid app version code")
		return
	}
	if err := h.releases.Delete(c.Request.Context(), versionCode); err != nil {
		switch {
		case service.IsAppReleaseNotFound(err):
			respondError(c, http.StatusNotFound, "app release not found")
		case errors.Is(err, service.ErrInvalidAppRelease):
			respondError(c, http.StatusBadRequest, "invalid app version code")
		default:
			respondInternalError(c)
		}
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AppReleaseHandler) Publish(c *gin.Context) {
	versionName := strings.TrimSpace(c.GetHeader("X-APK-Version"))
	versionCode, err := strconv.ParseInt(c.GetHeader("X-APK-Version-Code"), 10, 64)
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid APK version code")
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(c.GetHeader("Content-Type"), ";")[0]))
	if contentType != "application/vnd.android.package-archive" && contentType != "application/octet-stream" {
		respondError(c, http.StatusUnsupportedMediaType, "APK content type required")
		return
	}
	if c.Request.ContentLength > service.MaxAPKSizeBytes {
		respondError(c, http.StatusRequestEntityTooLarge, "APK exceeds upload limit")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxAPKSizeBytes+1)
	release, err := h.releases.Publish(c.Request.Context(), versionName, versionCode, c.Request.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			respondError(c, http.StatusRequestEntityTooLarge, "APK exceeds upload limit")
			return
		}
		switch {
		case errors.Is(err, service.ErrAPKTooLarge):
			respondError(c, http.StatusRequestEntityTooLarge, "APK exceeds upload limit")
		case errors.Is(err, service.ErrInvalidAPK), errors.Is(err, service.ErrInvalidAppRelease):
			respondError(c, http.StatusBadRequest, "invalid APK release")
		case errors.Is(err, repository.ErrAppReleaseAlreadyExists):
			respondError(c, http.StatusConflict, "version code already published")
		default:
			respondInternalError(c)
		}
		return
	}
	c.JSON(http.StatusCreated, latestReleaseResponse{
		VersionName: release.VersionName,
		VersionCode: release.VersionCode,
		PackageName: release.PackageName,
		SizeBytes:   release.SizeBytes,
		SHA256:      release.SHA256,
		PublishedAt: release.PublishedAt,
		DownloadURL: fmt.Sprintf("/api/v1/app/releases/%d/download", release.VersionCode),
	})
}
