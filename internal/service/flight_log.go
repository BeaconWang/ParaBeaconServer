package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
)

var ErrInvalidFlightLog = errors.New("invalid flight log")

const (
	MaxFlightLogBatchSize = 50
	MaxFlightLogBytes     = 15 << 20
	FlightLogPageSize     = 10
)

type FlightLogRepository interface {
	Import(ctx context.Context, userID uuid.UUID, logs []model.FlightLog) (int, int, error)
	List(ctx context.Context, userID, after uuid.UUID, limit int) ([]model.FlightLog, bool, error)
}

type FlightLogService struct {
	logs FlightLogRepository
}

func NewFlightLogService(logs FlightLogRepository) *FlightLogService {
	return &FlightLogService{logs: logs}
}

func (s *FlightLogService) Import(ctx context.Context, userID uuid.UUID, flights []json.RawMessage) (int, int, error) {
	if userID == uuid.Nil || len(flights) > MaxFlightLogBatchSize {
		return 0, 0, ErrInvalidFlightLog
	}
	logs := make([]model.FlightLog, 0, len(flights))
	for _, flight := range flights {
		if len(flight) == 0 || len(flight) > MaxFlightLogBytes {
			return 0, 0, ErrInvalidFlightLog
		}
		var data map[string]json.RawMessage
		decoder := json.NewDecoder(bytes.NewReader(flight))
		decoder.UseNumber()
		if err := decoder.Decode(&data); err != nil || data == nil {
			return 0, 0, ErrInvalidFlightLog
		}
		start, ok := readFlightTime(data["startTime"])
		if !ok {
			return 0, 0, ErrInvalidFlightLog
		}
		end, ok := readFlightTime(data["endTime"])
		if !ok || end.Before(start) || end.After(time.Now().UTC().Add(5*time.Minute)) {
			return 0, 0, ErrInvalidFlightLog
		}
		var pointCount int
		if err := json.Unmarshal(data["pointCount"], &pointCount); err != nil || pointCount < 0 || pointCount > 1_000_000 {
			return 0, 0, ErrInvalidFlightLog
		}
		if samples, exists := data["samples"]; exists {
			var points []json.RawMessage
			if err := json.Unmarshal(samples, &points); err != nil || len(points) > 1_000_000 {
				return 0, 0, ErrInvalidFlightLog
			}
		}
		logs = append(logs, model.FlightLog{
			StartedAt: start,
			PointCount: pointCount,
			Data: string(flight),
		})
	}
	return s.logs.Import(ctx, userID, logs)
}

func (s *FlightLogService) List(ctx context.Context, userID, after uuid.UUID) ([]model.FlightLog, bool, error) {
	if userID == uuid.Nil {
		return nil, false, ErrInvalidFlightLog
	}
	return s.logs.List(ctx, userID, after, FlightLogPageSize)
}

func readFlightTime(raw json.RawMessage) (time.Time, bool) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return time.Time{}, false
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		parsed, err := time.Parse(time.RFC3339Nano, text)
		return parsed.UTC(), err == nil
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return time.Time{}, false
	}
	value, err := strconv.ParseFloat(number.String(), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > 8.64e15 {
		return time.Time{}, false
	}
	milliseconds := value
	if math.Abs(value) < 100_000_000_000 {
		milliseconds *= 1000
	}
	if math.Abs(milliseconds) > 8.64e15 {
		return time.Time{}, false
	}
	return time.UnixMilli(int64(math.Round(milliseconds))).UTC(), true
}
