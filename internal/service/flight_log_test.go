package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
)

type flightLogTestRepository struct {
	userID      uuid.UUID
	logs        []model.FlightLog
	limit       int
	replaceCall int
	syncs       map[uuid.UUID]*model.FlightLogSync
	batches     map[uuid.UUID]map[int][]model.FlightLog
	hashes      map[uuid.UUID]map[int]string
}

func (r *flightLogTestRepository) Import(_ context.Context, userID uuid.UUID, logs []model.FlightLog) (int, int, error) {
	r.userID, r.logs = userID, logs
	return len(logs), 0, nil
}

func (r *flightLogTestRepository) Replace(_ context.Context, userID uuid.UUID, logs []model.FlightLog) error {
	r.userID, r.logs = userID, logs
	r.replaceCall++
	return nil
}

func (r *flightLogTestRepository) BeginSync(_ context.Context, syncSession *model.FlightLogSync) error {
	if r.syncs == nil {
		r.syncs = make(map[uuid.UUID]*model.FlightLogSync)
		r.batches = make(map[uuid.UUID]map[int][]model.FlightLog)
		r.hashes = make(map[uuid.UUID]map[int]string)
	}
	copy := *syncSession
	r.syncs[syncSession.ID] = &copy
	r.batches[syncSession.ID] = make(map[int][]model.FlightLog)
	r.hashes[syncSession.ID] = make(map[int]string)
	return nil
}

func (r *flightLogTestRepository) StageSyncBatch(_ context.Context, userID, syncID uuid.UUID, batchIndex int, payloadHash string, logs []model.FlightLog, batchBytes int64, now time.Time) error {
	session := r.syncs[syncID]
	if session == nil || session.UserID != userID || !session.ExpiresAt.After(now) {
		return ErrFlightLogSyncNotFound
	}
	if previous, exists := r.hashes[syncID][batchIndex]; exists {
		if previous == payloadHash {
			return nil
		}
		return ErrFlightLogSyncConflict
	}
	if session.CommittedAt != nil || batchIndex != session.NextBatchIndex || session.StagedCount+len(logs) > session.ExpectedCount || session.StagedBytes+batchBytes > MaxFlightLogSnapshotBytes {
		return ErrFlightLogSyncConflict
	}
	for _, batch := range r.batches[syncID] {
		for _, previous := range batch {
			for _, current := range logs {
				if previous.StartedAt.Equal(current.StartedAt) && previous.PointCount == current.PointCount {
					return ErrFlightLogSyncConflict
				}
			}
		}
	}
	r.batches[syncID][batchIndex] = append([]model.FlightLog(nil), logs...)
	r.hashes[syncID][batchIndex] = payloadHash
	session.StagedCount += len(logs)
	session.StagedBytes += batchBytes
	session.NextBatchIndex++
	return nil
}

func (r *flightLogTestRepository) CommitSync(_ context.Context, userID, syncID uuid.UUID, now time.Time) (int, error) {
	session := r.syncs[syncID]
	if session == nil || session.UserID != userID || !session.ExpiresAt.After(now) {
		return 0, ErrFlightLogSyncNotFound
	}
	if session.CommittedAt != nil {
		return session.ExpectedCount, nil
	}
	if session.StagedCount != session.ExpectedCount || len(r.batches[syncID]) != session.NextBatchIndex {
		return 0, ErrFlightLogSyncConflict
	}
	logs := make([]model.FlightLog, 0, session.ExpectedCount)
	for index := 0; index < session.NextBatchIndex; index++ {
		batch, exists := r.batches[syncID][index]
		if !exists {
			return 0, ErrFlightLogSyncConflict
		}
		logs = append(logs, batch...)
	}
	r.userID = userID
	r.logs = logs
	r.replaceCall++
	committedAt := now
	session.CommittedAt = &committedAt
	return len(logs), nil
}

func (r *flightLogTestRepository) List(_ context.Context, userID, _ uuid.UUID, limit int) ([]model.FlightLog, bool, error) {
	r.userID, r.limit = userID, limit
	return nil, false, nil
}

