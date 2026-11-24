package service

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/repository"
	"gorm.io/gorm"
)

type chatReadPair struct {
	owner uuid.UUID
	peer  uuid.UUID
}

type memoryChatRepository struct {
	users          map[uuid.UUID]repository.ChatUser
	messages       []model.ChatMessage
	media          map[uuid.UUID]model.ChatMedia
	locationStatus map[int]bool
	pins           map[uuid.UUID]map[uuid.UUID]time.Time
	readStates     map[chatReadPair]time.Time
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
	for id, user := range r.users {
		if id == requesterID || user.PilotNumber <= afterPilotNumber {
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
			AvatarID:    user.AvatarID,
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

func (r *memoryChatRepository) ListChatConversations(_ context.Context, requesterID uuid.UUID, query string, onlineIDs []uuid.UUID, offset, limit int) ([]repository.ChatConversation, error) {
	query = strings.ToLower(query)
	onlineSet := make(map[uuid.UUID]struct{}, len(onlineIDs))
	for _, id := range onlineIDs {
		onlineSet[id] = struct{}{}
	}
	conversations := make([]repository.ChatConversation, 0)
	for id, user := range r.users {
		if id == requesterID {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(user.Username), query) && !strings.Contains(strings.ToLower(user.DisplayName), query) {
			continue
		}
		_, online := onlineSet[id]
		conversation := repository.ChatConversation{
			PeerID: id, Online: online, PilotNumber: user.PilotNumber, Username: user.Username,
			DisplayName: user.DisplayName, AvatarID: user.AvatarID,
		}
		if lastReadAt, ok := r.readStates[chatReadPair{owner: requesterID, peer: id}]; ok {
			for _, message := range r.messages {
				if message.Scope == ChatScopeDirect && message.RecipientID != nil &&
					message.SenderID == id && *message.RecipientID == requesterID &&
					message.CreatedAt.After(lastReadAt) {
					conversation.UnreadCount++
				}
			}
		}
		for _, message := range r.messages {
			if message.Scope != ChatScopeDirect || message.RecipientID == nil ||
				!((message.SenderID == requesterID && *message.RecipientID == id) ||
					(message.SenderID == id && *message.RecipientID == requesterID)) {
				continue
			}
			if conversation.LastChatAt == nil || message.CreatedAt.After(*conversation.LastChatAt) {
				lastChatAt := message.CreatedAt
				conversation.LastChatAt = &lastChatAt
			}
		}
		if pinnedAt, ok := r.pins[requesterID][id]; ok {
			conversation.Pinned = true
			pinnedAtCopy := pinnedAt
			conversation.PinnedAt = &pinnedAtCopy
		}
		conversations = append(conversations, conversation)
	}
	sort.Slice(conversations, func(i, j int) bool {
		left, right := conversations[i], conversations[j]
		if left.Pinned != right.Pinned {
			return left.Pinned
		}
		if left.Online != right.Online {
			return left.Online
		}
		if left.LastChatAt != nil || right.LastChatAt != nil {
			if left.LastChatAt == nil {
				return false
			}
			if right.LastChatAt == nil {
				return true
			}
			if !left.LastChatAt.Equal(*right.LastChatAt) {
				return left.LastChatAt.After(*right.LastChatAt)
			}
		}
		leftName := strings.ToLower(strings.TrimSpace(left.DisplayName))
		if leftName == "" {
			leftName = strings.ToLower(left.Username)
		}
		rightName := strings.ToLower(strings.TrimSpace(right.DisplayName))
		if rightName == "" {
			rightName = strings.ToLower(right.Username)
		}
		if leftName != rightName {
			return leftName < rightName
		}
		return left.PilotNumber < right.PilotNumber
	})
	if offset >= len(conversations) {
		return []repository.ChatConversation{}, nil
	}
	conversations = conversations[offset:]
	if len(conversations) > limit {
		conversations = conversations[:limit]
	}
	return conversations, nil
}

func (r *memoryChatRepository) SetChatConversationPinned(_ context.Context, ownerID, peerID uuid.UUID, pinned bool, pinnedAt time.Time) error {
	if r.pins == nil {
		r.pins = make(map[uuid.UUID]map[uuid.UUID]time.Time)
	}
	if r.pins[ownerID] == nil {
		r.pins[ownerID] = make(map[uuid.UUID]time.Time)
	}
	if pinned {
		r.pins[ownerID][peerID] = pinnedAt
	} else {
		delete(r.pins[ownerID], peerID)
	}
	return nil
}

func (r *memoryChatRepository) EnsureChatConversationRead(_ context.Context, ownerID, peerID uuid.UUID, readAt time.Time) error {
	if r.readStates == nil {
		r.readStates = make(map[chatReadPair]time.Time)
	}
	key := chatReadPair{owner: ownerID, peer: peerID}
	if _, exists := r.readStates[key]; !exists {
		r.readStates[key] = readAt
	}
	return nil
}

func (r *memoryChatRepository) MarkChatConversationRead(_ context.Context, ownerID, peerID uuid.UUID, readAt time.Time) error {
	if r.readStates == nil {
		r.readStates = make(map[chatReadPair]time.Time)
	}
	key := chatReadPair{owner: ownerID, peer: peerID}
	if previous, exists := r.readStates[key]; !exists || readAt.After(previous) {
		r.readStates[key] = readAt
	}
	return nil
}

func (r *memoryChatRepository) HasFreshPublicLocation(_ context.Context, pilotNumber int, _, _ time.Time) (bool, error) {
	return r.locationStatus[pilotNumber], nil
}

func (r *memoryChatRepository) CreateChatMessage(_ context.Context, message *model.ChatMessage) error {
	copy := *message
	r.messages = append(r.messages, copy)
	return nil
}

func (r *memoryChatRepository) CreateChatMessageWithMedia(ctx context.Context, message *model.ChatMessage) error {
	mediaID := *message.MediaID
	media, ok := r.media[mediaID]
	channelMatches := media.ChannelNumber == nil && message.ChannelNumber == nil ||
		media.ChannelNumber != nil && message.ChannelNumber != nil && *media.ChannelNumber == *message.ChannelNumber
	recipientMatches := media.RecipientID == nil && message.RecipientID == nil ||
		media.RecipientID != nil && message.RecipientID != nil && *media.RecipientID == *message.RecipientID
	if !ok || !media.ExpiresAt.After(time.Now()) || media.SenderID != message.SenderID ||
		media.Kind != message.Kind || media.Scope != message.Scope || media.MessageID != nil ||
		!channelMatches || !recipientMatches {
		return ErrInvalidChatRequest
	}
	media.MessageID = &message.ID
	r.media[mediaID] = media
	return r.CreateChatMessage(ctx, message)
}

func (r *memoryChatRepository) CreateChatMedia(_ context.Context, media *model.ChatMedia) error {
	if r.media == nil {
		r.media = make(map[uuid.UUID]model.ChatMedia)
	}
	r.media[media.ID] = *media
	return nil
}

func (r *memoryChatRepository) FindChatMediaForUser(_ context.Context, mediaID, requesterID uuid.UUID) (*model.ChatMedia, error) {
	media, ok := r.media[mediaID]
	if !ok || media.MessageID == nil {
		return nil, gorm.ErrRecordNotFound
	}
	viewer, viewerExists := r.users[requesterID]
	allowed := viewerExists && media.Scope == ChatScopePublic
	switch media.Scope {
	case ChatScopeChannel:
		allowed = viewerExists && media.ChannelNumber != nil && viewer.ChannelNumber == *media.ChannelNumber
	case ChatScopeDirect:
		allowed = media.SenderID == requesterID || media.RecipientID != nil && *media.RecipientID == requesterID
	}
	if !allowed {
		return nil, gorm.ErrRecordNotFound
	}
	copy := media
	return &copy, nil
}

func (r *memoryChatRepository) DeleteExpiredChatMedia(_ context.Context, before time.Time) ([]model.ChatMedia, error) {
	var expired []model.ChatMedia
	for id, media := range r.media {
		if media.MessageID == nil && !media.ExpiresAt.After(before) {
			expired = append(expired, media)
			delete(r.media, id)
		}
	}
	return expired, nil
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
		aliceID:        {ID: aliceID, PilotNumber: 100002, ChannelNumber: 456789, Username: "ridge_runner", DisplayName: "Alice Summit", AvatarID: 4},
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
	if len(byDisplayName.Accounts) != 3 || byDisplayName.Accounts[0].DisplayName != "Alice Summit" {
		t.Fatalf("display-name matches = %#v", byDisplayName.Accounts)
	}
	if byDisplayName.Accounts[0].AvatarID != 4 {
		t.Fatalf("account avatar id = %d, want 4", byDisplayName.Accounts[0].AvatarID)
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
	if len(secondPage.Accounts) != 2 || secondPage.Accounts[0].Username != "carol" || secondPage.Accounts[1].Username != "alice_other" || secondPage.NextCursor != "" {
		t.Fatalf("second page = %#v", secondPage)
	}
	for _, account := range append(firstPage.Accounts, secondPage.Accounts...) {
		if account.PilotNumber == 100001 {
			t.Fatal("account search included the requester")
		}
	}
}

func TestListConversationsSortsPinnedOnlineAndRecent(t *testing.T) {
	requesterID, recentID, oldID, pinnedID, otherChannelID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	lastRecent := now.Add(-time.Minute)
	lastOld := now.Add(-time.Hour)
	oldRecipient := oldID
	repository := &memoryChatRepository{
		users: map[uuid.UUID]repository.ChatUser{
			requesterID:    {ID: requesterID, PilotNumber: 100001, ChannelNumber: 123456, Username: "requester"},
			recentID:       {ID: recentID, PilotNumber: 100002, ChannelNumber: 123456, Username: "recent_pilot", DisplayName: "Alice Recent", AvatarID: 2},
			oldID:          {ID: oldID, PilotNumber: 100003, ChannelNumber: 123456, Username: "old_pilot", DisplayName: "Bob Offline"},
			pinnedID:       {ID: pinnedID, PilotNumber: 100004, ChannelNumber: 123456, Username: "pinned_pilot", DisplayName: "Zoe Pinned"},
			otherChannelID: {ID: otherChannelID, PilotNumber: 100005, ChannelNumber: 654321, Username: "other_frequency", DisplayName: "Aaron Other"},
		},
		messages: []model.ChatMessage{
			{ID: uuid.New(), Scope: ChatScopeDirect, SenderID: requesterID, RecipientID: &oldRecipient, CreatedAt: lastOld},
			{ID: uuid.New(), Scope: ChatScopeDirect, SenderID: recentID, RecipientID: &requesterID, CreatedAt: lastRecent},
		},
	}
	chat := NewChatService(repository)
	onlineIDs := []uuid.UUID{recentID, pinnedID, otherChannelID}

	if err := chat.SetConversationPinned(context.Background(), requesterID, 100004, true); err != nil {
		t.Fatalf("pin online conversation: %v", err)
	}
	if err := chat.SetConversationPinned(context.Background(), requesterID, 100003, true); err != nil {
		t.Fatalf("pin offline conversation: %v", err)
	}
	page, err := chat.ListConversations(context.Background(), requesterID, onlineIDs, "", "", 2)
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	if len(page.Conversations) != 2 || page.NextCursor != "2" {
		t.Fatalf("first conversation page = %#v", page)
	}
	if page.Conversations[0].PilotNumber != 100004 || !page.Conversations[0].Online || !page.Conversations[0].Pinned {
		t.Fatalf("first conversation = %#v", page.Conversations[0])
	}
	if page.Conversations[1].PilotNumber != 100003 || page.Conversations[1].Online || !page.Conversations[1].Pinned {
		t.Fatalf("offline pinned pilot must precede unpinned online pilots: %#v", page.Conversations[1])
	}
	secondPage, err := chat.ListConversations(context.Background(), requesterID, onlineIDs, "", page.NextCursor, 2)
	if err != nil || len(secondPage.Conversations) != 2 {
		t.Fatalf("second conversation page = %#v, err=%v", secondPage, err)
	}
	if secondPage.Conversations[0].PilotNumber != 100002 || !secondPage.Conversations[0].Online || secondPage.Conversations[0].LastChatAt == nil || !secondPage.Conversations[0].LastChatAt.Equal(lastRecent) {
		t.Fatalf("most recent online conversation = %#v", secondPage.Conversations[0])
	}
	if secondPage.Conversations[0].AvatarID != 2 {
		t.Fatalf("conversation avatar id = %d, want 2", secondPage.Conversations[0].AvatarID)
	}
	if secondPage.Conversations[1].PilotNumber != 100005 || !secondPage.Conversations[1].Online {
		t.Fatalf("next online pilot must precede offline conversations: %#v", secondPage.Conversations[1])
	}
	searched, err := chat.ListConversations(context.Background(), requesterID, onlineIDs, "recent", "", 10)
	if err != nil || len(searched.Conversations) != 1 || searched.Conversations[0].PilotNumber != 100002 {
		t.Fatalf("filtered conversation list = %#v, err=%v", searched, err)
	}

	if err := chat.SetConversationPinned(context.Background(), requesterID, 100004, false); err != nil {
		t.Fatalf("unpin online conversation: %v", err)
	}
	if err := chat.SetConversationPinned(context.Background(), requesterID, 100003, false); err != nil {
		t.Fatalf("unpin offline conversation: %v", err)
	}
	unpinned, err := chat.ListConversations(context.Background(), requesterID, onlineIDs, "", "", 10)
	if err != nil || len(unpinned.Conversations) != 4 || unpinned.Conversations[0].PilotNumber != 100002 || unpinned.Conversations[1].PilotNumber != 100005 || unpinned.Conversations[2].PilotNumber != 100004 || unpinned.Conversations[3].PilotNumber != 100003 {
		t.Fatalf("pinned/online/recent ordering = %#v, err=%v", unpinned, err)
	}
	if _, err := chat.ListConversations(context.Background(), requesterID, onlineIDs, "x", "", 10); !errors.Is(err, ErrInvalidChatRequest) {
		t.Fatalf("short conversation search error = %v, want invalid request", err)
	}
}

func TestDirectConversationUnreadCountTracksIncomingMessagesAndReadState(t *testing.T) {
	ownerID, peerID := uuid.New(), uuid.New()
	repository := &memoryChatRepository{users: map[uuid.UUID]repository.ChatUser{
		ownerID: {ID: ownerID, PilotNumber: 123456, Username: "owner"},
		peerID:  {ID: peerID, PilotNumber: 234567, Username: "peer"},
	}}
	chat := NewChatService(repository)

	for index, text := range []string{"first unread", "second unread"} {
		if index > 0 {
			chat.lastSent[peerID] = time.Now().Add(-time.Second)
		}
		if _, err := chat.Send(context.Background(), peerID, SendChatRequest{
			Scope: ChatScopeDirect, PilotNumber: 123456, Text: text,
		}); err != nil {
			t.Fatalf("send direct message %q: %v", text, err)
		}
	}
	page, err := chat.ListConversations(context.Background(), ownerID, nil, "", "", 10)
	if err != nil || len(page.Conversations) != 1 || page.Conversations[0].UnreadCount != 2 {
		t.Fatalf("unread conversations = %#v, err=%v; want count 2", page, err)
	}

	time.Sleep(2 * time.Millisecond)
	if err := chat.MarkConversationRead(context.Background(), ownerID, 234567); err != nil {
		t.Fatalf("mark direct conversation read: %v", err)
	}
	page, err = chat.ListConversations(context.Background(), ownerID, nil, "", "", 10)
	if err != nil || len(page.Conversations) != 1 || page.Conversations[0].UnreadCount != 0 {
		t.Fatalf("read conversation = %#v, err=%v; want count 0", page, err)
	}

	time.Sleep(2 * time.Millisecond)
	chat.lastSent[peerID] = time.Now().Add(-time.Second)
	if _, err := chat.Send(context.Background(), peerID, SendChatRequest{
		Scope: ChatScopeDirect, PilotNumber: 123456, Text: "new unread",
	}); err != nil {
		t.Fatalf("send new direct message: %v", err)
	}
	page, err = chat.ListConversations(context.Background(), ownerID, nil, "", "", 10)
	if err != nil || len(page.Conversations) != 1 || page.Conversations[0].UnreadCount != 1 {
		t.Fatalf("new unread conversation = %#v, err=%v; want count 1", page, err)
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

func TestChannelChatStaysOnFrequencyButDirectMessagesReachAllPilots(t *testing.T) {
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
	peerID, err = chat.SubscribePeer(context.Background(), requesterID, 345678)
	if err != nil || peerID != otherChannelID {
		t.Fatalf("cross-frequency private peer = %s, err=%v", peerID, err)
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
		aliceID: {ID: aliceID, PilotNumber: 123456, ChannelNumber: channelNumber, Username: "alice", DisplayName: "Alice Ridge", AvatarID: 5, ProfileImageURL: "/api/v1/profile-images/bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"},
		bobID:   {ID: bobID, PilotNumber: 234567, ChannelNumber: channelNumber, Username: "bob"},
		carolID: {ID: carolID, PilotNumber: 345678, ChannelNumber: channelNumber, Username: "carol"},
	}}
	chat := NewChatService(repository)

	publicMessage, err := chat.Send(ctx, aliceID, SendChatRequest{Scope: ChatScopePublic, Text: " hello world "})
	if err != nil {
		t.Fatalf("send public: %v", err)
	}
	if publicMessage.Text != "hello world" || publicMessage.SenderPilotNumber != 123456 || publicMessage.Scope != ChatScopePublic || publicMessage.SenderName != "Alice Ridge" || publicMessage.SenderAvatarID != 5 || publicMessage.SenderProfileImageURL == "" {
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
	message, err := chat.Send(ctx, aliceID, SendChatRequest{Scope: ChatScopeDirect, PilotNumber: 234567, Text: "different frequency"})
	if err != nil || message.RecipientID != bobID {
		t.Fatalf("cross-channel direct message = %#v, err=%v", message, err)
	}
	history, err := chat.History(ctx, aliceID, ChatScopeDirect, 0, 234567, "")
	if err != nil || len(history.Messages) != 1 || history.Messages[0].ID != message.ID {
		t.Fatalf("cross-channel direct history = %#v, err=%v", history, err)
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

func TestChatSendsImageVoiceAndValidatedLocationMessages(t *testing.T) {
	ctx := context.Background()
	aliceID, bobID := uuid.New(), uuid.New()
	imageID, voiceID := uuid.New(), uuid.New()
	bobPilot := 234567
	repository := &memoryChatRepository{
		users: map[uuid.UUID]repository.ChatUser{
			aliceID: {ID: aliceID, PilotNumber: 123456, ChannelNumber: 345678, Username: "alice"},
			bobID:   {ID: bobID, PilotNumber: bobPilot, ChannelNumber: 456789, Username: "bob"},
		},
		media: map[uuid.UUID]model.ChatMedia{
			imageID: {
				ID: imageID, SenderID: aliceID, Scope: ChatScopeDirect,
				RecipientID: &bobID, Kind: ChatKindImage, ExpiresAt: time.Now().Add(time.Hour),
			},
			voiceID: {
				ID: voiceID, SenderID: aliceID, Scope: ChatScopeDirect,
				RecipientID: &bobID, Kind: ChatKindVoice, ExpiresAt: time.Now().Add(time.Hour),
			},
		},
	}
	chat := NewChatService(repository)
	imageMessage, err := chat.Send(ctx, aliceID, SendChatRequest{
		Scope: ChatScopeDirect, PilotNumber: bobPilot, Kind: ChatKindImage, MediaID: &imageID,
	})
	if err != nil || imageMessage.Kind != ChatKindImage || imageMessage.MediaID == nil || *imageMessage.MediaID != imageID {
		t.Fatalf("send image = %#v, err=%v", imageMessage, err)
	}
	chat.lastSent[aliceID] = time.Now().Add(-time.Second)
	voiceMessage, err := chat.Send(ctx, aliceID, SendChatRequest{
		Scope: ChatScopeDirect, PilotNumber: bobPilot, Kind: ChatKindVoice, MediaID: &voiceID,
	})
	if err != nil || voiceMessage.Kind != ChatKindVoice || voiceMessage.MediaID == nil || *voiceMessage.MediaID != voiceID {
		t.Fatalf("send voice = %#v, err=%v", voiceMessage, err)
	}
	latitude, longitude := 37.4219999, -122.0840575
	chat.lastSent[aliceID] = time.Now().Add(-time.Second)
	locationMessage, err := chat.Send(ctx, aliceID, SendChatRequest{
		Scope: ChatScopeDirect, PilotNumber: bobPilot, Kind: ChatKindLocation,
		Latitude: &latitude, Longitude: &longitude,
	})
	if err != nil || locationMessage.Kind != ChatKindLocation || locationMessage.Latitude == nil ||
		*locationMessage.Latitude != latitude || locationMessage.Longitude == nil || *locationMessage.Longitude != longitude {
		t.Fatalf("send location = %#v, err=%v", locationMessage, err)
	}
	badLatitude := math.NaN()
	if _, err := chat.Send(ctx, aliceID, SendChatRequest{
		Scope: ChatScopeDirect, PilotNumber: bobPilot, Kind: ChatKindLocation,
		Latitude: &badLatitude, Longitude: &longitude,
	}); !errors.Is(err, ErrInvalidChatRequest) {
		t.Fatalf("send NaN location error = %v, want invalid request", err)
	}
}
