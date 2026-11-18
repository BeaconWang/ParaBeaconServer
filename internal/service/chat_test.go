package service

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/repository"
	"gorm.io/gorm"
)

type memoryChatRepository struct {
	users          map[uuid.UUID]repository.ChatUser
	messages       []model.ChatMessage
	locationStatus map[int]bool
}

func (r *memoryChatRepository) FindChatUserByID(_ context.Context, id uuid.UUID) (*repository.ChatUser, error) {
	user, ok := r.users[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	copy := user
	return &copy, nil
}

func (r *memoryChatRepository) FindChatUserByPilotNumber(_ context.Context, number int) (*repository.ChatUser, error) {
	for _, user := range r.users {
		if user.PilotNumber == number {
			copy := user
			return &copy, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *memoryChatRepository) HasFreshPublicLocation(_ context.Context, pilotNumber int, _, _ time.Time) (bool, error) {
	return r.locationStatus[pilotNumber], nil
}

func (r *memoryChatRepository) CreateChatMessage(_ context.Context, message *model.ChatMessage) error {
	copy := *message
	r.messages = append(r.messages, copy)
	return nil
}

func (r *memoryChatRepository) ListPublicChat(_ context.Context, before time.Time, beforeID uuid.UUID, limit int) ([]model.ChatMessage, error) {
	return r.list(func(message model.ChatMessage) bool { return message.Scope == ChatScopePublic }, before, beforeID, limit), nil
}

func (r *memoryChatRepository) ListChannelChat(_ context.Context, channel int, before time.Time, beforeID uuid.UUID, limit int) ([]model.ChatMessage, error) {
	return r.list(func(message model.ChatMessage) bool {
		return message.Scope == ChatScopeChannel && message.ChannelNumber != nil && *message.ChannelNumber == channel
	}, before, beforeID, limit), nil
}

func (r *memoryChatRepository) ListDirectChat(_ context.Context, userID, peerID uuid.UUID, before time.Time, beforeID uuid.UUID, limit int) ([]model.ChatMessage, error) {
	return r.list(func(message model.ChatMessage) bool {
		return message.Scope == ChatScopeDirect && message.RecipientID != nil &&
			((message.SenderID == userID && *message.RecipientID == peerID) ||
				(message.SenderID == peerID && *message.RecipientID == userID))
	}, before, beforeID, limit), nil
}

func (r *memoryChatRepository) list(matches func(model.ChatMessage) bool, before time.Time, beforeID uuid.UUID, limit int) []model.ChatMessage {
	rows := make([]model.ChatMessage, 0)
	for _, message := range r.messages {
		if matches(message) && (before.IsZero() || message.CreatedAt.Before(before) || message.CreatedAt.Equal(before) && message.ID.String() < beforeID.String()) {
			rows = append(rows, message)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].ID.String() > rows[j].ID.String()
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

func TestPublicLocationStatusOnlyReportsFreshPublicPartnerLocation(t *testing.T) {
	requesterID, partnerID := uuid.New(), uuid.New()
	repository := &memoryChatRepository{
		users: map[uuid.UUID]repository.ChatUser{
			requesterID: {ID: requesterID, PilotNumber: 123456, Username: "requester"},
			partnerID:   {ID: partnerID, PilotNumber: 234567, Username: "partner"},
		},
		locationStatus: map[int]bool{234567: true},
	}
	chat := NewChatService(repository)

	sharing, err := chat.PublicLocationStatus(context.Background(), requesterID, 234567)
	if err != nil || !sharing {
		t.Fatalf("partner status = %v, err=%v; want sharing", sharing, err)
	}
	sharing, err = chat.PublicLocationStatus(context.Background(), requesterID, 123456)
	if err != nil || sharing {
		t.Fatalf("self status = %v, err=%v; want not sharing", sharing, err)
	}
	sharing, err = chat.PublicLocationStatus(context.Background(), requesterID, 345678)
	if err != nil || sharing {
		t.Fatalf("unknown pilot status = %v, err=%v; want false without revealing existence", sharing, err)
	}
	if _, err := chat.PublicLocationStatus(context.Background(), uuid.Nil, 234567); !errors.Is(err, ErrInvalidChatRequest) {
		t.Fatalf("anonymous status error = %v, want invalid request", err)
	}
}

func TestChatRoutesPublicChannelAndDirectMessages(t *testing.T) {
	ctx := context.Background()
	aliceID, bobID, carolID := uuid.New(), uuid.New(), uuid.New()
	channelNumber := 345678
	repository := &memoryChatRepository{users: map[uuid.UUID]repository.ChatUser{
		aliceID: {ID: aliceID, PilotNumber: 123456, Username: "alice"},
		bobID:   {ID: bobID, PilotNumber: 234567, Username: "bob"},
		carolID: {ID: carolID, PilotNumber: 345678, Username: "carol"},
	}}
	chat := NewChatService(repository)

	publicMessage, err := chat.Send(ctx, aliceID, SendChatRequest{Scope: ChatScopePublic, Text: " hello world "})
	if err != nil {
		t.Fatalf("send public: %v", err)
	}
	if publicMessage.Text != "hello world" || publicMessage.SenderPilotNumber != 123456 || publicMessage.Scope != ChatScopePublic {
		t.Fatalf("public message = %#v", publicMessage)
	}
	chat.rateMu.Lock()
	chat.lastSent[aliceID] = time.Now().Add(-time.Second)
	chat.rateMu.Unlock()

	channelMessage, err := chat.Send(ctx, aliceID, SendChatRequest{Scope: ChatScopeChannel, ChannelNumber: channelNumber, Text: "thermal"})
	if err != nil {
		t.Fatalf("send channel: %v", err)
	}
	if channelMessage.ChannelNumber == nil || *channelMessage.ChannelNumber != channelNumber {
		t.Fatalf("channel message = %#v", channelMessage)
	}
	chat.rateMu.Lock()
	chat.lastSent[aliceID] = time.Now().Add(-time.Second)
	chat.rateMu.Unlock()

	directMessage, err := chat.Send(ctx, aliceID, SendChatRequest{Scope: ChatScopeDirect, PilotNumber: 234567, Text: "hello Bob"})
	if err != nil {
		t.Fatalf("send direct: %v", err)
	}
	if directMessage.RecipientPilotNumber == nil || *directMessage.RecipientPilotNumber != 234567 || directMessage.RecipientID != bobID {
		t.Fatalf("direct message = %#v", directMessage)
	}

	publicPage, err := chat.History(ctx, carolID, ChatScopePublic, 0, 0, "")
	if err != nil || len(publicPage.Messages) != 1 || publicPage.Messages[0].ID != publicMessage.ID {
		t.Fatalf("public history = %#v, err=%v", publicPage, err)
	}
	channelPage, err := chat.History(ctx, bobID, ChatScopeChannel, channelNumber, 0, "")
	if err != nil || len(channelPage.Messages) != 1 || channelPage.Messages[0].ID != channelMessage.ID {
		t.Fatalf("channel history = %#v, err=%v", channelPage, err)
	}
	directPage, err := chat.History(ctx, bobID, ChatScopeDirect, 0, 123456, "")
	if err != nil || len(directPage.Messages) != 1 || directPage.Messages[0].ID != directMessage.ID {
		t.Fatalf("direct history = %#v, err=%v", directPage, err)
	}
	unrelatedDM, err := chat.History(ctx, carolID, ChatScopeDirect, 0, 123456, "")
	if err != nil || len(unrelatedDM.Messages) != 0 {
		t.Fatalf("unrelated direct history = %#v, err=%v", unrelatedDM, err)
	}
}

func TestChatValidatesChannelsRecipientsAndText(t *testing.T) {
	ctx := context.Background()
	aliceID, bobID := uuid.New(), uuid.New()
	repository := &memoryChatRepository{users: map[uuid.UUID]repository.ChatUser{
		aliceID: {ID: aliceID, PilotNumber: 123456, Username: "alice"},
		bobID:   {ID: bobID, PilotNumber: 234567, Username: "bob"},
	}}
	chat := NewChatService(repository)
	for _, request := range []SendChatRequest{
		{Scope: "unknown", Text: "hello"},
		{Scope: ChatScopeChannel, ChannelNumber: 1000000, Text: "hello"},
		{Scope: ChatScopeDirect, PilotNumber: 99999, Text: "hello"},
		{Scope: ChatScopeDirect, PilotNumber: 123456, Text: "self"},
		{Scope: ChatScopePublic, Text: string(make([]byte, 0))},
		{Scope: ChatScopePublic, Text: string(make([]rune, ChatMaxTextRunes+1))},
	} {
		if _, err := chat.Send(ctx, aliceID, request); !errors.Is(err, ErrInvalidChatRequest) {
			t.Errorf("Send(%#v) error = %v, want invalid request", request, err)
		}
	}
	if _, err := chat.Send(ctx, aliceID, SendChatRequest{Scope: ChatScopeDirect, PilotNumber: 999998, Text: "unknown pilot"}); !errors.Is(err, ErrChatUserNotFound) {
		t.Fatalf("missing recipient error = %v, want not found", err)
	}
}

func TestChatAllowsChannel000000AndRateLimitsSender(t *testing.T) {
	ctx := context.Background()
	pilotID := uuid.New()
	repository := &memoryChatRepository{users: map[uuid.UUID]repository.ChatUser{
		pilotID: {ID: pilotID, PilotNumber: 123456, Username: "pilot"},
	}}
	chat := NewChatService(repository)
	if _, err := chat.Send(ctx, pilotID, SendChatRequest{Scope: ChatScopeChannel, ChannelNumber: 0, Text: "all channel"}); err != nil {
		t.Fatalf("send channel 000000: %v", err)
	}
	if _, err := chat.Send(ctx, pilotID, SendChatRequest{Scope: ChatScopePublic, Text: "too fast"}); !errors.Is(err, ErrChatRateLimited) {
		t.Fatalf("rapid send error = %v, want rate limited", err)
	}
}
