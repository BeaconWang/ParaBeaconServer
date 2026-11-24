package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const ChatPageSize = 50

var ErrInvalidChatMediaReference = errors.New("invalid chat media reference")

type ChatUser struct {
	ID              uuid.UUID
	PilotNumber     int
	ChannelNumber   int
	Username        string
	DisplayName     string
	AvatarID        int
	ProfileImageKey string
	ProfileImageURL string
}

type ChatAccount struct {
	PilotNumber     int    `json:"pilot_number"`
	Username        string `json:"username"`
	DisplayName     string `json:"display_name,omitempty"`
	AvatarID        int    `json:"avatar_id"`
	ProfileImageURL string `json:"profile_image_url,omitempty"`
}

type ChatConversation struct {
	PeerID          uuid.UUID  `json:"-"`
	Online          bool       `json:"-"`
	PilotNumber     int        `json:"pilot_number"`
	Username        string     `json:"username"`
	DisplayName     string     `json:"display_name,omitempty"`
	AvatarID        int        `json:"avatar_id"`
	ProfileImageURL string     `json:"profile_image_url,omitempty"`
	LastChatAt      *time.Time `json:"last_chat_at,omitempty"`
	LastMessageText string     `json:"last_message_text,omitempty"`
	LastMessageKind string     `json:"last_message_kind,omitempty"`
	UnreadCount     int64      `json:"unread_count"`
	Pinned          bool       `json:"pinned"`
	PinnedAt        *time.Time `json:"-"`
}

type ChatRepository interface {
	FindChatUserByID(context.Context, uuid.UUID) (*ChatUser, error)
	FindChatUserByPilotNumber(context.Context, int) (*ChatUser, error)
	SearchChatAccounts(context.Context, uuid.UUID, string, int, int) ([]ChatAccount, error)
	ListChatConversations(context.Context, uuid.UUID, string, []uuid.UUID, int, int) ([]ChatConversation, error)
	SetChatConversationPinned(context.Context, uuid.UUID, uuid.UUID, bool, time.Time) error
	EnsureChatConversationRead(context.Context, uuid.UUID, uuid.UUID, time.Time) error
	MarkChatConversationRead(context.Context, uuid.UUID, uuid.UUID, time.Time) error
	CreateChatMessage(context.Context, *model.ChatMessage) error
	ListPublicChat(context.Context, time.Time, uuid.UUID, int) ([]model.ChatMessage, error)
	ListChannelChat(context.Context, int, time.Time, uuid.UUID, int) ([]model.ChatMessage, error)
	ListDirectChat(context.Context, uuid.UUID, uuid.UUID, time.Time, uuid.UUID, int) ([]model.ChatMessage, error)
	HasFreshPublicLocation(context.Context, int, time.Time, time.Time) (bool, error)
}

type ChatMediaRepository interface {
	CreateChatMedia(context.Context, *model.ChatMedia) error
	CreateChatMessageWithMedia(context.Context, *model.ChatMessage) error
	FindChatMediaForUser(context.Context, uuid.UUID, uuid.UUID) (*model.ChatMedia, error)
	DeleteExpiredChatMedia(context.Context, time.Time) ([]model.ChatMedia, error)
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
		Select("id, pilot_number, channel_number, username, display_name, avatar_id, profile_image_key").
		Where("id = ?", id).
		Take(&user).Error; err != nil {
		return nil, fmt.Errorf("find chat user: %w", err)
	}
	user.ProfileImageURL = ProfileImageURL(user.ProfileImageKey)
	return &user, nil
}

func (r *GormChatRepository) FindChatUserByPilotNumber(ctx context.Context, number int) (*ChatUser, error) {
	var user ChatUser
	if err := r.db.WithContext(ctx).
		Table("users").
		Select("id, pilot_number, channel_number, username, avatar_id, profile_image_key").
		Where("pilot_number = ?", number).
		Take(&user).Error; err != nil {
		return nil, fmt.Errorf("find chat pilot: %w", err)
	}
	user.ProfileImageURL = ProfileImageURL(user.ProfileImageKey)
	return &user, nil
}

func (r *GormChatRepository) SearchChatAccounts(ctx context.Context, requesterID uuid.UUID, query string, afterPilotNumber, limit int) ([]ChatAccount, error) {
	accounts := make([]ChatAccount, 0, limit)
	databaseQuery := r.db.WithContext(ctx).
		Table("users").
		Select("pilot_number, username, display_name, avatar_id, CASE WHEN profile_image_key <> '' THEN '/api/v1/profile-images/' || profile_image_key ELSE '' END AS profile_image_url").
		Where("id <> ? AND pilot_number > ?", requesterID, afterPilotNumber)
	if query != "" {
		databaseQuery = databaseQuery.Where(
			"POSITION(LOWER(?) IN LOWER(username)) > 0 OR POSITION(LOWER(?) IN LOWER(display_name)) > 0",
			query,
			query,
		)
	}
	if err := databaseQuery.Order("pilot_number ASC").Limit(limit).Scan(&accounts).Error; err != nil {
		return nil, fmt.Errorf("search chat accounts: %w", err)
	}
	return accounts, nil
}

