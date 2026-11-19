package service

import (
	"context"
	"errors"
	"sort"
	"strings"
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

func (r *memoryChatRepository) SearchChatAccounts(_ context.Context, requesterID uuid.UUID, query string, afterPilotNumber, limit int) ([]repository.ChatAccount, error) {
	query = strings.ToLower(query)
	accounts := make([]repository.ChatAccount, 0)
	requester := r.users[requesterID]
	for id, user := range r.users {
		if requester.ChannelNumber == 0 ||
			id == requesterID ||
			user.ChannelNumber != requester.ChannelNumber ||
			user.PilotNumber <= afterPilotNumber {
			continue
		}
		if query != "" &&
			!strings.Contains(strings.ToLower(user.Username), query) &&
			!strings.Contains(strings.ToLower(user.DisplayName), query) {
			continue
		}
		accounts = append(accounts, repository.ChatAccount{
			PilotNumber: user.PilotNumber,
			Username:    user.Username,
			DisplayName: user.DisplayName,
		})
	}
	sort.Slice(accounts, func(i, j int) bool {
		return accounts[i].PilotNumber < accounts[j].PilotNumber
	})
	if len(accounts) > limit {
		accounts = accounts[:limit]
	}
	return accounts, nil
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

func TestSearchAccountsFindsUsernameAndDisplayNameAndPages(t *testing.T) {
	requesterID, aliceID, bobID, carolID, otherChannelID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	repository := &memoryChatRepository{users: map[uuid.UUID]repository.ChatUser{
		requesterID:    {ID: requesterID, PilotNumber: 100001, ChannelNumber: 456789, Username: "my_account", DisplayName: "My Name"},
		aliceID:        {ID: aliceID, PilotNumber: 100002, ChannelNumber: 456789, Username: "ridge_runner", DisplayName: "Alice Summit"},
		bobID:          {ID: bobID, PilotNumber: 100003, ChannelNumber: 456789, Username: "bob_wings", DisplayName: "Robert Cloud"},
		carolID:        {ID: carolID, PilotNumber: 100004, ChannelNumber: 456789, Username: "carol", DisplayName: "Alice Ridge"},
		otherChannelID: {ID: otherChannelID, PilotNumber: 100005, ChannelNumber: 456780, Username: "alice_other", DisplayName: "Alice Other"},
	}}
	chat := NewChatService(repository)

	byUsername, err := chat.SearchAccounts(context.Background(), requesterID, "WINGS", "", 10)
	if err != nil {
		t.Fatalf("search username: %v", err)
	}
	if len(byUsername.Accounts) != 1 || byUsername.Accounts[0].Username != "bob_wings" {
		t.Fatalf("username matches = %#v", byUsername.Accounts)
	}
	byDisplayName, err := chat.SearchAccounts(context.Background(), requesterID, "alice", "", 10)
	if err != nil {
		t.Fatalf("search display name: %v", err)
	}
	if len(byDisplayName.Accounts) != 2 || byDisplayName.Accounts[0].DisplayName != "Alice Summit" {
		t.Fatalf("display-name matches = %#v", byDisplayName.Accounts)
	}

	firstPage, err := chat.SearchAccounts(context.Background(), requesterID, "", "", 2)
	if err != nil {
		t.Fatalf("list first account page: %v", err)
	}
	if len(firstPage.Accounts) != 2 || firstPage.NextCursor != "100003" {
		t.Fatalf("first page = %#v", firstPage)
	}
	secondPage, err := chat.SearchAccounts(context.Background(), requesterID, "", firstPage.NextCursor, 2)
	if err != nil {
		t.Fatalf("list second account page: %v", err)
	}
	if len(secondPage.Accounts) != 1 || secondPage.Accounts[0].Username != "carol" || secondPage.NextCursor != "" {
		t.Fatalf("second page = %#v", secondPage)
	}
	for _, account := range append(firstPage.Accounts, secondPage.Accounts...) {
		if account.PilotNumber == 100001 {
			t.Fatal("account search included the requester")
		}
	}
}

func TestSearchAccountsRejectsInvalidQueriesAndPaging(t *testing.T) {
	requesterID := uuid.New()
	repository := &memoryChatRepository{users: map[uuid.UUID]repository.ChatUser{
		requesterID: {ID: requesterID, PilotNumber: 123456, Username: "pilot"},
	}}
	chat := NewChatService(repository)
	for _, testCase := range []struct {
		query  string
		cursor string
		limit  int
	}{
		{query: "a", limit: 10},
		{query: strings.Repeat("a", 65), limit: 10},
		{query: "pilot", cursor: "not-a-number", limit: 10},
		{query: "pilot", limit: ChatAccountMaxPageSize + 1},
	} {
		if _, err := chat.SearchAccounts(context.Background(), requesterID, testCase.query, testCase.cursor, testCase.limit); !errors.Is(err, ErrInvalidChatRequest) {
			t.Errorf("SearchAccounts(%q, %q, %d) error = %v, want invalid request", testCase.query, testCase.cursor, testCase.limit, err)
		}
	}
}

func TestChannelAccessRequiresSameNonzeroAssignedFrequency(t *testing.T) {
	requesterID, sameChannelID, otherChannelID := uuid.New(), uuid.New(), uuid.New()
	repository := &memoryChatRepository{users: map[uuid.UUID]repository.ChatUser{
		requesterID:    {ID: requesterID, PilotNumber: 123456, ChannelNumber: 765432, Username: "requester"},
		sameChannelID:  {ID: sameChannelID, PilotNumber: 234567, ChannelNumber: 765432, Username: "same_frequency"},
		otherChannelID: {ID: otherChannelID, PilotNumber: 345678, ChannelNumber: 765433, Username: "other_frequency"},
	}}
	chat := NewChatService(repository)

	if err := chat.ValidateChannel(context.Background(), requesterID, 765432); err != nil {
		t.Fatalf("validate assigned frequency: %v", err)
	}
	for _, channelNumber := range []int{0, 765433} {
		if err := chat.ValidateChannel(context.Background(), requesterID, channelNumber); !errors.Is(err, ErrInvalidChatRequest) {
			t.Errorf("ValidateChannel(%d) error = %v, want invalid request", channelNumber, err)
		}
	}
	peerID, err := chat.SubscribePeer(context.Background(), requesterID, 234567)
	if err != nil || peerID != sameChannelID {
		t.Fatalf("same-frequency private peer = %s, err=%v", peerID, err)
	}
	if _, err := chat.SubscribePeer(context.Background(), requesterID, 345678); !errors.Is(err, ErrInvalidChatRequest) {
		t.Fatalf("cross-frequency private peer error = %v, want invalid request", err)
	}
}

func TestPublicLocationStatusOnlyReportsFreshPublicPartnerLocation(t *testing.T) {
	requesterID, partnerID := uuid.New(), uuid.New()
	repository := &memoryChatRepository{
		users: map[uuid.UUID]repository.ChatUser{
			requesterID: {ID: requesterID, PilotNumber: 123456, ChannelNumber: 111111, Username: "requester"},
			partnerID:   {ID: partnerID, PilotNumber: 234567, ChannelNumber: 111111, Username: "partner"},
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
		aliceID: {ID: aliceID, PilotNumber: 123456, ChannelNumber: channelNumber, Username: "alice", DisplayName: "Alice Ridge"},
		bobID:   {ID: bobID, PilotNumber: 234567, ChannelNumber: channelNumber, Username: "bob"},
		carolID: {ID: carolID, PilotNumber: 345678, ChannelNumber: channelNumber, Username: "carol"},
	}}
	chat := NewChatService(repository)

	publicMessage, err := chat.Send(ctx, aliceID, SendChatRequest{Scope: ChatScopePublic, Text: " hello world "})
	if err != nil {
		t.Fatalf("send public: %v", err)
	}
	if publicMessage.Text != "hello world" || publicMessage.SenderPilotNumber != 123456 || publicMessage.Scope != ChatScopePublic || publicMessage.SenderName != "Alice Ridge" {
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
		aliceID: {ID: aliceID, PilotNumber: 123456, ChannelNumber: 345678, Username: "alice"},
		bobID:   {ID: bobID, PilotNumber: 234567, ChannelNumber: 456789, Username: "bob"},
	}}
	chat := NewChatService(repository)
	for _, request := range []SendChatRequest{
		{Scope: "unknown", Text: "hello"},
		{Scope: ChatScopeChannel, ChannelNumber: 1000000, Text: "hello"},
		{Scope: ChatScopeChannel, ChannelNumber: 0, Text: "no channel"},
		{Scope: ChatScopeChannel, ChannelNumber: 345679, Text: "different channel"},
		{Scope: ChatScopeDirect, PilotNumber: 99999, Text: "hello"},
		{Scope: ChatScopeDirect, PilotNumber: 123456, Text: "self"},
		{Scope: ChatScopePublic, Text: string(make([]byte, 0))},
		{Scope: ChatScopePublic, Text: string(make([]rune, ChatMaxTextRunes+1))},
	} {
		if _, err := chat.Send(ctx, aliceID, request); !errors.Is(err, ErrInvalidChatRequest) {
			t.Errorf("Send(%#v) error = %v, want invalid request", request, err)
		}
	}
	if _, err := chat.Send(ctx, aliceID, SendChatRequest{Scope: ChatScopeDirect, PilotNumber: 234567, Text: "different frequency"}); !errors.Is(err, ErrInvalidChatRequest) {
		t.Fatalf("cross-channel direct message error = %v, want invalid request", err)
	}
	if _, err := chat.Send(ctx, aliceID, SendChatRequest{Scope: ChatScopeDirect, PilotNumber: 999998, Text: "unknown pilot"}); !errors.Is(err, ErrChatUserNotFound) {
		t.Fatalf("missing recipient error = %v, want not found", err)
	}
}

func TestChatRejectsChannelZeroAndRateLimitsSender(t *testing.T) {
	ctx := context.Background()
	pilotID := uuid.New()
	repository := &memoryChatRepository{users: map[uuid.UUID]repository.ChatUser{
		pilotID: {ID: pilotID, PilotNumber: 123456, ChannelNumber: 987654, Username: "pilot"},
	}}
	chat := NewChatService(repository)
	if _, err := chat.Send(ctx, pilotID, SendChatRequest{Scope: ChatScopeChannel, ChannelNumber: 0, Text: "no frequency"}); !errors.Is(err, ErrInvalidChatRequest) {
		t.Fatalf("send channel zero error = %v, want invalid request", err)
	}
	if _, err := chat.Send(ctx, pilotID, SendChatRequest{Scope: ChatScopeChannel, ChannelNumber: 987653, Text: "wrong frequency"}); !errors.Is(err, ErrInvalidChatRequest) {
		t.Fatalf("send other channel error = %v, want invalid request", err)
	}
	if _, err := chat.Send(ctx, pilotID, SendChatRequest{Scope: ChatScopeChannel, ChannelNumber: 987654, Text: "on frequency"}); err != nil {
		t.Fatalf("send assigned channel: %v", err)
	}
	if _, err := chat.Send(ctx, pilotID, SendChatRequest{Scope: ChatScopePublic, Text: "too fast"}); !errors.Is(err, ErrChatRateLimited) {
		t.Fatalf("rapid send error = %v, want rate limited", err)
	}
}
