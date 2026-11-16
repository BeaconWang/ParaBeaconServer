package service

import (
	"context"
	"encoding/json"
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
