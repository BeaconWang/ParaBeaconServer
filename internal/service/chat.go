package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/repository"
	"gorm.io/gorm"
)

const (
	ChatScopePublic          = "public"
	ChatScopeChannel         = "channel"
	ChatScopeDirect          = "direct"
	ChatMaxTextRunes         = 1000
	ChatLocationStatusMaxAge = 90 * time.Second
	ChatAccountPageSize      = 30
	ChatAccountMaxPageSize   = 50
)

var (
	ErrInvalidChatRequest = errors.New("invalid chat request")
	ErrChatUserNotFound   = errors.New("chat pilot not found")
	ErrChatRateLimited    = errors.New("chat message rate limited")
)

type ChatMessage struct {
	ID                   uuid.UUID `json:"id"`
	Scope                string    `json:"scope"`
	ChannelNumber        *int      `json:"channel_number,omitempty"`
	SenderPilotNumber    int       `json:"sender_pilot_number"`
	SenderName           string    `json:"sender_name"`
	RecipientPilotNumber *int      `json:"recipient_pilot_number,omitempty"`
	RecipientID          uuid.UUID `json:"-"`
	Text                 string    `json:"text"`
	CreatedAt            time.Time `json:"created_at"`
}

type ChatCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        uuid.UUID `json:"id"`
}

