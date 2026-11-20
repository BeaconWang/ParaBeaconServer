package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/middleware"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/service"
)

const maxSessionMetricRequestBytes = 32 * 1024

type SessionMetricIngestor interface {
	Ingest(ctx context.Context, event *model.SessionMetricEvent) error
}

type SessionMetricsHandler struct {
	ingestor        SessionMetricIngestor
	hardwareLimiter *middleware.FixedWindowLimiter
	sessionLimiter  *middleware.FixedWindowLimiter
}

type sessionMetricRequest struct {
	EventID         string          `json:"event_id"`
	SessionID       string          `json:"session_id"`
	HardwareID      string          `json:"hardware_id"`
	AppVersion      string          `json:"app_version"`
	UserID          *string         `json:"user_id"`
	RunTimeSec      *int64          `json:"run_time_sec"`
	TotalRuntimeSec *int64          `json:"total_runtime_sec"`
	ClientTimestamp string          `json:"client_timestamp"`
	Diagnostics     json.RawMessage `json:"diagnostics"`
}

func NewSessionMetricsHandler(
	ingestor SessionMetricIngestor,
	hardwareLimiter *middleware.FixedWindowLimiter,
	sessionLimiter *middleware.FixedWindowLimiter,
) *SessionMetricsHandler {
	return &SessionMetricsHandler{
		ingestor:        ingestor,
		hardwareLimiter: hardwareLimiter,
		sessionLimiter:  sessionLimiter,
	}
}

func (h *SessionMetricsHandler) Ingest(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSessionMetricRequestBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var request sessionMetricRequest
	if err := decoder.Decode(&request); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			respondError(c, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			respondError(c, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if request.RunTimeSec == nil {
		respondError(c, http.StatusBadRequest, "missing runtime")
		return
	}

	sessionID, err := uuid.Parse(strings.TrimSpace(request.SessionID))
	if err != nil || sessionID == uuid.Nil {
		respondError(c, http.StatusBadRequest, "invalid session id")
		return
	}
	hardwareID, err := uuid.Parse(strings.TrimSpace(request.HardwareID))
	if err != nil || hardwareID == uuid.Nil {
		respondError(c, http.StatusBadRequest, "invalid hardware id")
		return
	}
	eventID := uuid.New()
	if strings.TrimSpace(request.EventID) != "" {
		eventID, err = uuid.Parse(strings.TrimSpace(request.EventID))
		if err != nil || eventID == uuid.Nil {
			respondError(c, http.StatusBadRequest, "invalid event id")
			return
		}
	}
	clientTimestamp, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(request.ClientTimestamp))
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid client timestamp")
		return
	}

	diagnostics := request.Diagnostics
	if len(diagnostics) == 0 {
		diagnostics = json.RawMessage(`{}`)
	}
	if len(diagnostics) > service.MaxSessionMetricDiagnosticsBytes {
		respondError(c, http.StatusRequestEntityTooLarge, "diagnostics too large")
		return
	}

	clientIP := net.ParseIP(c.ClientIP())
	if clientIP == nil {
		respondError(c, http.StatusBadRequest, "invalid client address")
		return
	}

	var authenticatedUserID *uuid.UUID
	var trustedUserID uuid.UUID
	isAuthenticated := false
	if value, ok := c.Get(middleware.AuthenticatedUserIDKey); ok {
		if userID, valid := value.(uuid.UUID); valid && userID != uuid.Nil {
			trustedUserID = userID
			isAuthenticated = true
		}
	}
	if request.UserID != nil && strings.TrimSpace(*request.UserID) != "" {
		claimedUserID, parseErr := uuid.Parse(strings.TrimSpace(*request.UserID))
		if parseErr != nil || claimedUserID == uuid.Nil {
			respondError(c, http.StatusBadRequest, "invalid user id")
			return
		}
		if isAuthenticated && claimedUserID == trustedUserID {
			authenticatedUserID = &trustedUserID
		}
	}

	if !allowSessionMetricKey(c, h.hardwareLimiter, "hardware:", hardwareID.String()) ||
		!allowSessionMetricKey(c, h.sessionLimiter, "session:", sessionID.String()) {
		return
	}

	event := &model.SessionMetricEvent{
		ID:               uuid.New(),
		EventID:          eventID,
		SessionID:        sessionID,
		HardwareID:       hardwareID,
		UserID:           authenticatedUserID,
		AppVersion:       request.AppVersion,
		ClientIP:         clientIP.String(),
		RunTimeSec:       *request.RunTimeSec,
		TotalRuntimeSec:  request.TotalRuntimeSec,
		ClientTimestamp:  clientTimestamp,
		ServerReceivedAt: time.Now().UTC(),
		Diagnostics:      string(diagnostics),
	}
	if err := h.ingestor.Ingest(c.Request.Context(), event); err != nil {
		if errors.Is(err, service.ErrInvalidSessionMetric) {
			respondError(c, http.StatusBadRequest, "invalid session metric")
			return
		}
		respondError(c, http.StatusInternalServerError, "unable to record session metric")
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "accepted"})
}

func allowSessionMetricKey(
	c *gin.Context,
	limiter *middleware.FixedWindowLimiter,
	prefix string,
	value string,
) bool {
	if limiter == nil {
		return true
	}
	allowed, retryAfter := limiter.AllowKey(prefix + value)
	if allowed {
		return true
	}
	c.Header("Retry-After", strconv.Itoa(retryAfter))
	respondError(c, http.StatusTooManyRequests, "too many requests")
	return false
}
