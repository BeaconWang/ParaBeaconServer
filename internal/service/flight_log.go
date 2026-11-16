package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
)

var (
	ErrInvalidFlightLog      = errors.New("invalid flight log")
	ErrFlightLogSyncNotFound = errors.New("flight log sync not found or expired")
	ErrFlightLogSyncConflict = errors.New("flight log sync conflict")
)

const (
	MaxFlightLogBatchSize        = 50
	MaxFlightLogSnapshotSize     = 500
	MaxFlightLogSyncBatchRecords = 50
	MaxFlightLogBytes            = 18 << 20
	MaxFlightLogSyncBatchBytes   = 18 << 20
	MaxFlightLogSnapshotBytes    = 512 << 20
	FlightLogSyncLifetime        = 24 * time.Hour
	FlightLogPageSize            = 10
)

type flightLogIdentity struct {
	startedAt  int64
	pointCount int
}

type FlightLogRepository interface {
	Import(ctx context.Context, userID uuid.UUID, logs []model.FlightLog) (int, int, error)
	Replace(ctx context.Context, userID uuid.UUID, logs []model.FlightLog) error
	BeginSync(ctx context.Context, syncSession *model.FlightLogSync) error
	StageSyncBatch(ctx context.Context, userID, syncID uuid.UUID, batchIndex int, payloadHash string, logs []model.FlightLog, batchBytes int64, now time.Time) error
	CommitSync(ctx context.Context, userID, syncID uuid.UUID, now time.Time) (int, error)
	List(ctx context.Context, userID, after uuid.UUID, limit int) ([]model.FlightLog, bool, error)
}

type FlightLogService struct {
	logs FlightLogRepository
}

func NewFlightLogService(logs FlightLogRepository) *FlightLogService {
	return &FlightLogService{logs: logs}
}

func (s *FlightLogService) BeginSync(ctx context.Context, userID uuid.UUID, expectedCount int) (uuid.UUID, error) {
	if userID == uuid.Nil || expectedCount < 0 || expectedCount > MaxFlightLogSnapshotSize {
		return uuid.Nil, ErrInvalidFlightLog
	}
	now := time.Now().UTC()
	session := &model.FlightLogSync{
		ID:            uuid.New(),
		UserID:        userID,
		ExpectedCount: expectedCount,
		ExpiresAt:     now.Add(FlightLogSyncLifetime),
	}
	if err := s.logs.BeginSync(ctx, session); err != nil {
		return uuid.Nil, err
	}
	return session.ID, nil
}

func (s *FlightLogService) UploadSyncBatch(ctx context.Context, userID, syncID uuid.UUID, batchIndex int, flights []json.RawMessage) error {
	if userID == uuid.Nil || syncID == uuid.Nil || batchIndex < 0 || batchIndex >= MaxFlightLogSnapshotSize || len(flights) == 0 || len(flights) > MaxFlightLogSyncBatchRecords {
		return ErrInvalidFlightLog
	}
	logs, err := parseFlightLogs(flights)
	if err != nil {
		return err
	}
	var batchBytes int64
	identities := make(map[flightLogIdentity]struct{}, len(logs))
	for _, log := range logs {
		batchBytes += int64(len(log.Data))
		identity := flightLogIdentity{startedAt: log.StartedAt.UnixMicro(), pointCount: log.PointCount}
		if _, exists := identities[identity]; exists {
			return ErrInvalidFlightLog
		}
		identities[identity] = struct{}{}
	}
	if batchBytes > MaxFlightLogSyncBatchBytes {
		return ErrInvalidFlightLog
	}
	return s.logs.StageSyncBatch(ctx, userID, syncID, batchIndex, hashFlightLogBatch(flights), logs, batchBytes, time.Now().UTC())
}

func (s *FlightLogService) CommitSync(ctx context.Context, userID, syncID uuid.UUID) (int, error) {
	if userID == uuid.Nil || syncID == uuid.Nil {
		return 0, ErrInvalidFlightLog
	}
	return s.logs.CommitSync(ctx, userID, syncID, time.Now().UTC())
}

func hashFlightLogBatch(flights []json.RawMessage) string {
	digest := sha256.New()
	var size [8]byte
	for _, flight := range flights {
		binary.BigEndian.PutUint64(size[:], uint64(len(flight)))
		_, _ = digest.Write(size[:])
		_, _ = digest.Write(flight)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func (s *FlightLogService) Import(ctx context.Context, userID uuid.UUID, flights []json.RawMessage) (int, int, error) {
	if userID == uuid.Nil || len(flights) > MaxFlightLogBatchSize {
		return 0, 0, ErrInvalidFlightLog
	}
	logs, err := parseFlightLogs(flights)
	if err != nil {
		return 0, 0, err
	}
	return s.logs.Import(ctx, userID, logs)
}

func (s *FlightLogService) Sync(ctx context.Context, userID uuid.UUID, flights []json.RawMessage) (int, error) {
	if userID == uuid.Nil || len(flights) > MaxFlightLogSnapshotSize {
		return 0, ErrInvalidFlightLog
	}
	logs, err := parseFlightLogs(flights)
	if err != nil {
		return 0, err
	}
	identities := make(map[flightLogIdentity]struct{}, len(logs))
	var snapshotBytes int64
	for _, log := range logs {
		snapshotBytes += int64(len(log.Data))
		identity := flightLogIdentity{startedAt: log.StartedAt.UnixMicro(), pointCount: log.PointCount}
		if _, exists := identities[identity]; exists {
			return 0, ErrInvalidFlightLog
		}
		identities[identity] = struct{}{}
	}
	if snapshotBytes > MaxFlightLogSnapshotBytes {
		return 0, ErrInvalidFlightLog
	}
	if err := s.logs.Replace(ctx, userID, logs); err != nil {
		return 0, err
	}
	return len(logs), nil
}

func parseFlightLogs(flights []json.RawMessage) ([]model.FlightLog, error) {
	logs := make([]model.FlightLog, 0, len(flights))
	for _, flight := range flights {
		if len(flight) == 0 || len(flight) > MaxFlightLogBytes {
			return nil, ErrInvalidFlightLog
		}
		var data map[string]json.RawMessage
		decoder := json.NewDecoder(bytes.NewReader(flight))
		decoder.UseNumber()
		if err := decoder.Decode(&data); err != nil || data == nil {
			return nil, ErrInvalidFlightLog
		}
		start, ok := readFlightTime(data["startTime"])
		if !ok {
			return nil, ErrInvalidFlightLog
		}
		end, ok := readFlightTime(data["endTime"])
		if !ok || end.Before(start) || end.After(time.Now().UTC().Add(5*time.Minute)) {
			return nil, ErrInvalidFlightLog
		}
		var pointCount int
		if err := json.Unmarshal(data["pointCount"], &pointCount); err != nil || pointCount < 0 || pointCount > 1_000_000 {
			return nil, ErrInvalidFlightLog
		}
		if samples, exists := data["samples"]; exists {
			var points []json.RawMessage
			if err := json.Unmarshal(samples, &points); err != nil || len(points) > 1_000_000 {
				return nil, ErrInvalidFlightLog
			}
		}
		logs = append(logs, model.FlightLog{
			StartedAt:  start,
			PointCount: pointCount,
			Data:       string(flight),
		})
	}
	return logs, nil
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