type ChatPage struct {
	Messages   []ChatMessage `json:"messages"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

type ChatAccountPage struct {
	Accounts   []repository.ChatAccount `json:"accounts"`
	NextCursor string                   `json:"next_cursor,omitempty"`
}

type SendChatRequest struct {
	Scope         string `json:"scope"`
	ChannelNumber int    `json:"channel_number,omitempty"`
	PilotNumber   int    `json:"pilot_number,omitempty"`
	Text          string `json:"text"`
}

type ChatService struct {
	repository repository.ChatRepository
	rateMu     sync.Mutex
	lastSent   map[uuid.UUID]time.Time
}

func NewChatService(repository repository.ChatRepository) *ChatService {
	return &ChatService{repository: repository, lastSent: make(map[uuid.UUID]time.Time)}
}

func (s *ChatService) ResolvePilot(ctx context.Context, number int) (uuid.UUID, error) {
	if number < 100000 || number > 999999 {
		return uuid.Nil, ErrInvalidChatRequest
	}
	user, err := s.repository.FindChatUserByPilotNumber(ctx, number)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return uuid.Nil, ErrChatUserNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	return user.ID, nil
}

func (s *ChatService) Send(ctx context.Context, senderID uuid.UUID, request SendChatRequest) (ChatMessage, error) {
	if senderID == uuid.Nil {
		return ChatMessage{}, ErrInvalidChatRequest
	}
	text := strings.TrimSpace(request.Text)
	if text == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > ChatMaxTextRunes || strings.ContainsAny(text, "\x00\r") {
		return ChatMessage{}, ErrInvalidChatRequest
	}
	sender, err := s.repository.FindChatUserByID(ctx, senderID)
	if err != nil {
		return ChatMessage{}, err
	}
	senderName := strings.TrimSpace(sender.DisplayName)
	if senderName == "" {
		senderName = sender.Username
	}
	message := &model.ChatMessage{
		ID:                uuid.New(),
		Scope:             request.Scope,
		SenderID:          sender.ID,
		SenderPilotNumber: sender.PilotNumber,
		SenderName:        senderName,
		Text:              text,
		CreatedAt:         time.Now().UTC(),
	}
	switch request.Scope {
	case ChatScopePublic:
		if request.ChannelNumber != 0 || request.PilotNumber != 0 {
			return ChatMessage{}, ErrInvalidChatRequest
		}
	case ChatScopeChannel:
		if request.ChannelNumber < 0 || request.ChannelNumber > 999999 || request.PilotNumber != 0 {
			return ChatMessage{}, ErrInvalidChatRequest
		}
		channel := request.ChannelNumber
		message.ChannelNumber = &channel
	case ChatScopeDirect:
		if request.ChannelNumber != 0 || request.PilotNumber < 100000 || request.PilotNumber > 999999 {
			return ChatMessage{}, ErrInvalidChatRequest
		}
		if request.PilotNumber == sender.PilotNumber {
			return ChatMessage{}, ErrInvalidChatRequest
		}
		recipient, err := s.repository.FindChatUserByPilotNumber(ctx, request.PilotNumber)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ChatMessage{}, ErrChatUserNotFound
		}
		if err != nil {
			return ChatMessage{}, err
		}
		message.RecipientID = &recipient.ID
		message.RecipientPilotNumber = &recipient.PilotNumber
	default:
		return ChatMessage{}, ErrInvalidChatRequest
	}
	now := time.Now().UTC()
	s.rateMu.Lock()
	if last := s.lastSent[senderID]; !last.IsZero() && now.Sub(last) < 750*time.Millisecond {
		s.rateMu.Unlock()
		return ChatMessage{}, ErrChatRateLimited
	}
	s.lastSent[senderID] = now
	if len(s.lastSent) > 100000 {
		for userID, sentAt := range s.lastSent {
			if now.Sub(sentAt) > time.Hour {
				delete(s.lastSent, userID)
			}
		}
	}
	s.rateMu.Unlock()
	message.CreatedAt = now
	if err := s.repository.CreateChatMessage(ctx, message); err != nil {
		return ChatMessage{}, err
	}
	return chatMessageFromModel(message), nil
}

func (s *ChatService) SearchAccounts(ctx context.Context, requesterID uuid.UUID, rawQuery, rawCursor string, limit int) (ChatAccountPage, error) {
	query := strings.TrimSpace(rawQuery)
	if requesterID == uuid.Nil ||
		!utf8.ValidString(query) ||
		utf8.RuneCountInString(query) > 64 ||
		strings.ContainsAny(query, "\x00\r\n") {
		return ChatAccountPage{}, ErrInvalidChatRequest
	}
	if query != "" && utf8.RuneCountInString(query) < 2 {
		return ChatAccountPage{}, ErrInvalidChatRequest
	}
	if limit < 1 || limit > ChatAccountMaxPageSize {
		return ChatAccountPage{}, ErrInvalidChatRequest
	}
	afterPilotNumber := 0
	if rawCursor != "" {
		parsed, err := strconv.Atoi(rawCursor)
		if err != nil || parsed < 100000 || parsed > 999999 {
			return ChatAccountPage{}, ErrInvalidChatRequest
		}
		afterPilotNumber = parsed
	}
	accounts, err := s.repository.SearchChatAccounts(
		ctx,
		requesterID,
		query,
		afterPilotNumber,
		limit+1,
	)
	if err != nil {
		return ChatAccountPage{}, err
	}
	hasMore := len(accounts) > limit
	if hasMore {
		accounts = accounts[:limit]
	}
	page := ChatAccountPage{Accounts: accounts}
	if hasMore && len(accounts) > 0 {
		page.NextCursor = strconv.Itoa(accounts[len(accounts)-1].PilotNumber)
	}
	return page, nil
}

func (s *ChatService) PublicLocationStatus(ctx context.Context, requesterID uuid.UUID, pilotNumber int) (bool, error) {
	if requesterID == uuid.Nil || pilotNumber < 100000 || pilotNumber > 999999 {
		return false, ErrInvalidChatRequest
	}
	pilot, err := s.repository.FindChatUserByPilotNumber(ctx, pilotNumber)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if pilot.ID == requesterID {
		return false, nil
	}
	now := time.Now().UTC()
	return s.repository.HasFreshPublicLocation(
		ctx,
		pilotNumber,
		now.Add(-ChatLocationStatusMaxAge),
		now.Add(5*time.Second),
	)
}

func (s *ChatService) SubscribePeer(ctx context.Context, userID uuid.UUID, number int) (uuid.UUID, error) {
	if userID == uuid.Nil || number < 100000 || number > 999999 {
		return uuid.Nil, ErrInvalidChatRequest
	}
	peerID, err := s.ResolvePilot(ctx, number)
	if err != nil {
		return uuid.Nil, err
	}
	if peerID == userID {
		return uuid.Nil, ErrInvalidChatRequest
	}
	return peerID, nil
}

func (s *ChatService) History(ctx context.Context, userID uuid.UUID, scope string, channelNumber, peerNumber int, rawCursor string) (ChatPage, error) {
	if userID == uuid.Nil {
		return ChatPage{}, ErrInvalidChatRequest
	}
	before, err := decodeChatCursor(rawCursor)
	if err != nil {
		return ChatPage{}, err
	}
	const queryLimit = repository.ChatPageSize + 1
	var rows []model.ChatMessage
	switch scope {
	case ChatScopePublic:
		if channelNumber != 0 || peerNumber != 0 {
			return ChatPage{}, ErrInvalidChatRequest
		}
		rows, err = s.repository.ListPublicChat(ctx, before.CreatedAt, before.ID, queryLimit)
	case ChatScopeChannel:
		if channelNumber < 0 || channelNumber > 999999 || peerNumber != 0 {
			return ChatPage{}, ErrInvalidChatRequest
		}
		rows, err = s.repository.ListChannelChat(ctx, channelNumber, before.CreatedAt, before.ID, queryLimit)
	case ChatScopeDirect:
		if channelNumber != 0 || peerNumber < 100000 || peerNumber > 999999 {
			return ChatPage{}, ErrInvalidChatRequest
		}
		peerID, resolveErr := s.SubscribePeer(ctx, userID, peerNumber)
		if resolveErr != nil {
			return ChatPage{}, resolveErr
		}
		rows, err = s.repository.ListDirectChat(ctx, userID, peerID, before.CreatedAt, before.ID, queryLimit)
	default:
		return ChatPage{}, ErrInvalidChatRequest
	}
	if err != nil {
		return ChatPage{}, err
	}
	hasMore := len(rows) > repository.ChatPageSize
	if hasMore {
		rows = rows[:repository.ChatPageSize]
	}
	page := ChatPage{Messages: make([]ChatMessage, len(rows))}
	for index, row := range rows {
		page.Messages[index] = chatMessageFromModel(&row)
	}
	for left, right := 0, len(page.Messages)-1; left < right; left, right = left+1, right-1 {
		page.Messages[left], page.Messages[right] = page.Messages[right], page.Messages[left]
	}
	if hasMore && len(rows) > 0 {
		oldest := rows[len(rows)-1]
		cursor, err := encodeChatCursor(ChatCursor{CreatedAt: oldest.CreatedAt, ID: oldest.ID})
		if err != nil {
			return ChatPage{}, fmt.Errorf("encode chat cursor: %w", err)
		}
		page.NextCursor = cursor
	}
	return page, nil
}

func dereferenceUUID(value *uuid.UUID) uuid.UUID {
	if value == nil {
		return uuid.Nil
	}
	return *value
}

func chatMessageFromModel(message *model.ChatMessage) ChatMessage {
	return ChatMessage{
		ID:                   message.ID,
		Scope:                message.Scope,
		ChannelNumber:        message.ChannelNumber,
		SenderPilotNumber:    message.SenderPilotNumber,
		SenderName:           message.SenderName,
		RecipientPilotNumber: message.RecipientPilotNumber,
		RecipientID:          dereferenceUUID(message.RecipientID),
		Text:                 message.Text,
		CreatedAt:            message.CreatedAt.UTC(),
	}
}

func encodeChatCursor(cursor ChatCursor) (string, error) {
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeChatCursor(raw string) (ChatCursor, error) {
	if raw == "" {
		return ChatCursor{}, nil
	}
	if len(raw) > 256 {
		return ChatCursor{}, ErrInvalidChatRequest
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return ChatCursor{}, ErrInvalidChatRequest
	}
	var cursor ChatCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.ID == uuid.Nil || cursor.CreatedAt.IsZero() {
		return ChatCursor{}, ErrInvalidChatRequest
	}
	return cursor, nil
}
