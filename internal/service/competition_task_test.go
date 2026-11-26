package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
)

type competitionTaskTestRepository struct {
	userID           uuid.UUID
	taskData         string
	updatedAt        time.Time
	tasks            []model.CompetitionTaskListEntry
	currentTaskOwner *uuid.UUID
	deletedUserID    uuid.UUID
}

func (r *competitionTaskTestRepository) Replace(_ context.Context, userID uuid.UUID, taskData string, updatedAt time.Time) error {
	r.userID, r.taskData, r.updatedAt = userID, taskData, updatedAt
	return nil
}

func (r *competitionTaskTestRepository) DeleteOwnTask(_ context.Context, userID uuid.UUID) error {
	r.deletedUserID = userID
	return nil
}

func (r *competitionTaskTestRepository) ListAll(context.Context) ([]model.CompetitionTaskListEntry, error) {
	return r.tasks, nil
}

func (r *competitionTaskTestRepository) CurrentTaskOwner(context.Context, uuid.UUID) (*uuid.UUID, error) {
	return r.currentTaskOwner, nil
}

func (r *competitionTaskTestRepository) SetCurrentTaskOwner(_ context.Context, _ uuid.UUID, ownerID *uuid.UUID) error {
	r.currentTaskOwner = ownerID
	return nil
}

func TestCompetitionTaskServiceReplacesAndListsAllServerTasks(t *testing.T) {
	repository := &competitionTaskTestRepository{}
	tasks := NewCompetitionTaskService(repository)
	userID := uuid.New()
	raw := json.RawMessage(`{"taskType":"CLASSIC","version":1,"name":"Saturday","turnpoints":[{"radius":400,"waypoint":{"lat":47.1,"lon":8.5}}]}`)

	updatedAt, err := tasks.Replace(context.Background(), userID, raw)
	if err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	if repository.userID != userID || repository.taskData != string(raw) || !repository.updatedAt.Equal(updatedAt) {
		t.Fatalf("stored task user=%s data=%s updated=%s", repository.userID, repository.taskData, repository.updatedAt)
	}

	other := model.CompetitionTaskListEntry{
		UserID: uuid.New(), TaskData: string(raw), UpdatedAt: updatedAt,
		Username: "pilot-two", DisplayName: "Second Pilot",
	}
	repository.tasks = []model.CompetitionTaskListEntry{{
		UserID: userID, TaskData: string(raw), UpdatedAt: updatedAt,
		Username: "pilot-one", DisplayName: "First Pilot",
	}, other}
	listed, err := tasks.ListAll(context.Background())
	if err != nil || len(listed) != 2 {
		t.Fatalf("ListAll() = %d tasks, error = %v", len(listed), err)
	}
	if listed[0].Username != "pilot-one" || listed[0].DisplayName != "First Pilot" {
		t.Fatalf("ListAll() owner = %q / %q", listed[0].Username, listed[0].DisplayName)
	}
}

func TestCompetitionTaskServiceDeletesOwnTask(t *testing.T) {
	repository := &competitionTaskTestRepository{}
	service := NewCompetitionTaskService(repository)
	userID := uuid.New()
	if err := service.DeleteOwnTask(context.Background(), userID); err != nil {
		t.Fatalf("DeleteOwnTask() error = %v", err)
	}
	if repository.deletedUserID != userID {
		t.Fatalf("deleted user ID = %s, want %s", repository.deletedUserID, userID)
	}
	if err := service.DeleteOwnTask(context.Background(), uuid.Nil); err != ErrInvalidCompetitionTask {
		t.Fatalf("DeleteOwnTask() with nil user error = %v", err)
	}
}

func TestCompetitionTaskServiceSelectsAndClearsCurrentTask(t *testing.T) {
	repository := &competitionTaskTestRepository{}
	service := NewCompetitionTaskService(repository)
	userID, taskOwnerID := uuid.New(), uuid.New()
	ctx := context.Background()

	if err := service.SetCurrentTaskOwner(ctx, userID, &taskOwnerID); err != nil {
		t.Fatalf("SetCurrentTaskOwner(select) error = %v", err)
	}
	selected, err := service.CurrentTaskOwner(ctx, userID)
	if err != nil || selected == nil || *selected != taskOwnerID {
		t.Fatalf("CurrentTaskOwner() = %v, error = %v", selected, err)
	}
	if err := service.SetCurrentTaskOwner(ctx, userID, nil); err != nil {
		t.Fatalf("SetCurrentTaskOwner(clear) error = %v", err)
	}
	selected, err = service.CurrentTaskOwner(ctx, userID)
	if err != nil || selected != nil {
		t.Fatalf("CurrentTaskOwner() after clear = %v, error = %v", selected, err)
	}
	if err := service.SetCurrentTaskOwner(ctx, uuid.Nil, &taskOwnerID); err != ErrInvalidCompetitionTask {
		t.Fatalf("SetCurrentTaskOwner() with nil user error = %v", err)
	}
}

func TestCompetitionTaskServiceRejectsInvalidTasks(t *testing.T) {
	repository := &competitionTaskTestRepository{}
	tasks := NewCompetitionTaskService(repository)
	valid := `{"taskType":"CLASSIC","version":1,"turnpoints":[{"radius":400,"waypoint":{"lat":47.1,"lon":8.5}}]}`
	invalid := []string{
		`{}`,
		`{"taskType":"CLASSIC","version":1,"turnpoints":[]}`,
		`{"taskType":"CLASSIC","version":1,"turnpoints":[{"radius":0,"waypoint":{"lat":47.1,"lon":8.5}}]}`,
		`{"taskType":"CLASSIC","version":1,"turnpoints":[{"radius":400,"waypoint":{"lat":91,"lon":8.5}}]}`,
		`{"taskType":"CLASSIC","version":1,"turnpoints":[{"radius":400,"waypoint":{}}]}`,
		`{"taskType":"CLASSIC","version":1,"turnpoints":[{"radius":400,"waypoint":{"lat":47.1,"lon":8.5,"altSmoothed":100001}}]}`,
		`{"taskType":"OTHER","version":1,"turnpoints":[{"radius":400,"waypoint":{"lat":47.1,"lon":8.5}}]}`,
	}
	for _, raw := range invalid {
		if _, err := tasks.Replace(context.Background(), uuid.New(), json.RawMessage(raw)); err != ErrInvalidCompetitionTask {
			t.Errorf("Replace(%s) error = %v, want ErrInvalidCompetitionTask", raw, err)
		}
	}
	if _, err := tasks.Replace(context.Background(), uuid.Nil, json.RawMessage(valid)); err != ErrInvalidCompetitionTask {
		t.Errorf("Replace() with empty user ID error = %v", err)
	}
	if len(repository.taskData) != 0 {
		t.Fatalf("invalid task reached repository: %s", repository.taskData)
	}
}
