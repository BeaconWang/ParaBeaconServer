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
	userID    uuid.UUID
	taskData  string
	updatedAt time.Time
	tasks     []model.CompetitionTask
}

func (r *competitionTaskTestRepository) Replace(_ context.Context, userID uuid.UUID, taskData string, updatedAt time.Time) error {
	r.userID, r.taskData, r.updatedAt = userID, taskData, updatedAt
	return nil
}

func (r *competitionTaskTestRepository) ListAll(context.Context) ([]model.CompetitionTask, error) {
	return r.tasks, nil
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

	other := model.CompetitionTask{UserID: uuid.New(), TaskData: string(raw), UpdatedAt: updatedAt}
	repository.tasks = []model.CompetitionTask{{UserID: userID, TaskData: string(raw), UpdatedAt: updatedAt}, other}
	listed, err := tasks.ListAll(context.Background())
	if err != nil || len(listed) != 2 {
		t.Fatalf("ListAll() = %d tasks, error = %v", len(listed), err)
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
