package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/middleware"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/repository"
	"github.com/parabeacon/server/internal/service"
)

const (
	maxFlightLogRequestBytes = 25 << 20
	maxFlightLogSyncBytes    = 100 << 20
)

type FlightLogService interface {
	Import(ctx context.Context, userID uuid.UUID, flights []json.RawMessage) (int, int, error)
	Sync(ctx context.Context, userID uuid.UUID, flights []json.RawMessage) (int, error)
	BeginSync(ctx context.Context, userID uuid.UUID, expectedCount int) (uuid.UUID, error)
	UploadSyncBatch(ctx context.Context, userID, syncID uuid.UUID, batchIndex int, flights []json.RawMessage) error
	CommitSync(ctx context.Context, userID, syncID uuid.UUID) (int, error)
	List(ctx context.Context, userID, after uuid.UUID) ([]model.FlightLog, bool, error)
}

type FlightLogHandler struct {
	logs FlightLogService
}

func NewFlightLogHandler(logs FlightLogService) *FlightLogHandler {
	return &FlightLogHandler{logs: logs}
}

func (h *FlightLogHandler) Import(c *gin.Context) {
	userID, ok := flightLogUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxFlightLogRequestBytes)
	var request struct {
		Flights []json.RawMessage `json:"flights"`
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
	if len(request.Flights) > service.MaxFlightLogBatchSize {
		respondError(c, http.StatusBadRequest, "too many flight logs")
		return
	}
	accepted, skipped, err := h.logs.Import(c.Request.Context(), userID, request.Flights)
	if errors.Is(err, service.ErrInvalidFlightLog) {
		respondError(c, http.StatusBadRequest, "invalid flight log")
		return
	}
	if err != nil {
		respondInternalError(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{"accepted": accepted, "skipped": skipped})
}

func (h *FlightLogHandler) BeginSync(c *gin.Context) {
	userID, ok := flightLogUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var request struct {
		ExpectedCount *int `json:"expected_count"`
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.ExpectedCount == nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	syncID, err := h.logs.BeginSync(c.Request.Context(), userID, *request.ExpectedCount)
	if err != nil {
		if handleFlightLogSyncError(c, err) {
			return
		}
		respondInternalError(c)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"sync_id": syncID.String()})
}

func (h *FlightLogHandler) UploadSyncBatch(c *gin.Context) {
	userID, ok := flightLogUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	syncID, err := uuid.Parse(c.Param("sync_id"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid sync id")
		return
	}
	batchIndex, err := strconv.Atoi(c.Param("batch_index"))
	if err != nil || batchIndex < 0 {
		respondError(c, http.StatusBadRequest, "invalid batch index")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxFlightLogSyncBatchBytes+(1<<20))
	var request struct {
		Flights []json.RawMessage `json:"flights"`
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
	if request.Flights == nil || len(request.Flights) == 0 {
		respondError(c, http.StatusBadRequest, "missing flight records")
		return
	}
	if err := h.logs.UploadSyncBatch(c.Request.Context(), userID, syncID, batchIndex, request.Flights); err != nil {
		if handleFlightLogSyncError(c, err) {
			return
		}
		respondInternalError(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{"accepted": len(request.Flights), "batch_index": batchIndex})
}

func (h *FlightLogHandler) CommitSync(c *gin.Context) {
	userID, ok := flightLogUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	syncID, err := uuid.Parse(c.Param("sync_id"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid sync id")
		return
	}
	synced, err := h.logs.CommitSync(c.Request.Context(), userID, syncID)
	if err != nil {
		if handleFlightLogSyncError(c, err) {
			return
		}
		respondInternalError(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{"synced": synced})
}

func handleFlightLogSyncError(c *gin.Context, err error) bool {
	switch {
	case errors.Is(err, service.ErrInvalidFlightLog):
		respondError(c, http.StatusBadRequest, "invalid flight log sync")
	case errors.Is(err, service.ErrFlightLogSyncNotFound), errors.Is(err, repository.ErrFlightLogSyncNotFound):
		respondError(c, http.StatusNotFound, "flight log sync not found or expired")
	case errors.Is(err, service.ErrFlightLogSyncConflict), errors.Is(err, repository.ErrFlightLogSyncConflict):
		respondError(c, http.StatusConflict, "flight log sync conflict")
	default:
		return false
	}
	return true
}

func (h *FlightLogHandler) Sync(c *gin.Context) {
	userID, ok := flightLogUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxFlightLogSyncBytes)
	var request struct {
		Flights []json.RawMessage `json:"flights"`
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
	if request.Flights == nil {
		respondError(c, http.StatusBadRequest, "missing flight records")
		return
	}
	synced, err := h.logs.Sync(c.Request.Context(), userID, request.Flights)
	if errors.Is(err, service.ErrInvalidFlightLog) {
		respondError(c, http.StatusBadRequest, "invalid flight records")
		return
	}
	if err != nil {
		respondInternalError(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{"synced": synced})
}

func (h *FlightLogHandler) List(c *gin.Context) {
	userID, ok := flightLogUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
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
	if rawLimit := c.Query("limit"); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit != service.FlightLogPageSize {
			respondError(c, http.StatusBadRequest, "invalid page size")
			return
		}
	}
	logs, hasMore, err := h.logs.List(c.Request.Context(), userID, after)
	if err != nil {
		if errors.Is(err, service.ErrInvalidFlightLog) {
			respondError(c, http.StatusBadRequest, "invalid request")
		} else {
			respondInternalError(c)
		}
		return
	}
	flights := make([]gin.H, 0, len(logs))
	for _, log := range logs {
		flights = append(flights, gin.H{"id": log.ID, "flight": json.RawMessage(log.Data)})
	}
	nextCursor := ""
	if hasMore && len(logs) > 0 {
		nextCursor = logs[len(logs)-1].ID.String()
	}
	c.JSON(http.StatusOK, gin.H{"flights": flights, "next_cursor": nextCursor})
}

func flightLogUserID(c *gin.Context) (uuid.UUID, bool) {
	value, ok := c.Get(middleware.AuthenticatedUserIDKey)
	if !ok {
		return uuid.Nil, false
	}
	userID, ok := value.(uuid.UUID)
	return userID, ok && userID != uuid.Nil
}
