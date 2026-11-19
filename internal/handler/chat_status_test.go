package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/middleware"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/realtime"
	"github.com/parabeacon/server/internal/repository"
	"github.com/parabeacon/server/internal/service"
	"gorm.io/gorm"
)

type chatStatusTestRepository struct {
	pilotID       uuid.UUID
	requesterID   uuid.UUID
	sharing       bool
	accounts      []repository.ChatAccount
	searchQuery   string
	searchAfter   int
	searchLimit   int
	conversations []repository.ChatConversation
	pinnedPeer    uuid.UUID
	pinned        bool
}

func (r *chatStatusTestRepository) FindChatUserByID(_ context.Context, id uuid.UUID) (*repository.ChatUser, error) {
	if id == r.requesterID {
		return &repository.ChatUser{ID: id, PilotNumber: 123456, ChannelNumber: 678901, Username: "requester"}, nil
	}
	if id != r.pilotID {
		return nil, gorm.ErrRecordNotFound
	}
	return &repository.ChatUser{ID: r.pilotID, PilotNumber: 234567, ChannelNumber: 678901, Username: "partner"}, nil
}

func (r *chatStatusTestRepository) FindChatUserByPilotNumber(_ context.Context, number int) (*repository.ChatUser, error) {
	if number != 234567 {
		return nil, gorm.ErrRecordNotFound
	}
	return &repository.ChatUser{ID: r.pilotID, PilotNumber: number, ChannelNumber: 678901, Username: "partner"}, nil
}

func (r *chatStatusTestRepository) SearchChatAccounts(_ context.Context, _ uuid.UUID, query string, after, limit int) ([]repository.ChatAccount, error) {
	r.searchQuery = query
	r.searchAfter = after
	r.searchLimit = limit
	accounts := make([]repository.ChatAccount, 0, len(r.accounts))
	for _, account := range r.accounts {
		if account.PilotNumber > after {
			accounts = append(accounts, account)
		}
	}
	if len(accounts) > limit {
		accounts = accounts[:limit]
	}
	return accounts, nil
}

func (r *chatStatusTestRepository) ListChatConversations(_ context.Context, _ uuid.UUID, _ string, onlineIDs []uuid.UUID, offset, limit int) ([]repository.ChatConversation, error) {
	if offset >= len(r.conversations) {
		return []repository.ChatConversation{}, nil
	}
	onlineSet := make(map[uuid.UUID]struct{}, len(onlineIDs))
	for _, id := range onlineIDs {
		onlineSet[id] = struct{}{}
	}
	conversations := append([]repository.ChatConversation(nil), r.conversations[offset:]...)
	for index := range conversations {
		_, conversations[index].Online = onlineSet[conversations[index].PeerID]
	}
	if len(conversations) > limit {
		conversations = conversations[:limit]
	}
	return conversations, nil
}

func (r *chatStatusTestRepository) SetChatConversationPinned(_ context.Context, _, peerID uuid.UUID, pinned bool, _ time.Time) error {
	r.pinnedPeer = peerID
	r.pinned = pinned
	return nil
}

func (r *chatStatusTestRepository) HasFreshPublicLocation(_ context.Context, number int, _, _ time.Time) (bool, error) {
	return number == 234567 && r.sharing, nil
}

func (*chatStatusTestRepository) CreateChatMessage(context.Context, *model.ChatMessage) error {
	return nil
}

func (*chatStatusTestRepository) ListPublicChat(context.Context, time.Time, uuid.UUID, int) ([]model.ChatMessage, error) {
	return nil, nil
}

func (*chatStatusTestRepository) ListChannelChat(context.Context, int, time.Time, uuid.UUID, int) ([]model.ChatMessage, error) {
	return nil, nil
}

func (*chatStatusTestRepository) ListDirectChat(context.Context, uuid.UUID, uuid.UUID, time.Time, uuid.UUID, int) ([]model.ChatMessage, error) {
	return nil, nil
}