func TestFlightLogServiceValidatesAndStoresRecords(t *testing.T) {
	repository := &flightLogTestRepository{}
	logs := NewFlightLogService(repository)
	userID := uuid.New()
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	flight, _ := json.Marshal(map[string]any{
		"startTime":  start.UnixMilli(),
		"endTime":    start.Add(time.Minute).UnixMilli(),
		"pointCount": 2,
		"samples":    []any{},
	})
	accepted, skipped, err := logs.Import(context.Background(), userID, []json.RawMessage{flight})
	if err != nil || accepted != 1 || skipped != 0 {
		t.Fatalf("Import() = %d, %d, %v", accepted, skipped, err)
	}
	if repository.userID != userID || len(repository.logs) != 1 || repository.logs[0].PointCount != 2 || repository.logs[0].StartedAt.UnixMilli() != start.UnixMilli() {
		t.Fatalf("stored records = %#v for user %s", repository.logs, repository.userID)
	}
}

func TestFlightLogServiceRejectsInvalidRecords(t *testing.T) {
	repository := &flightLogTestRepository{}
	logs := NewFlightLogService(repository)
	userID := uuid.New()
	for _, flight := range []string{
		`[]`,
		`{"startTime":1,"endTime":0,"pointCount":1}`,
		`{"startTime":1,"endTime":2,"pointCount":-1}`,
		`{"startTime":1,"endTime":2,"pointCount":1,"samples":{}}`,
	} {
		if _, _, err := logs.Import(context.Background(), userID, []json.RawMessage{json.RawMessage(flight)}); err != ErrInvalidFlightLog {
			t.Errorf("Import(%s) error = %v, want ErrInvalidFlightLog", flight, err)
		}
	}
	if len(repository.logs) != 0 {
		t.Fatalf("invalid records reached repository: %#v", repository.logs)
	}
}

func TestFlightLogSyncReplacesAccountSnapshotAndAllowsEmpty(t *testing.T) {
	repository := &flightLogTestRepository{logs: []model.FlightLog{{Data: `{"old":true}`}}}
	logs := NewFlightLogService(repository)
	userID := uuid.New()
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	flight, _ := json.Marshal(map[string]any{
		"startTime":  start.UnixMilli(),
		"endTime":    start.Add(time.Minute).UnixMilli(),
		"pointCount": 2,
		"samples":    []any{},
	})

	synced, err := logs.Sync(context.Background(), userID, []json.RawMessage{flight})
	if err != nil || synced != 1 {
		t.Fatalf("Sync() = %d, %v; want 1, nil", synced, err)
	}
	if repository.userID != userID || repository.replaceCall != 1 || len(repository.logs) != 1 || repository.logs[0].Data != string(flight) {
		t.Fatalf("replacement snapshot = %#v for user %s (%d calls)", repository.logs, repository.userID, repository.replaceCall)
	}

	synced, err = logs.Sync(context.Background(), userID, []json.RawMessage{})
	if err != nil || synced != 0 {
		t.Fatalf("Sync(empty) = %d, %v; want 0, nil", synced, err)
	}
	if repository.replaceCall != 2 || repository.logs == nil || len(repository.logs) != 0 {
		t.Fatalf("empty snapshot was not forwarded for replacement: %#v (%d calls)", repository.logs, repository.replaceCall)
	}
}

func TestFlightLogStagedSyncCommitsOnlyCompleteSnapshots(t *testing.T) {
	repository := &flightLogTestRepository{logs: []model.FlightLog{{Data: `{"keep":true}`}}}
	logs := NewFlightLogService(repository)
	userID := uuid.New()
	firstStart := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	secondStart := firstStart.Add(time.Hour)
	first, _ := json.Marshal(map[string]any{
		"startTime": firstStart.UnixMilli(), "endTime": firstStart.Add(time.Minute).UnixMilli(), "pointCount": 2,
	})
	second, _ := json.Marshal(map[string]any{
		"startTime": secondStart.UnixMilli(), "endTime": secondStart.Add(time.Minute).UnixMilli(), "pointCount": 3,
	})

	syncID, err := logs.BeginSync(context.Background(), userID, 2)
	if err != nil {
		t.Fatalf("BeginSync() error = %v", err)
	}
	if err := logs.UploadSyncBatch(context.Background(), userID, syncID, 0, []json.RawMessage{first}); err != nil {
		t.Fatalf("UploadSyncBatch(0) error = %v", err)
	}
	if err := logs.UploadSyncBatch(context.Background(), userID, syncID, 0, []json.RawMessage{first}); err != nil {
		t.Fatalf("idempotent UploadSyncBatch(0) error = %v", err)
	}
	if _, err := logs.CommitSync(context.Background(), userID, syncID); !errors.Is(err, ErrFlightLogSyncConflict) {
		t.Fatalf("incomplete CommitSync() error = %v, want conflict", err)
	}
	if repository.replaceCall != 0 || len(repository.logs) != 1 || repository.logs[0].Data != `{"keep":true}` {
		t.Fatalf("incomplete snapshot changed live records: %#v", repository.logs)
	}
	if err := logs.UploadSyncBatch(context.Background(), userID, syncID, 1, []json.RawMessage{second}); err != nil {
		t.Fatalf("UploadSyncBatch(1) error = %v", err)
	}
	if synced, err := logs.CommitSync(context.Background(), userID, syncID); err != nil || synced != 2 {
		t.Fatalf("CommitSync() = %d, %v; want 2, nil", synced, err)
	}
	if synced, err := logs.CommitSync(context.Background(), userID, syncID); err != nil || synced != 2 {
		t.Fatalf("idempotent CommitSync() = %d, %v; want 2, nil", synced, err)
	}
	if repository.replaceCall != 1 || len(repository.logs) != 2 {
		t.Fatalf("committed snapshot = %#v (%d replacements)", repository.logs, repository.replaceCall)
	}
}

