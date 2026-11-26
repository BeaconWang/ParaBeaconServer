package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CompetitionTaskRepository interface {
	Replace(ctx context.Context, userID uuid.UUID, taskData string, updatedAt time.Time) error
	ListAll(ctx context.Context) ([]model.CompetitionTaskListEntry, error)
}

type GormCompetitionTaskRepository struct {
	db *gorm.DB
}

func NewCompetitionTaskRepository(db *gorm.DB) *GormCompetitionTaskRepository {
	return &GormCompetitionTaskRepository{db: db}
}

func (r *GormCompetitionTaskRepository) Replace(ctx context.Context, userID uuid.UUID, taskData string, updatedAt time.Time) error {
	task := model.CompetitionTask{
		UserID:    userID,
		TaskData:  taskData,
		UpdatedAt: updatedAt,
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"task_data", "updated_at"}),
	}).Create(&task).Error
}

func (r *GormCompetitionTaskRepository) ListAll(ctx context.Context) ([]model.CompetitionTaskListEntry, error) {
	tasks := make([]model.CompetitionTaskListEntry, 0)
	err := r.db.WithContext(ctx).
		Table("competition_tasks").
		Select("competition_tasks.user_id, competition_tasks.task_data, competition_tasks.updated_at, users.username, users.display_name").
		Joins("JOIN users ON users.id = competition_tasks.user_id").
		Order("competition_tasks.updated_at DESC, competition_tasks.user_id ASC").
		Scan(&tasks).Error
	return tasks, err
}