func (r *GormChatRepository) ListChatConversations(ctx context.Context, requesterID uuid.UUID, query string, onlineIDs []uuid.UUID, offset, limit int) ([]ChatConversation, error) {
	conversations := make([]ChatConversation, 0, limit)
	selectOnline := "FALSE AS online"
	var onlineArgs []any
	if len(onlineIDs) > 0 {
		selectOnline = "CASE WHEN peer.id IN ? THEN TRUE ELSE FALSE END AS online"
		onlineArgs = append(onlineArgs, onlineIDs)
	}
	selection := `
		peer.id AS peer_id,
		peer.pilot_number,
		peer.username,
		peer.display_name,
		peer.avatar_id,
		CASE WHEN peer.profile_image_key <> '' THEN '/api/v1/profile-images/' || peer.profile_image_key ELSE '' END AS profile_image_url,
		latest.last_chat_at,
		COALESCE(latest.last_message_text, '') AS last_message_text,
		COALESCE(latest.last_message_kind, 'text') AS last_message_kind,
		COALESCE(unread.unread_count, 0) AS unread_count,
		pins.pinned_at IS NOT NULL AS pinned,
		pins.pinned_at,
		` + selectOnline
	databaseQuery := r.db.WithContext(ctx).
		Table("users AS peer").
		Select(selection, onlineArgs...).
		Joins("JOIN users AS owner ON owner.id = ?", requesterID).
		Joins("LEFT JOIN chat_conversation_pins AS pins ON pins.owner_id = owner.id AND pins.peer_id = peer.id").
		Joins("LEFT JOIN chat_conversation_reads AS reads ON reads.owner_id = owner.id AND reads.peer_id = peer.id").
		Joins(`LEFT JOIN LATERAL (
			SELECT message.created_at AS last_chat_at,
				message.text AS last_message_text,
				message.kind AS last_message_kind
			FROM chat_messages AS message
			WHERE message.scope = ?
				AND ((message.sender_id = owner.id AND message.recipient_id = peer.id)
					OR (message.sender_id = peer.id AND message.recipient_id = owner.id))
			ORDER BY message.created_at DESC, message.id DESC
			LIMIT 1
		) AS latest ON TRUE`, "direct").
		Joins(`LEFT JOIN LATERAL (
			SELECT COUNT(*) AS unread_count
			FROM chat_messages AS unread_message
			WHERE unread_message.scope = ?
				AND unread_message.sender_id = peer.id
				AND unread_message.recipient_id = owner.id
				AND unread_message.created_at > COALESCE(reads.last_read_at, latest.last_chat_at)
		) AS unread ON TRUE`, "direct").
		Where("peer.id <> owner.id")
	if query != "" {
		databaseQuery = databaseQuery.Where(
			"POSITION(LOWER(?) IN LOWER(peer.username)) > 0 OR POSITION(LOWER(?) IN LOWER(peer.display_name)) > 0",
			query,
			query,
		)
	}
	if err := databaseQuery.
		Order("(pins.pinned_at IS NOT NULL) DESC, online DESC, latest.last_chat_at DESC NULLS LAST, LOWER(COALESCE(NULLIF(BTRIM(peer.display_name), ''), peer.username)) ASC, peer.pilot_number ASC").
		Offset(offset).
		Limit(limit).
		Scan(&conversations).Error; err != nil {
		return nil, fmt.Errorf("list chat conversations: %w", err)
	}
	return conversations, nil
}

func (r *GormChatRepository) EnsureChatConversationRead(ctx context.Context, ownerID, peerID uuid.UUID, readAt time.Time) error {
	if ownerID == uuid.Nil || peerID == uuid.Nil || ownerID == peerID {
		return fmt.Errorf("invalid chat read state")
	}
	readState := model.ChatConversationRead{
		OwnerID: ownerID, PeerID: peerID, LastReadAt: readAt.UTC(),
	}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&readState).Error; err != nil {
		return fmt.Errorf("initialize chat read state: %w", err)
	}
	return nil
}