func TestFlightLogStagedSyncSupportsEmptySnapshot(t *testing.T) {
	repository := &flightLogTestRepository{logs: []model.FlightLog{{Data: `{"old":true}`}}}
	logs := NewFlightLogService(repository)
	userID := uuid.New()
	syncID, err := logs.BeginSync(context.Background(), userID, 0)
	if err != nil {
		t.Fatalf("BeginSync(empty) error = %v", err)
	}
	if synced, err := logs.CommitSync(context.Background(), userID, syncID); err != nil || synced != 0 {
		t.Fatalf("CommitSync(empty) = %d, %v; want 0, nil", synced, err)
	}
	if repository.replaceCall != 1 || repository.logs == nil || len(repository.logs) != 0 {
		t.Fatalf("empty commit did not replace the library: %#v", repository.logs)
	}
}

func TestFlightLogStagedSyncRejectsDuplicateIdentityAcrossBatches(t *testing.T) {
	repository := &flightLogTestRepository{}
	logs := NewFlightLogService(repository)
	userID := uuid.New()
	flight := json.RawMessage(`{"startTime":1700000000000,"endTime":1700000001000,"pointCount":1}`)
	syncID, err := logs.BeginSync(context.Background(), userID, 2)
	if err != nil {
		t.Fatalf("BeginSync() error = %v", err)
	}
	if err := logs.UploadSyncBatch(context.Background(), userID, syncID, 0, []json.RawMessage{flight}); err != nil {
		t.Fatalf("UploadSyncBatch(0) error = %v", err)
	}
	if err := logs.UploadSyncBatch(context.Background(), userID, syncID, 1, []json.RawMessage{flight}); !errors.Is(err, ErrFlightLogSyncConflict) {
		t.Fatalf("duplicate identity error = %v, want conflict", err)
	}
}

func TestFlightLogSyncRejectsInvalidSnapshotBeforeReplacing(t *testing.T) {
	repository := &flightLogTestRepository{logs: []model.FlightLog{{Data: `{"keep":true}`}}}
	logs := NewFlightLogService(repository)
	userID := uuid.New()
	duplicate := json.RawMessage(`{"startTime":1700000000000,"endTime":1700000001000,"pointCount":1}`)
	if _, err := logs.Sync(context.Background(), userID, []json.RawMessage{duplicate, duplicate}); err != ErrInvalidFlightLog {
		t.Fatalf("Sync() duplicate error = %v, want ErrInvalidFlightLog", err)
	}
	if repository.replaceCall != 0 || len(repository.logs) != 1 || repository.logs[0].Data != `{"keep":true}` {
		t.Fatalf("invalid snapshot reached repository: %#v (%d calls)", repository.logs, repository.replaceCall)
	}
}

func TestFlightLogListUsesFixedBoundedPage(t *testing.T) {
	repository := &flightLogTestRepository{}
	logs := NewFlightLogService(repository)
	userID := uuid.New()
	if _, _, err := logs.List(context.Background(), userID, uuid.Nil); err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if repository.userID != userID || repository.limit != FlightLogPageSize {
		t.Fatalf("list user/limit = %s/%d", repository.userID, repository.limit)
	}
}
