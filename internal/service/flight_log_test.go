package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
)

type flightLogTestRepository struct {
	userID         uuid.UUID
	logs           []model.FlightLog
	limit          int
	replaceCall    int
	syncs          map[uuid.UUID]*model.FlightLogSync
	batches        map[uuid.UUID]map[int][]model.FlightLog
	hashes         map[uuid.UUID]map[int]string
	archiveRecords map[uuid.UUID]map[int]model.FlightLogSyncRecord
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
		r.archiveRecords = make(map[uuid.UUID]map[int]model.FlightLogSyncRecord)
	}
	copy := *syncSession
	r.syncs[syncSession.ID] = &copy
	r.batches[syncSession.ID] = make(map[int][]model.FlightLog)
	r.hashes[syncSession.ID] = make(map[int]string)
	r.archiveRecords[syncSession.ID] = make(map[int]model.FlightLogSyncRecord)
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
	if session.CommittedAt != nil || session.ArchiveMode || batchIndex != session.NextBatchIndex || session.StagedCount+len(logs) > session.ExpectedCount || session.StagedBytes+batchBytes > MaxFlightLogSnapshotBytes {
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

func (r *flightLogTestRepository) StageSyncArchive(_ context.Context, userID, syncID uuid.UUID, flightIndex int, payloadHash string, record *model.FlightLogSyncRecord, archiveBytes int64, now time.Time) (uuid.UUID, error) {
	session := r.syncs[syncID]
	if session == nil || session.UserID != userID || !session.ExpiresAt.After(now) {
		return uuid.Nil, ErrFlightLogSyncNotFound
	}
	if previousHash, exists := r.hashes[syncID][flightIndex]; exists {
		if previousHash != payloadHash {
			return uuid.Nil, ErrFlightLogSyncConflict
		}
		return *r.archiveRecords[syncID][flightIndex].ArchiveID, nil
	}
	if flightIndex != session.NextBatchIndex || session.CommittedAt != nil || session.StagedCount >= session.ExpectedCount || session.StagedBytes+archiveBytes > MaxFlightLogSnapshotBytes {
		return uuid.Nil, ErrFlightLogSyncConflict
	}
	for _, previous := range r.archiveRecords[syncID] {
		if previous.StartedAt.Equal(record.StartedAt) && previous.PointCount == record.PointCount {
			return uuid.Nil, ErrFlightLogSyncConflict
		}
	}
	r.archiveRecords[syncID][flightIndex] = *record
	r.hashes[syncID][flightIndex] = payloadHash
	session.ArchiveMode = true
	session.StagedCount++
	session.StagedBytes += archiveBytes
	session.NextBatchIndex++
	return *record.ArchiveID, nil
}

func (r *flightLogTestRepository) SyncArchivePlan(_ context.Context, userID, syncID uuid.UUID, now time.Time) ([]model.FlightLogSyncRecord, int, bool, error) {
	session := r.syncs[syncID]
	if session == nil || session.UserID != userID || !session.ExpiresAt.After(now) {
		return nil, 0, false, ErrFlightLogSyncNotFound
	}
	if session.CommittedAt != nil {
		return nil, session.ExpectedCount, true, nil
	}
	if session.StagedCount != session.ExpectedCount {
		return nil, 0, false, ErrFlightLogSyncConflict
	}
	records := make([]model.FlightLogSyncRecord, session.ExpectedCount)
	for index := 0; index < session.ExpectedCount; index++ {
		record, ok := r.archiveRecords[syncID][index]
		if !ok {
			return nil, 0, false, ErrFlightLogSyncConflict
		}
		records[index] = record
	}
	return records, session.ExpectedCount, false, nil
}

func (r *flightLogTestRepository) CommitSyncArchives(_ context.Context, userID, syncID uuid.UUID, now time.Time) (int, []uuid.UUID, error) {
	session := r.syncs[syncID]
	if session == nil || session.UserID != userID || !session.ExpiresAt.After(now) {
		return 0, nil, ErrFlightLogSyncNotFound
	}
	if session.CommittedAt != nil {
		return session.ExpectedCount, nil, nil
	}
	if session.StagedCount != session.ExpectedCount {
		return 0, nil, ErrFlightLogSyncConflict
	}
	var oldArchives []uuid.UUID
	for _, log := range r.logs {
		if log.ArchiveID != nil {
			oldArchives = append(oldArchives, *log.ArchiveID)
		}
	}
	logs := make([]model.FlightLog, session.ExpectedCount)
	for index := 0; index < session.ExpectedCount; index++ {
		record, ok := r.archiveRecords[syncID][index]
		if !ok || record.ArchiveID == nil {
			return 0, nil, ErrFlightLogSyncConflict
		}
		logs[index] = model.FlightLog{
			ID:         *record.ArchiveID,
			UserID:     userID,
			StartedAt:  record.StartedAt,
			PointCount: record.PointCount,
			Data:       "null",
			ArchiveID:  record.ArchiveID,
		}
	}
	r.userID, r.logs = userID, logs
	r.replaceCall++
	committedAt := now
	session.CommittedAt = &committedAt
	return len(logs), oldArchives, nil
}

func (r *flightLogTestRepository) CommitSync(_ context.Context, userID, syncID uuid.UUID, now time.Time) (int, error) {
	session := r.syncs[syncID]
	if session == nil || session.UserID != userID || !session.ExpiresAt.After(now) {
		return 0, ErrFlightLogSyncNotFound
	}
	if session.CommittedAt != nil {
		return session.ExpectedCount, nil
	}
	if session.ArchiveMode {
		return 0, ErrFlightLogSyncConflict
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
	return r.logs, false, nil
}

func makeFlightPackage(t *testing.T, flightJSON []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	entry, err := archive.Create("flight.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(flightJSON); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestFlightArchiveSyncStoresGuidNamedZipAndListsFlightJSON(t *testing.T) {
	storage, err := NewFlightArchiveStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewFlightArchiveStorage() error = %v", err)
	}
	repository := &flightLogTestRepository{}
	logs := NewFlightLogService(repository, storage)
	userID := uuid.New()
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	flightJSON, err := json.Marshal(map[string]any{
		"startTime":  start.UnixMilli(),
		"endTime":    start.Add(time.Minute).UnixMilli(),
		"pointCount": 2,
		"samples":    []any{},
	})
	if err != nil {
		t.Fatalf("marshal flight: %v", err)
	}
	syncID, err := logs.BeginSync(context.Background(), userID, 1)
	if err != nil {
		t.Fatalf("BeginSync() error = %v", err)
	}
	flightPackage := makeFlightPackage(t, flightJSON)
	archiveID, err := logs.UploadSyncFlightArchive(context.Background(), userID, syncID, 0, bytes.NewReader(flightPackage))
	if err != nil {
		t.Fatalf("UploadSyncFlightArchive() error = %v", err)
	}
	retryArchiveID, err := logs.UploadSyncFlightArchive(context.Background(), userID, syncID, 0, bytes.NewReader(flightPackage))
	if err != nil || retryArchiveID != archiveID {
		t.Fatalf("idempotent archive upload = %s, %v; want %s", retryArchiveID, err, archiveID)
	}
	if _, err := os.Stat(storage.archivePath(archiveID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("archive became visible before sync commit: stat error = %v", err)
	}

	synced, err := logs.CommitSync(context.Background(), userID, syncID)
	if err != nil || synced != 1 {
		t.Fatalf("CommitSync() = %d, %v; want 1, nil", synced, err)
	}
	stored := repository.logs
	if len(stored) != 1 || stored[0].ArchiveID == nil || *stored[0].ArchiveID != archiveID || stored[0].ID != archiveID || stored[0].Data != "null" {
		t.Fatalf("stored flight log = %#v", stored)
	}
	if filepath.Base(storage.archivePath(archiveID)) != archiveID.String()+".flight.zip" {
		t.Fatalf("archive path = %q", storage.archivePath(archiveID))
	}
	storedJSON, err := storage.Read(archiveID)
	if err != nil || !bytes.Equal(storedJSON, flightJSON) {
		t.Fatalf("stored archive flight data differs: error = %v", err)
	}
	listed, _, err := logs.List(context.Background(), userID, uuid.Nil)
	if err != nil || len(listed) != 1 || listed[0].Data != string(flightJSON) {
		t.Fatalf("List() archive hydration = %#v, %v", listed, err)
	}

	updatedFlightJSON, err := json.Marshal(map[string]any{
		"startTime": start.UnixMilli(), "endTime": start.Add(time.Minute).UnixMilli(), "pointCount": 2, "pilotName": "updated",
	})
	if err != nil {
		t.Fatalf("marshal updated flight: %v", err)
	}
	nextSyncID, err := logs.BeginSync(context.Background(), userID, 1)
	if err != nil {
		t.Fatalf("BeginSync(replacement) error = %v", err)
	}
	newArchiveID, err := logs.UploadSyncFlightArchive(context.Background(), userID, nextSyncID, 0, bytes.NewReader(makeFlightPackage(t, updatedFlightJSON)))
	if err != nil {
		t.Fatalf("UploadSyncFlightArchive(replacement) error = %v", err)
	}
	if synced, err := logs.CommitSync(context.Background(), userID, nextSyncID); err != nil || synced != 1 {
		t.Fatalf("CommitSync(replacement) = %d, %v; want 1, nil", synced, err)
	}
	if len(repository.logs) != 1 || repository.logs[0].ArchiveID == nil || *repository.logs[0].ArchiveID != newArchiveID {
		t.Fatalf("replacement created duplicate flight rows: %#v", repository.logs)
	}
	if _, err := os.Stat(storage.archivePath(archiveID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replaced archive was not removed: stat error = %v", err)
	}
	listed, _, err = logs.List(context.Background(), userID, uuid.Nil)
	if err != nil || len(listed) != 1 || listed[0].Data != string(updatedFlightJSON) {
		t.Fatalf("updated List() archive hydration = %#v, %v", listed, err)
	}
}

func TestFlightArchiveSyncRejectsMalformedOrDuplicateFlightPackages(t *testing.T) {
	storage, err := NewFlightArchiveStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewFlightArchiveStorage() error = %v", err)
	}
	repository := &flightLogTestRepository{}
	logs := NewFlightLogService(repository, storage)
	userID := uuid.New()
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	flightJSON, _ := json.Marshal(map[string]any{
		"startTime": start.UnixMilli(), "endTime": start.Add(time.Minute).UnixMilli(), "pointCount": 2,
	})
	syncID, err := logs.BeginSync(context.Background(), userID, 2)
	if err != nil {
		t.Fatalf("BeginSync() error = %v", err)
	}
	packageBytes := makeFlightPackage(t, flightJSON)
	if _, err := logs.UploadSyncFlightArchive(context.Background(), userID, syncID, 0, bytes.NewReader([]byte("not a zip"))); !errors.Is(err, ErrInvalidFlightArchive) {
		t.Fatalf("invalid archive error = %v, want ErrInvalidFlightArchive", err)
	}
	if _, err := logs.UploadSyncFlightArchive(context.Background(), userID, syncID, 0, bytes.NewReader(packageBytes)); err != nil {
		t.Fatalf("UploadSyncFlightArchive(0) error = %v", err)
	}
	if _, err := logs.UploadSyncFlightArchive(context.Background(), userID, syncID, 1, bytes.NewReader(packageBytes)); !errors.Is(err, ErrFlightLogSyncConflict) {
		t.Fatalf("duplicate flight identity error = %v, want conflict", err)
	}
	if repository.replaceCall != 0 {
		t.Fatal("invalid/duplicate archive changed the live snapshot")
	}
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
