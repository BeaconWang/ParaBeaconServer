package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"gorm.io/gorm"
)

type LiveRoomAuthorizer struct {
	db *gorm.DB
}

func NewLiveRoomAuthorizer(db *gorm.DB) *LiveRoomAuthorizer {
	return &LiveRoomAuthorizer{db: db}
}

func (a *LiveRoomAuthorizer) AuthorizeLiveRoom(ctx context.Context, userID, roomID uuid.UUID) (canPublish bool, pilotName string, allowed bool, err error) {
	if a == nil || a.db == nil || userID == uuid.Nil || roomID == uuid.Nil {
		return false, "", false, nil
	}
	var session model.LiveTrackSession
	err = a.db.WithContext(ctx).
		Select("id", "user_id", "pilot_name", "publicly_share").
		Where("id = ? AND ended_at IS NULL", roomID).
		Take(&session).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, "", false, nil
	}
	if err != nil {
		return false, "", false, err
	}
	canPublish = session.UserID == userID
	if !canPublish && !session.PubliclyShare {
		return false, "", false, nil
	}
	return canPublish, session.PilotName, true, nil
}
