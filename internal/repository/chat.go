package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"gorm.io/gorm"
)

const ChatPageSize = 50

type ChatUser struct {
	ID          uuid.UUID
	PilotNumber int
	Username    string
}

type ChatRepository interface {
	FindChatUserByID(context.Context, uuid.UUID) (*ChatUser, error)
	FindChatUserByPilotNumber(context.Context, int) (*ChatUser, error)
	CreateChatMessage(context.Context, *model.ChatMessage) error
	ListPublicChat(context.Context, time.Time, uuid.UUID, int) ([]model.ChatMessage, error)
	ListChannelChat(context.Context, int, time.Time, uuid.UUID, int) ([]model.ChatMessage, error)
	ListDirectChat(context.Context, uuid.UUID, uuid.UUID, time.Time, uuid.UUID, int) ([]model.ChatMessage, error)
	HasFreshPublicLocation(context.Context, int, time.Time, time.Time) (bool, error)
}

type GormChatRepository struct {
	db *gorm.DB
}

func NewChatRepository(db *gorm.DB) *GormChatRepository {
	return &GormChatRepository{db: db}
}

func (r *GormChatRepository) FindChatUserByID(ctx context.Context, id uuid.UUID) (*ChatUser, error) {
	var user ChatUser
	if err := r.db.WithContext(ctx).
		Table("users").
		Select("id, pilot_number, username").
		Where("id = ?", id).
		Take(&user).Error; err != nil {
		return nil, fmt.Errorf("find chat user: %w", err)
	}
	return &user, nil
}

func (r *GormChatRepository) FindChatUserByPilotNumber(ctx context.Context, number int) (*ChatUser, error) {
	var user ChatUser
	if err := r.db.WithContext(ctx).
		Table("users").
		Select("id, pilot_number, username").
		Where("pilot_number = ?", number).
		Take(&user).Error; err != nil {
		return nil, fmt.Errorf("find chat pilot: %w", err)
	}
	return &user, nil
}

func (r *GormChatRepository) HasFreshPublicLocation(ctx context.Context, pilotNumber int, observedSince, observedUntil time.Time) (bool, error) {
	var status struct {
		Sharing bool `gorm:"column:sharing"`
	}
	err := r.db.WithContext(ctx).Raw(`
		SELECT EXISTS (
			SELECT 1
			FROM users AS u
			JOIN live_track_sessions AS s ON s.user_id = u.id
			JOIN LATERAL (
				SELECT p.timestamp
				FROM live_track_points AS p
				WHERE p.session_id = s.id
				ORDER BY p.timestamp DESC
				LIMIT 1
			) AS latest ON TRUE
			WHERE u.pilot_number = ?
				AND s.ended_at IS NULL
				AND s.publicly_share = TRUE
				AND latest.timestamp >= ?
				AND latest.timestamp <= ?
		) AS sharing
	`, pilotNumber, observedSince, observedUntil).Scan(&status).Error
	if err != nil {
		return false, fmt.Errorf("check pilot public location status: %w", err)
	}
	return status.Sharing, nil
}

func (r *GormChatRepository) CreateChatMessage(ctx context.Context, message *model.ChatMessage) error {
	if err := r.db.WithContext(ctx).Create(message).Error; err != nil {
		return fmt.Errorf("persist chat message: %w", err)
	}
	return nil
}

func (r *GormChatRepository) ListPublicChat(ctx context.Context, before time.Time, beforeID uuid.UUID, limit int) ([]model.ChatMessage, error) {
	return r.list(ctx, r.db.WithContext(ctx).Where("scope = ?", "public"), before, beforeID, limit)
}

func (r *GormChatRepository) ListChannelChat(ctx context.Context, channel int, before time.Time, beforeID uuid.UUID, limit int) ([]model.ChatMessage, error) {
	return r.list(ctx, r.db.WithContext(ctx).Where("scope = ? AND channel_number = ?", "channel", channel), before, beforeID, limit)
}

func (r *GormChatRepository) ListDirectChat(ctx context.Context, userID, peerID uuid.UUID, before time.Time, beforeID uuid.UUID, limit int) ([]model.ChatMessage, error) {
	query := r.db.WithContext(ctx).Where(
		"scope = ? AND ((sender_id = ? AND recipient_id = ?) OR (sender_id = ? AND recipient_id = ?))",
		"direct", userID, peerID, peerID, userID,
	)
	return r.list(ctx, query, before, beforeID, limit)
}

func (r *GormChatRepository) list(ctx context.Context, query *gorm.DB, before time.Time, beforeID uuid.UUID, limit int) ([]model.ChatMessage, error) {
	messages := make([]model.ChatMessage, 0, limit)
	if !before.IsZero() {
		query = query.Where("(created_at < ?) OR (created_at = ? AND id < ?)", before, before, beforeID)
	}
	if err := query.Order("created_at DESC, id DESC").Limit(limit).Find(&messages).Error; err != nil {
		return nil, fmt.Errorf("list chat history: %w", err)
	}
	return messages, nil
}
