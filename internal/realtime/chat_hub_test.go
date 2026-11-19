package realtime

import (
	"context"
	"log"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/service"
)

func TestChatTicketsAreShortLivedAndSingleUse(t *testing.T) {
	hub := NewChatHub(nil, nil, log.New(testLogWriter{t}, "", 0))
	defer hub.Close()
	userID := uuid.New()
	ticket, err := hub.IssueTicket(context.Background(), userID)
	if err != nil {
		t.Fatalf("IssueTicket() error = %v", err)
	}
	if len(ticket) < 40 {
		t.Fatalf("ticket length = %d, want at least 40 characters", len(ticket))
	}
	got, ok := hub.consumeTicket(context.Background(), ticket)
	if !ok || got != userID {
		t.Fatalf("consumeTicket() = %s, %v; want %s, true", got, ok, userID)
	}
	if _, ok := hub.consumeTicket(context.Background(), ticket); ok {
		t.Fatal("chat ticket was accepted more than once")
	}

	hub.ticketsMu.Lock()
	hub.tickets["expired"] = localChatTicket{userID: userID, expiresAt: time.Now().Add(-time.Second)}
	hub.ticketsMu.Unlock()
	if _, ok := hub.consumeTicket(context.Background(), "expired"); ok {
		t.Fatal("expired chat ticket was accepted")
	}
}

func TestOnlineUsersReportsAuthenticatedSocketConnections(t *testing.T) {
	hub := NewChatHub(nil, nil, log.New(testLogWriter{t}, "", 0))
	defer hub.Close()
	friendID, otherID := uuid.New(), uuid.New()
	client := &chatClient{userID: friendID}
	secondDevice := &chatClient{userID: friendID}
	hub.mu.Lock()
	hub.clients[client] = struct{}{}
	hub.clients[secondDevice] = struct{}{}
	hub.mu.Unlock()

	statuses, err := hub.OnlineUsers(context.Background(), []uuid.UUID{friendID, otherID, friendID})
	if err != nil {
		t.Fatalf("OnlineUsers() error = %v", err)
	}
	if !statuses[friendID] || statuses[otherID] {
		t.Fatalf("online statuses = %#v, want friend online and other offline", statuses)
	}
	onlineIDs, err := hub.OnlineUserIDs(context.Background())
	if err != nil || len(onlineIDs) != 1 || onlineIDs[0] != friendID {
		t.Fatalf("OnlineUserIDs() = %v, err=%v; want friend", onlineIDs, err)
	}

	hub.mu.Lock()
	delete(hub.clients, client)
	hub.mu.Unlock()
	statuses, err = hub.OnlineUsers(context.Background(), []uuid.UUID{friendID})
	if err != nil || !statuses[friendID] {
		t.Fatalf("pilot with another device online=%v err=%v, want online", statuses[friendID], err)
	}

	hub.mu.Lock()
	delete(hub.clients, secondDevice)
	hub.mu.Unlock()
	statuses, err = hub.OnlineUsers(context.Background(), []uuid.UUID{friendID})
	if err != nil || statuses[friendID] {
		t.Fatalf("disconnected pilot online=%v err=%v, want offline", statuses[friendID], err)
	}
}

func TestChatAudienceRoutesOnlyToMatchingClients(t *testing.T) {
	sender := uuid.New()
	recipient := uuid.New()
	outsider := uuid.New()
	channel := 456789
	public := chatAudience{Scope: service.ChatScopePublic}
	channelAudience := chatAudience{Scope: service.ChatScopeChannel, ChannelNumber: channel}
	direct := chatAudience{Scope: service.ChatScopeDirect, SenderID: sender, RecipientID: recipient}

	clients := []struct {
		client *chatClient
		want   []bool
	}{
		{&chatClient{userID: sender}, []bool{true, false, false}},
		{&chatClient{userID: outsider, channel: channel}, []bool{true, true, false}},
		{&chatClient{userID: recipient, peerID: sender}, []bool{true, false, true}},
		{&chatClient{userID: outsider, peerID: sender}, []bool{true, false, false}},
	}
	for _, testCase := range clients {
		got := []bool{
			testCase.client.matches(public),
			testCase.client.matches(channelAudience),
			testCase.client.matches(direct),
		}
		for index := range got {
			if got[index] != testCase.want[index] {
				t.Errorf("client user=%s subscriptions=%#v audience index %d = %v, want %v", testCase.client.userID, testCase.client, index, got[index], testCase.want[index])
			}
		}
	}
}

type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(data []byte) (int, error) {
	w.t.Log(string(data))
	return len(data), nil
}
