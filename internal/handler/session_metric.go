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

type InstalledClientReader interface {
	ListInstalledClients(ctx context.Context, limit int) ([]model.InstalledClient, error)
	GetInstalledClient(ctx context.Context, hardwareID uuid.UUID, eventLimit int) (*model.InstalledClientDetail, error)
}

type SessionMetricsHandler struct {
	ingestor        SessionMetricIngestor
	reader          InstalledClientReader
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
	reader, _ := ingestor.(InstalledClientReader)
	return &SessionMetricsHandler{
		ingestor:        ingestor,
		reader:          reader,
		hardwareLimiter: hardwareLimiter,
		sessionLimiter:  sessionLimiter,
	}
}

func (h *SessionMetricsHandler) ListInstalledClients(c *gin.Context) {
	if h.reader == nil {
		respondError(c, http.StatusNotImplemented, "installed client inventory unavailable")
		return
	}
	limit, ok := queryLimit(c, "limit", 100, 200)
	if !ok {
		return
	}
	clients, err := h.reader.ListInstalledClients(c.Request.Context(), limit)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "unable to load installed clients")
		return
	}
	c.JSON(http.StatusOK, gin.H{"clients": clients, "count": len(clients)})
}

func (h *SessionMetricsHandler) GetInstalledClient(c *gin.Context) {
	if h.reader == nil {
		respondError(c, http.StatusNotImplemented, "installed client inventory unavailable")
		return
	}
	hardwareID, err := uuid.Parse(strings.TrimSpace(c.Param("hardware_id")))
	if err != nil || hardwareID == uuid.Nil {
		respondError(c, http.StatusBadRequest, "invalid hardware id")
		return
	}
	eventLimit, ok := queryLimit(c, "event_limit", 100, 200)
	if !ok {
		return
	}
	client, err := h.reader.GetInstalledClient(c.Request.Context(), hardwareID, eventLimit)
	if errors.Is(err, service.ErrInstalledClientNotFound) {
		respondError(c, http.StatusNotFound, "installed client not found")
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "unable to load installed client")
		return
	}
	c.JSON(http.StatusOK, client)
}

func queryLimit(c *gin.Context, name string, fallback, maximum int) (int, bool) {
	value := strings.TrimSpace(c.Query(name))
	if value == "" {
		return fallback, true
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > maximum {
		respondError(c, http.StatusBadRequest, name+" must be between 1 and "+strconv.Itoa(maximum))
		return 0, false
	}
	return limit, true
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
