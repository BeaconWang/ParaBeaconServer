package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrCompetitionTaskNotFound = errors.New("competition task not found")

type CompetitionTaskRepository interface {
	Replace(ctx context.Context, userID uuid.UUID, taskData string, updatedAt time.Time) error
	DeleteOwnTask(ctx context.Context, userID uuid.UUID) error
	ListAll(ctx context.Context) ([]model.CompetitionTaskListEntry, error)
	CurrentTaskOwner(ctx context.Context, userID uuid.UUID) (*uuid.UUID, error)
	SetCurrentTaskOwner(ctx context.Context, userID uuid.UUID, taskOwnerID *uuid.UUID) error
}

type GormCompetitionTaskRepository struct {
	db *gorm.DB
}

func NewCompetitionTaskRepository(db *gorm.DB) *GormCompetitionTaskRepository {
	return &GormCompetitionTaskRepository{db: db}
}

func (r *GormCompetitionTaskRepository) Replace(ctx context.Context, userID uuid.UUID, taskData string, updatedAt time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		task := model.CompetitionTask{
			UserID:    userID,
			TaskData:  taskData,
			UpdatedAt: updatedAt,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"task_data", "updated_at"}),
		}).Create(&task).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.User{}).Where("id = ?", userID).
			Update("current_competition_task_owner_id", userID).Error; err != nil {
			return err
		}
		return tx.Model(&model.LiveTrackSession{}).
			Where("user_id = ? AND ended_at IS NULL", userID).
			Update("current_competition_task_owner_id", userID).Error
	})
}

func (r *GormCompetitionTaskRepository) DeleteOwnTask(ctx context.Context, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("user_id = ?", userID).Delete(&model.CompetitionTask{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrCompetitionTaskNotFound
		}
		if err := tx.Model(&model.User{}).
			Where("current_competition_task_owner_id = ?", userID).
			Update("current_competition_task_owner_id", nil).Error; err != nil {
			return err
		}
		return tx.Model(&model.LiveTrackSession{}).
			Where("current_competition_task_owner_id = ?", userID).
			Update("current_competition_task_owner_id", nil).Error
	})
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

func (r *GormCompetitionTaskRepository) CurrentTaskOwner(ctx context.Context, userID uuid.UUID) (*uuid.UUID, error) {
	var user model.User
	if err := r.db.WithContext(ctx).
		Select("current_competition_task_owner_id").
		Where("id = ?", userID).
		Take(&user).Error; err != nil {
		return nil, err
	}
	return user.CurrentCompetitionTaskOwnerID, nil
}

func (r *GormCompetitionTaskRepository) SetCurrentTaskOwner(ctx context.Context, userID uuid.UUID, taskOwnerID *uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if taskOwnerID != nil {
			var task model.CompetitionTask
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("user_id = ?", *taskOwnerID).
				Take(&task).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrCompetitionTaskNotFound
				}
				return err
			}
		}
		result := tx.Model(&model.User{}).
			Where("id = ?", userID).
			Updates(map[string]interface{}{"current_competition_task_owner_id": taskOwnerID})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return tx.Model(&model.LiveTrackSession{}).
			Where("user_id = ? AND ended_at IS NULL", userID).
			Updates(map[string]interface{}{"current_competition_task_owner_id": taskOwnerID}).Error
	})
}
