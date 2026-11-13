package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/middleware"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/service"
)

type TrackingService interface {
	StartSession(ctx context.Context, userID uuid.UUID, pilotName string, startedAt time.Time) (*model.LiveTrackSession, error)
	StorePoints(ctx context.Context, userID, sessionID uuid.UUID, points []model.LiveTrackPoint) (int64, error)
	StopSession(ctx context.Context, userID, sessionID uuid.UUID, endedAt time.Time) error
	ListActiveSessions(ctx context.Context, userID uuid.UUID) ([]model.LiveTrackSession, error)
}

type TrackingHandler struct {
	tracking TrackingService
}

func NewTrackingHandler(tracking TrackingService) *TrackingHandler {
	return &TrackingHandler{tracking: tracking}
}

type startTrackingRequest struct {
	PilotName string    `json:"pilot_name"`
	StartTime time.Time `json:"start_time"`
}

type stopTrackingRequest struct {
	EndTime time.Time `json:"end_time"`
}

type trackingPointRequest struct {
	SessionID          string     `json:"session_id"`
	Latitude           *float64   `json:"lat"`
	Longitude          *float64   `json:"lon"`
	Altitude           *float64   `json:"altitude"`
	GroundSpeed        *float64   `json:"speed"`
	VerticalSpeed      *float64   `json:"vertical_speed"`
	GPSAltitude        *float64   `json:"gps_altitude"`
	BarometricAltitude *float64   `json:"barometric_altitude"`
	Heading            *float64   `json:"heading"`
	Bearing            *float64   `json:"bearing"`
	WindSpeed          *float64   `json:"wind_speed"`
	WindDirection      *float64   `json:"wind_direction"`
	GPSAccuracy        *float64   `json:"gps_accuracy"`
	Satellites         *int       `json:"satellites"`
	Pressure           *float64   `json:"pressure"`
	Temperature        *float64   `json:"temperature"`
	Timestamp          *time.Time `json:"timestamp"`
}

func (h *TrackingHandler) ListActive(c *gin.Context) {
	userID, ok := trackingUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	sessions, err := h.tracking.ListActiveSessions(c.Request.Context(), userID)
	if err != nil {
		respondInternalError(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

func (h *TrackingHandler) Start(c *gin.Context) {
	userID, ok := trackingUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	var request startTrackingRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	session, err := h.tracking.StartSession(c.Request.Context(), userID, request.PilotName, request.StartTime)
	if err != nil {
		respondTrackingError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"session_id": session.ID.String(), "started_at": session.StartedAt, "pilot_name": session.PilotName})
}

func (h *TrackingHandler) AddPoints(c *gin.Context) {
	userID, ok := trackingUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	sessionID, err := uuid.Parse(c.Param("session_id"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid session id")
		return
	}
	var request struct {
		SessionID   string                 `json:"session_id"`
		Trackpoints []trackingPointRequest `json:"trackpoints"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if request.SessionID != "" && request.SessionID != sessionID.String() {
		respondError(c, http.StatusBadRequest, "session id mismatch")
		return
	}
	if len(request.Trackpoints) < 1 || len(request.Trackpoints) > service.MaxTrackingBatchSize {
		respondError(c, http.StatusBadRequest, "invalid trackpoint batch size")
		return
	}
	points := make([]model.LiveTrackPoint, 0, len(request.Trackpoints))
	for _, raw := range request.Trackpoints {
		point, ok := raw.toModel()
		if !ok {
			respondError(c, http.StatusBadRequest, "invalid trackpoint")
			return
		}
		points = append(points, point)
	}
	accepted, err := h.tracking.StorePoints(c.Request.Context(), userID, sessionID, points)
	if err != nil {
		respondTrackingError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"accepted": accepted})
}

func (h *TrackingHandler) Stop(c *gin.Context) {
	userID, ok := trackingUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	sessionID, err := uuid.Parse(c.Param("session_id"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid session id")
		return
	}
	var request stopTrackingRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if request.EndTime.IsZero() {
		respondError(c, http.StatusBadRequest, "invalid end time")
		return
	}
	if err := h.tracking.StopSession(c.Request.Context(), userID, sessionID, request.EndTime); err != nil {
		respondTrackingError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (r trackingPointRequest) toModel() (model.LiveTrackPoint, bool) {
	if r.Latitude == nil || r.Longitude == nil || r.Altitude == nil || r.GroundSpeed == nil || r.Timestamp == nil {
		return model.LiveTrackPoint{}, false
	}
	return model.LiveTrackPoint{
		Latitude:           *r.Latitude,
		Longitude:          *r.Longitude,
		Altitude:           *r.Altitude,
		GroundSpeed:        *r.GroundSpeed,
		VerticalSpeed:      r.VerticalSpeed,
		GPSAltitude:        r.GPSAltitude,
		BarometricAltitude: r.BarometricAltitude,
		Heading:            r.Heading,
		Bearing:            r.Bearing,
		WindSpeed:          r.WindSpeed,
		WindDirection:      r.WindDirection,
		GPSAccuracy:        r.GPSAccuracy,
		Satellites:         r.Satellites,
		Pressure:           r.Pressure,
		Temperature:        r.Temperature,
		Timestamp:          r.Timestamp.UTC(),
	}, true
}

func trackingUserID(c *gin.Context) (uuid.UUID, bool) {
	value, ok := c.Get(middleware.AuthenticatedUserIDKey)
	if !ok {
		return uuid.Nil, false
	}
	userID, ok := value.(uuid.UUID)
	return userID, ok && userID != uuid.Nil
}

func respondTrackingError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidTrackingData):
		respondError(c, http.StatusBadRequest, "invalid tracking data")
	case errors.Is(err, service.ErrTrackingNotFound):
		respondError(c, http.StatusNotFound, "tracking session not found")
	default:
		respondInternalError(c)
	}
}
