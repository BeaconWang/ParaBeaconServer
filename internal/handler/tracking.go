package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/middleware"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/service"
)

type TrackingService interface {
	StartSession(ctx context.Context, userID uuid.UUID, pilotName string, startedAt time.Time, publiclyShare bool) (*model.LiveTrackSession, error)
	StorePoints(ctx context.Context, userID, sessionID uuid.UUID, points []model.LiveTrackPoint) (int64, error)
	StopSession(ctx context.Context, userID, sessionID uuid.UUID, endedAt time.Time) error
	ListActiveSessions(ctx context.Context, userID uuid.UUID) ([]model.LiveTrackSession, error)
	SetPublicSharing(ctx context.Context, userID, sessionID uuid.UUID, enabled bool) error
	ListPublicLocations(ctx context.Context, userID, taskOwnerID uuid.UUID, south, north, west, east float64, after uuid.UUID, includeOffline bool) ([]model.PublicPilotLocation, error)
}

type TrackingPointObserver interface {
	ObserveTrackingPoints(userID uuid.UUID, points []model.LiveTrackPoint)
}

type TrackingSessionObserver interface {
	PublicSharingChanged(ownerID, sessionID uuid.UUID, enabled bool)
	LiveSessionEnded(ownerID, sessionID uuid.UUID)
}

type TrackingHandler struct {
	tracking        TrackingService
	observer        TrackingPointObserver
	sessionObserver TrackingSessionObserver
}

func NewTrackingHandler(tracking TrackingService, observers ...TrackingPointObserver) *TrackingHandler {
	handler := &TrackingHandler{tracking: tracking}
	if len(observers) > 0 {
		handler.observer = observers[0]
		handler.sessionObserver, _ = observers[0].(TrackingSessionObserver)
	}
	return handler
}

type startTrackingRequest struct {
	PilotName     string    `json:"pilot_name"`
	StartTime     time.Time `json:"start_time"`
	PubliclyShare bool      `json:"publicly_share"`
}

type publicSharingRequest struct {
	PubliclyShare *bool `json:"publicly_share"`
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

func (h *TrackingHandler) ListPublicLocations(c *gin.Context) {
	userID, _ := trackingUserID(c)
	parseBound := func(key string) (float64, bool) {
		value, err := strconv.ParseFloat(c.Query(key), 64)
		return value, err == nil
	}
	west, westOK := parseBound("west")
	south, southOK := parseBound("south")
	east, eastOK := parseBound("east")
	north, northOK := parseBound("north")
	if !westOK || !southOK || !eastOK || !northOK {
		respondError(c, http.StatusBadRequest, "invalid map bounds")
		return
	}
	var taskOwnerID uuid.UUID
	if rawTaskOwnerID := c.Query("task_owner_id"); rawTaskOwnerID != "" {
		parsed, err := uuid.Parse(rawTaskOwnerID)
		if err != nil || parsed == uuid.Nil {
			respondError(c, http.StatusBadRequest, "invalid task owner")
			return
		}
		taskOwnerID = parsed
	}
	var after uuid.UUID
	if cursor := c.Query("cursor"); cursor != "" {
		parsed, err := uuid.Parse(cursor)
		if err != nil {
			respondError(c, http.StatusBadRequest, "invalid cursor")
			return
		}
		after = parsed
	}
	includeOffline := false
	if raw := c.Query("show_offline"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			respondError(c, http.StatusBadRequest, "invalid offline-location setting")
			return
		}
		includeOffline = parsed
	}
	locations, err := h.tracking.ListPublicLocations(c.Request.Context(), userID, taskOwnerID, south, north, west, east, after, includeOffline)
	if err != nil {
		respondTrackingError(c, err)
		return
	}
	nextCursor := ""
	if len(locations) == service.PublicLocationPageSize {
		nextCursor = locations[len(locations)-1].SessionID.String()
	}
	c.JSON(http.StatusOK, gin.H{"locations": locations, "next_cursor": nextCursor})
}

func (h *TrackingHandler) SetPublicSharing(c *gin.Context) {
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
	var request publicSharingRequest
	if err := c.ShouldBindJSON(&request); err != nil || request.PubliclyShare == nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.tracking.SetPublicSharing(c.Request.Context(), userID, sessionID, *request.PubliclyShare); err != nil {
		respondTrackingError(c, err)
		return
	}
	if h.sessionObserver != nil {
		h.sessionObserver.PublicSharingChanged(userID, sessionID, *request.PubliclyShare)
	}
	c.Status(http.StatusNoContent)
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
	session, err := h.tracking.StartSession(c.Request.Context(), userID, request.PilotName, request.StartTime, request.PubliclyShare)
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
	if h.observer != nil {
		h.observer.ObserveTrackingPoints(userID, points)
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
	if h.sessionObserver != nil {
		h.sessionObserver.LiveSessionEnded(userID, sessionID)
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