func TestSearchChatAccountsRequiresAuthenticationAndReturnsMinimalResults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requesterID := uuid.New()
	repository := &chatStatusTestRepository{
		pilotID: requesterID,
		accounts: []repository.ChatAccount{{
			PilotNumber: 234567,
			Username:    "sky_pilot",
			DisplayName: "Sky Pilot",
		}},
	}
	handler := NewChatHandler(service.NewChatService(repository), nil)
	engine := gin.New()
	engine.GET("/chat/accounts", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, requesterID)
		handler.SearchAccounts(c)
	})

	request := httptest.NewRequest(http.MethodGet, "/chat/accounts?q=Sky&cursor=123456&limit=5", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if repository.searchQuery != "Sky" || repository.searchAfter != 123456 || repository.searchLimit != 6 {
		t.Fatalf("search args = %q, %d, %d", repository.searchQuery, repository.searchAfter, repository.searchLimit)
	}
	var responseBody struct {
		Accounts []map[string]json.RawMessage `json:"accounts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &responseBody); err != nil {
		t.Fatalf("decode account search: %v", err)
	}
	if len(responseBody.Accounts) != 1 {
		t.Fatalf("account count = %d, want 1", len(responseBody.Accounts))
	}
	for _, forbidden := range []string{"email", "id", "password_hash"} {
		if _, exists := responseBody.Accounts[0][forbidden]; exists {
			t.Errorf("account result exposed %q", forbidden)
		}
	}

	anonymousEngine := gin.New()
	anonymousEngine.GET("/chat/accounts", handler.SearchAccounts)
	request = httptest.NewRequest(http.MethodGet, "/chat/accounts", nil)
	response = httptest.NewRecorder()
	anonymousEngine.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestListConversationsIncludesOnlinePinAndRecentActivity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requesterID, peerID := uuid.New(), uuid.New()
	lastChatAt := time.Now().UTC().Add(-time.Minute)
	repository := &chatStatusTestRepository{
		requesterID: requesterID,
		conversations: []repository.ChatConversation{{
			PeerID: peerID, PilotNumber: 234567, Username: "friend", DisplayName: "Sky Friend",
			AvatarID: 3, ProfileImageURL: "/api/v1/profile-images/bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
			LastChatAt: &lastChatAt, Pinned: true, PinnedAt: &lastChatAt,
		}},
	}
	chat := service.NewChatService(repository)
	hub := realtime.NewChatHub(nil, chat, nil)
	defer hub.Close()
	handler := NewChatHandler(chat, hub)
	engine := gin.New()
	engine.GET("/chat/conversations", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, requesterID)
		handler.ListConversations(c)
	})
	request := httptest.NewRequest(http.MethodGet, "/chat/conversations?q=Sky&cursor=0&limit=10", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var body struct {
		Conversations []struct {
			PilotNumber     int        `json:"pilot_number"`
			AvatarID        int        `json:"avatar_id"`
			ProfileImageURL string     `json:"profile_image_url"`
			Online          bool       `json:"online"`
			Pinned          bool       `json:"pinned"`
			LastChatAt      *time.Time `json:"last_chat_at"`
		} `json:"conversations"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode conversation list: %v", err)
	}
	if len(body.Conversations) != 1 {
		t.Fatalf("conversation count = %d, want 1", len(body.Conversations))
	}
	conversation := body.Conversations[0]
	if conversation.PilotNumber != 234567 || conversation.AvatarID != 3 || conversation.ProfileImageURL == "" || !conversation.Pinned || conversation.Online || conversation.LastChatAt == nil {
		t.Fatalf("conversation = %#v", conversation)
	}
}

func TestSetConversationPinnedRequiresAuthenticatedSameChannelPeer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requesterID, peerID := uuid.New(), uuid.New()
	repository := &chatStatusTestRepository{requesterID: requesterID, pilotID: peerID}
	chat := service.NewChatService(repository)
	hub := realtime.NewChatHub(nil, chat, nil)
	defer hub.Close()
	handler := NewChatHandler(chat, hub)
	engine := gin.New()
	engine.PUT("/chat/conversations/:pilot_number/pin", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, requesterID)
		handler.SetConversationPinned(c)
	})
	request := httptest.NewRequest(http.MethodPut, "/chat/conversations/234567/pin", strings.NewReader(`{"pinned":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || repository.pinnedPeer != peerID || !repository.pinned {
		t.Fatalf("pin response = %d %s, peer=%s pinned=%v", response.Code, response.Body.String(), repository.pinnedPeer, repository.pinned)
	}

	anonymous := gin.New()
	anonymous.PUT("/chat/conversations/:pilot_number/pin", handler.SetConversationPinned)
	request = httptest.NewRequest(http.MethodPut, "/chat/conversations/234567/pin", strings.NewReader(`{"pinned":true}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	anonymous.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous pin status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestPilotLocationStatusReturnsOnlyBooleanForAuthenticatedChatUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requesterID := uuid.New()
	partnerID := uuid.New()
	repository := &chatStatusTestRepository{pilotID: partnerID, requesterID: requesterID, sharing: true}
	handler := NewChatHandler(service.NewChatService(repository), nil)
	engine := gin.New()
	engine.GET("/chat/pilots/:pilot_number/location-status", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, requesterID)
		handler.PilotLocationStatus(c)
	})

	request := httptest.NewRequest(http.MethodGet, "/chat/pilots/234567/location-status", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	var sharing bool
	if err := json.Unmarshal(body["actively_sharing_location"], &sharing); err != nil || !sharing {
		t.Fatalf("sharing = %v, err=%v; want true", sharing, err)
	}
	if len(body) != 1 {
		t.Fatalf("location-status exposed extra fields: %v", body)
	}

	request = httptest.NewRequest(http.MethodGet, "/chat/pilots/345678/location-status", nil)
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unknown pilot response = %d %s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode unknown pilot status: %v", err)
	}
	if err := json.Unmarshal(body["actively_sharing_location"], &sharing); err != nil || sharing {
		t.Fatalf("unknown pilot sharing = %v, err=%v; want false", sharing, err)
	}
	if len(body) != 1 {
		t.Fatalf("unknown pilot response exposed extra fields: %v", body)
	}
}

func TestPilotLocationStatusRequiresAuthenticationAndValidPilotNumber(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requesterID := uuid.New()
	repository := &chatStatusTestRepository{pilotID: uuid.New(), requesterID: requesterID, sharing: true}
	handler := NewChatHandler(service.NewChatService(repository), nil)
	engine := gin.New()
	engine.GET("/chat/pilots/:pilot_number/location-status", func(c *gin.Context) {
		c.Set(middleware.AuthenticatedUserIDKey, requesterID)
		handler.PilotLocationStatus(c)
	})

	request := httptest.NewRequest(http.MethodGet, "/chat/pilots/123/location-status", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid pilot number status = %d, want %d", response.Code, http.StatusBadRequest)
	}

	anonymousEngine := gin.New()
	anonymousEngine.GET("/chat/pilots/:pilot_number/location-status", handler.PilotLocationStatus)
	request = httptest.NewRequest(http.MethodGet, "/chat/pilots/234567/location-status", nil)
	response = httptest.NewRecorder()
	anonymousEngine.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}
