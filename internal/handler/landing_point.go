package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/middleware"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/service"
)

const maxLandingPointRequestBytes = 1 << 20

type LandingPointService interface {
	Import(ctx context.Context, userID uuid.UUID, points []model.LandingPoint, defaultID *string) error
	List(ctx context.Context, userID uuid.UUID) ([]model.LandingPoint, *string, error)
}

type LandingPointHandler struct {
	points LandingPointService
}

func NewLandingPointHandler(points LandingPointService) *LandingPointHandler {
	return &LandingPointHandler{points: points}
}

func (h *LandingPointHandler) Import(c *gin.Context) {
	userID, ok := landingPointUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxLandingPointRequestBytes)
	var request struct {
		Points   []model.LandingPoint `json:"points"`
		DefaultID *string              `json:"default_id"`
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if request.Points == nil {
		respondError(c, http.StatusBadRequest, "missing landing points")
		return
	}
	if err := h.points.Import(c.Request.Context(), userID, request.Points, request.DefaultID); errors.Is(err, service.ErrInvalidLandingPoints) {
		respondError(c, http.StatusBadRequest, "invalid landing points")
		return
	} else if err != nil {
		respondInternalError(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{"synced": len(request.Points)})
}

func (h *LandingPointHandler) List(c *gin.Context) {
	userID, ok := landingPointUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	points, defaultID, err := h.points.List(c.Request.Context(), userID)
	if errors.Is(err, service.ErrInvalidLandingPoints) {
		respondError(c, http.StatusInternalServerError, "invalid stored landing points")
		return
	}
	if err != nil {
		respondInternalError(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{"points": points, "default_id": defaultID})
}

func landingPointUserID(c *gin.Context) (uuid.UUID, bool) {
	value, ok := c.Get(middleware.AuthenticatedUserIDKey)
	if !ok {
		return uuid.Nil, false
	}
	userID, ok := value.(uuid.UUID)
	return userID, ok && userID != uuid.Nil
}
