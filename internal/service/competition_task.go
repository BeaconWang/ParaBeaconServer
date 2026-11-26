package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
)

var ErrInvalidCompetitionTask = errors.New("invalid competition task")

const (
	MaxCompetitionTaskBytes      = 256 << 10
	MaxCompetitionTaskTurnpoints = 100
)

type CompetitionTaskRepository interface {
	Replace(ctx context.Context, userID uuid.UUID, taskData string, updatedAt time.Time) error
	ListAll(ctx context.Context) ([]model.CompetitionTask, error)
}

type CompetitionTaskService struct {
	tasks CompetitionTaskRepository
}

func NewCompetitionTaskService(tasks CompetitionTaskRepository) *CompetitionTaskService {
	return &CompetitionTaskService{tasks: tasks}
}

func (s *CompetitionTaskService) Replace(ctx context.Context, userID uuid.UUID, taskData json.RawMessage) (time.Time, error) {
	if userID == uuid.Nil || !validCompetitionTask(taskData) {
		return time.Time{}, ErrInvalidCompetitionTask
	}
	updatedAt := time.Now().UTC()
	if err := s.tasks.Replace(ctx, userID, string(taskData), updatedAt); err != nil {
		return time.Time{}, err
	}
	return updatedAt, nil
}

func (s *CompetitionTaskService) ListAll(ctx context.Context) ([]model.CompetitionTask, error) {
	tasks, err := s.tasks.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		if task.UserID == uuid.Nil || !validCompetitionTask(json.RawMessage(task.TaskData)) {
			return nil, ErrInvalidCompetitionTask
		}
	}
	return tasks, nil
}

func validCompetitionTask(raw json.RawMessage) bool {
	if len(raw) == 0 || len(raw) > MaxCompetitionTaskBytes || !json.Valid(raw) {
		return false
	}
	var task struct {
		TaskType   string `json:"taskType"`
		Version    int    `json:"version"`
		Turnpoints []struct {
			Type     string  `json:"type"`
			Radius   float64 `json:"radius"`
			Waypoint struct {
				Name        string   `json:"name"`
				Description string   `json:"description"`
				Latitude    *float64 `json:"lat"`
				Longitude   *float64 `json:"lon"`
				AltitudeM   *float64 `json:"altSmoothed"`
			} `json:"waypoint"`
		} `json:"turnpoints"`
	}
	if err := json.Unmarshal(raw, &task); err != nil {
		return false
	}
	if task.TaskType != "CLASSIC" || task.Version < 1 || task.Version > 2 ||
		len(task.Turnpoints) == 0 || len(task.Turnpoints) > MaxCompetitionTaskTurnpoints {
		return false
	}
	for _, point := range task.Turnpoints {
		if point.Type != "" && point.Type != "TAKEOFF" && point.Type != "SSS" && point.Type != "ESS" {
			return false
		}
		if point.Waypoint.Latitude == nil || point.Waypoint.Longitude == nil ||
			utf8.RuneCountInString(point.Waypoint.Name) > 80 ||
			utf8.RuneCountInString(point.Waypoint.Description) > 200 ||
			math.IsNaN(point.Radius) || math.IsInf(point.Radius, 0) || point.Radius < 1 || point.Radius > 200000 {
			return false
		}
		latitude, longitude := *point.Waypoint.Latitude, *point.Waypoint.Longitude
		if math.IsNaN(latitude) || math.IsInf(latitude, 0) || latitude < -90 || latitude > 90 ||
			math.IsNaN(longitude) || math.IsInf(longitude, 0) || longitude < -180 || longitude > 180 {
			return false
		}
		if point.Waypoint.AltitudeM != nil {
			altitude := *point.Waypoint.AltitudeM
			if math.IsNaN(altitude) || math.IsInf(altitude, 0) || altitude < -12000 || altitude > 100000 {
				return false
			}
		}
	}
	return true
}