func (r *GormChatRepository) MarkChatConversationRead(ctx context.Context, ownerID, peerID uuid.UUID, readAt time.Time) error {
	if ownerID == uuid.Nil || peerID == uuid.Nil || ownerID == peerID {
		return fmt.Errorf("invalid chat read state")
	}
	readState := model.ChatConversationRead{
		OwnerID: ownerID, PeerID: peerID, LastReadAt: readAt.UTC(),
	}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "owner_id"}, {Name: "peer_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"last_read_at": gorm.Expr("GREATEST(chat_conversation_reads.last_read_at, EXCLUDED.last_read_at)"),
		}),
	}).Create(&readState).Error; err != nil {
		return fmt.Errorf("mark chat conversation read: %w", err)
	}
	return nil
}

func (r *GormChatRepository) SetChatConversationPinned(ctx context.Context, ownerID, peerID uuid.UUID, pinned bool, pinnedAt time.Time) error {
	query := r.db.WithContext(ctx).Where("owner_id = ? AND peer_id = ?", ownerID, peerID)
	if !pinned {
		if err := query.Delete(&model.ChatConversationPin{}).Error; err != nil {
			return fmt.Errorf("unpin chat conversation: %w", err)
		}
		return nil
	}
	pin := model.ChatConversationPin{OwnerID: ownerID, PeerID: peerID, PinnedAt: pinnedAt, CreatedAt: pinnedAt}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "owner_id"}, {Name: "peer_id"}},
		DoUpdates: clause.Assignments(map[string]any{"pinned_at": pinnedAt}),
	}).Create(&pin).Error; err != nil {
		return fmt.Errorf("pin chat conversation: %w", err)
	}
	return nil
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

func (r *GormChatRepository) CreateChatMessageWithMedia(ctx context.Context, message *model.ChatMessage) error {
	if message == nil || message.MediaID == nil || *message.MediaID == uuid.Nil {
		return ErrInvalidChatMediaReference
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var media model.ChatMedia
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", *message.MediaID).Take(&media).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrInvalidChatMediaReference
		}
		if err != nil {
			return fmt.Errorf("find staged chat media: %w", err)
		}
		if media.MessageID != nil || !media.ExpiresAt.After(time.Now().UTC()) ||
			media.SenderID != message.SenderID || media.Scope != message.Scope || media.Kind != message.Kind ||
			!sameOptionalInt(media.ChannelNumber, message.ChannelNumber) || !sameOptionalUUID(media.RecipientID, message.RecipientID) {
			return ErrInvalidChatMediaReference
		}
		if err := tx.Create(message).Error; err != nil {
			return fmt.Errorf("persist chat message: %w", err)
		}
		result := tx.Model(&model.ChatMedia{}).
			Where("id = ? AND message_id IS NULL AND expires_at > ?", media.ID, time.Now().UTC()).
			Update("message_id", message.ID)
		if result.Error != nil {
			return fmt.Errorf("attach media to chat message: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrInvalidChatMediaReference
		}
		return nil
	})
}

func sameOptionalInt(left, right *int) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func sameOptionalUUID(left, right *uuid.UUID) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func (r *GormChatRepository) CreateChatMedia(ctx context.Context, media *model.ChatMedia) error {
	if media == nil || media.ID == uuid.Nil || media.SenderID == uuid.Nil {
		return fmt.Errorf("invalid chat media")
	}
	if err := r.db.WithContext(ctx).Create(media).Error; err != nil {
		return fmt.Errorf("persist staged chat media: %w", err)
	}
	return nil
}

func (r *GormChatRepository) FindChatMediaForUser(ctx context.Context, mediaID, requesterID uuid.UUID) (*model.ChatMedia, error) {
	var media model.ChatMedia
	err := r.db.WithContext(ctx).
		Where("id = ? AND message_id IS NOT NULL", mediaID).
		Where(`(scope = ? OR
			(scope = ? AND EXISTS (SELECT 1 FROM users AS viewer WHERE viewer.id = ? AND viewer.channel_number = chat_media.channel_number)) OR
			(scope = ? AND (sender_id = ? OR recipient_id = ?)))`,
			"public", "channel", requesterID, "direct", requesterID, requesterID).
		Take(&media).Error
	if err != nil {
		return nil, err
	}
	return &media, nil
}

func (r *GormChatRepository) DeleteExpiredChatMedia(ctx context.Context, before time.Time) ([]model.ChatMedia, error) {
	var expired []model.ChatMedia
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("message_id IS NULL AND expires_at <= ?", before.UTC()).
			Find(&expired).Error; err != nil {
			return err
		}
		if len(expired) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, len(expired))
		for i := range expired {
			ids[i] = expired[i].ID
		}
		return tx.Where("id IN ? AND message_id IS NULL", ids).Delete(&model.ChatMedia{}).Error
	})
	if err != nil {
		return nil, fmt.Errorf("delete expired staged chat media: %w", err)
	}
	return expired, nil
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
