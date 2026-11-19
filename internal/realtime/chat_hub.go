package realtime

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/parabeacon/server/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	chatEventChannel = "parabeacon:chat:events"
	chatTicketPrefix = "parabeacon:chat:ticket:"
	chatTicketTTL    = 30 * time.Second
	chatWriteWait    = 5 * time.Second
	chatPongWait     = 60 * time.Second
	chatPingPeriod   = 25 * time.Second
	chatMaxMessage   = 4096
)

var consumeChatTicketScript = redis.NewScript(`
local value = redis.call('GET', KEYS[1])
if value then redis.call('DEL', KEYS[1]) end
return value
`)

type chatAudience struct {
	Scope         string    `json:"scope"`
	ChannelNumber int       `json:"channel_number,omitempty"`
	SenderID      uuid.UUID `json:"sender_id"`
	RecipientID   uuid.UUID `json:"recipient_id,omitempty"`
}

type chatEnvelope struct {
	Audience chatAudience        `json:"audience"`
	Message  service.ChatMessage `json:"message"`
}

type chatClientMessage struct {
	Type          string `json:"type"`
	Scope         string `json:"scope,omitempty"`
	ChannelNumber int    `json:"channel_number,omitempty"`
	PilotNumber   int    `json:"pilot_number,omitempty"`
	Text          string `json:"text,omitempty"`
}

type chatServerMessage struct {
	Type    string               `json:"type"`
	Scope   string               `json:"scope,omitempty"`
	Message *service.ChatMessage `json:"message,omitempty"`
	Error   string               `json:"error,omitempty"`
}

type ChatHub struct {
	mu        sync.Mutex
	clients   map[*chatClient]struct{}
	redis     *redis.Client
	chat      *service.ChatService
	logger    *log.Logger
	ctx       context.Context
	cancel    context.CancelFunc
	workers   sync.WaitGroup
	started   bool
	ticketsMu sync.Mutex
	tickets   map[string]localChatTicket
}

type localChatTicket struct {
	userID    uuid.UUID
	expiresAt time.Time
}

type chatClient struct {
	hub             *ChatHub
	conn            *websocket.Conn
	userID          uuid.UUID
	channel         int
	peerID          uuid.UUID
	send            chan []byte
	done            chan struct{}
	closeOnce       sync.Once
	lastSentAt      time.Time
	lastSubscribeAt time.Time
}

func NewChatHub(redisClient *redis.Client, chat *service.ChatService, logger *log.Logger) *ChatHub {
	if logger == nil {
		logger = log.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &ChatHub{
		clients: make(map[*chatClient]struct{}),
		redis:   redisClient,
		chat:    chat,
		logger:  logger,
		ctx:     ctx,
		cancel:  cancel,
		tickets: make(map[string]localChatTicket),
	}
}

func (h *ChatHub) Start() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.started || h.ctx.Err() != nil {
		return
	}
	h.started = true
	if h.redis != nil {
		h.workers.Add(1)
		go h.redisEventLoop()
	}
}

func (h *ChatHub) Close() {
	h.cancel()
	h.mu.Lock()
	for client := range h.clients {
		client.close()
		_ = client.conn.Close()
	}
	h.clients = make(map[*chatClient]struct{})
	h.mu.Unlock()
	h.workers.Wait()
}

func (h *ChatHub) IssueTicket(ctx context.Context, userID uuid.UUID) (string, error) {
	if userID == uuid.Nil || h.ctx.Err() != nil {
		return "", errors.New("chat unavailable")
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate chat ticket: %w", err)
	}
	ticket := base64.RawURLEncoding.EncodeToString(random)
	if h.redis != nil {
		if err := h.redis.Set(ctx, chatTicketPrefix+ticket, userID.String(), chatTicketTTL).Err(); err != nil {
			return "", fmt.Errorf("store chat ticket: %w", err)
		}
		return ticket, nil
	}
	h.ticketsMu.Lock()
	h.pruneTicketsLocked(time.Now())
	if len(h.tickets) >= 10000 {
		h.ticketsMu.Unlock()
		return "", errors.New("chat ticket capacity reached")
	}
	h.tickets[ticket] = localChatTicket{userID: userID, expiresAt: time.Now().Add(chatTicketTTL)}
	h.ticketsMu.Unlock()
	return ticket, nil
}

func (h *ChatHub) consumeTicket(ctx context.Context, ticket string) (uuid.UUID, bool) {
	if ticket == "" || len(ticket) > 128 {
		return uuid.Nil, false
	}
	if h.redis != nil {
		value, err := consumeChatTicketScript.Run(ctx, h.redis, []string{chatTicketPrefix + ticket}).Text()
		if err != nil {
			return uuid.Nil, false
		}
		userID, err := uuid.Parse(value)
		return userID, err == nil && userID != uuid.Nil
	}
	h.ticketsMu.Lock()
	defer h.ticketsMu.Unlock()
	entry, exists := h.tickets[ticket]
	delete(h.tickets, ticket)
	if !exists || !time.Now().Before(entry.expiresAt) {
		return uuid.Nil, false
	}
	return entry.userID, true
}

func (h *ChatHub) ServeHTTP(allowedOrigins []string) gin.HandlerFunc {
	origins := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		origins[origin] = struct{}{}
	}
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 4096,
		CheckOrigin: func(request *http.Request) bool {
			origin := request.Header.Get("Origin")
			if origin == "" {
				return true
			}
			if _, allowed := origins[origin]; allowed {
				return true
			}
			parsed, err := url.Parse(origin)
			if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
				return false
			}
			scheme := "http"
			if request.TLS != nil {
				scheme = "https"
			}
			return parsed.Host == request.Host && parsed.Scheme == scheme
		},
	}
	return func(c *gin.Context) {
		if h.ctx.Err() != nil || h.chat == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chat unavailable"})
			return
		}
		userID, ok := h.consumeTicket(c.Request.Context(), c.Query("ticket"))
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired chat ticket"})
			return
		}
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		client := &chatClient{
			hub:    h,
			conn:   conn,
			userID: userID,
			send:   make(chan []byte, 64),
			done:   make(chan struct{}),
		}
		h.mu.Lock()
		if h.ctx.Err() != nil {
			h.mu.Unlock()
			_ = conn.Close()
			return
		}
		h.clients[client] = struct{}{}
		h.mu.Unlock()
		go client.writePump()
		client.readPump()
		h.removeClient(client)
	}
}

func (h *ChatHub) removeClient(client *chatClient) {
	h.mu.Lock()
	delete(h.clients, client)
	client.close()
	h.mu.Unlock()
	_ = client.conn.Close()
}

func (h *ChatHub) publish(envelope chatEnvelope) {
	if h.redis == nil {
		h.broadcast(envelope)
		return
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		h.logger.Printf("encode chat event: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(h.ctx, time.Second)
	defer cancel()
	if err := h.redis.Publish(ctx, chatEventChannel, payload).Err(); err != nil {
		h.logger.Printf("publish chat event scope=%s: %v", envelope.Audience.Scope, err)
		h.broadcast(envelope)
	}
}

func (h *ChatHub) broadcast(envelope chatEnvelope) {
	payload, err := json.Marshal(chatServerMessage{Type: "message", Message: &envelope.Message})
	if err != nil {
		h.logger.Printf("encode chat message id=%s: %v", envelope.Message.ID, err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for client := range h.clients {
		if client.matches(envelope.Audience) {
			client.enqueue(payload)
		}
	}
}

func (h *ChatHub) redisEventLoop() {
	defer h.workers.Done()
	attempt := 0
	for h.ctx.Err() == nil {
		pubsub := h.redis.Subscribe(h.ctx, chatEventChannel)
		if _, err := pubsub.Receive(h.ctx); err != nil {
			_ = pubsub.Close()
			if h.ctx.Err() != nil {
				return
			}
			h.logger.Printf("subscribe chat events: %v", err)
			if !waitChatContext(h.ctx, time.Second*time.Duration(1<<min(attempt, 5))) {
				return
			}
			attempt++
			continue
		}
		attempt = 0
		for message := range pubsub.Channel() {
			var envelope chatEnvelope
			if err := json.Unmarshal([]byte(message.Payload), &envelope); err != nil {
				h.logger.Printf("decode chat event: %v", err)
				continue
			}
			h.broadcast(envelope)
			select {
			case <-h.ctx.Done():
				_ = pubsub.Close()
				return
			default:
			}
		}
		_ = pubsub.Close()
		if !waitChatContext(h.ctx, time.Second) {
			return
		}
	}
}

func waitChatContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (h *ChatHub) pruneTicketsLocked(now time.Time) {
	for ticket, entry := range h.tickets {
		if !now.Before(entry.expiresAt) {
			delete(h.tickets, ticket)
		}
	}
}

func (c *chatClient) matches(audience chatAudience) bool {
	switch audience.Scope {
	case service.ChatScopePublic:
		return true
	case service.ChatScopeChannel:
		return c.channel == audience.ChannelNumber
	case service.ChatScopeDirect:
		return (c.userID == audience.SenderID && c.peerID == audience.RecipientID) ||
			(c.userID == audience.RecipientID && c.peerID == audience.SenderID)
	default:
		return false
	}
}

func (c *chatClient) enqueue(payload []byte) {
	select {
	case <-c.done:
		return
	case c.send <- payload:
	default:
		c.close()
		_ = c.conn.Close()
	}
}

func (c *chatClient) close() {
	c.closeOnce.Do(func() { close(c.done) })
}

func (c *chatClient) readPump() {
	defer c.close()
	c.conn.SetReadLimit(chatMaxMessage)
	_ = c.conn.SetReadDeadline(time.Now().Add(chatPongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(chatPongWait))
	})
	for {
		_, payload, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var incoming chatClientMessage
		if err := json.Unmarshal(payload, &incoming); err != nil {
			c.sendError("invalid_message")
			continue
		}
		switch incoming.Type {
		case "subscribe":
			c.subscribe(incoming)
		case "send":
			c.sendChat(incoming)
		default:
			c.sendError("unsupported_message")
		}
	}
}

func (c *chatClient) subscribe(incoming chatClientMessage) {
	now := time.Now()
	if !c.lastSubscribeAt.IsZero() && now.Sub(c.lastSubscribeAt) < 200*time.Millisecond {
		c.sendError("rate_limited")
		return
	}
	c.lastSubscribeAt = now
	switch incoming.Scope {
	case "public":
		if incoming.ChannelNumber != 0 || incoming.PilotNumber != 0 {
			c.sendError("invalid_subscription")
			return
		}
		c.hub.mu.Lock()
		c.channel = 0
		c.peerID = uuid.Nil
		c.hub.mu.Unlock()
		c.enqueueJSON(chatServerMessage{Type: "subscribed", Scope: service.ChatScopePublic})
	case service.ChatScopeChannel:
		ctx, cancel := context.WithTimeout(c.hub.ctx, 5*time.Second)
		err := c.hub.chat.ValidateChannel(ctx, c.userID, incoming.ChannelNumber)
		cancel()
		if incoming.PilotNumber != 0 {
			c.sendError("invalid_subscription")
			return
		}
		if err != nil {
			c.sendError(chatErrorCode(err))
			return
		}
		c.hub.mu.Lock()
		c.channel = incoming.ChannelNumber
		c.peerID = uuid.Nil
		c.hub.mu.Unlock()
		c.enqueueJSON(chatServerMessage{Type: "subscribed", Scope: fmt.Sprintf("channel:%06d", incoming.ChannelNumber)})
	case service.ChatScopeDirect:
		ctx, cancel := context.WithTimeout(c.hub.ctx, 5*time.Second)
		peerID, err := c.hub.chat.SubscribePeer(ctx, c.userID, incoming.PilotNumber)
		cancel()
		if err != nil {
			c.sendError(chatErrorCode(err))
			return
		}
		c.hub.mu.Lock()
		c.channel = 0
		c.peerID = peerID
		c.hub.mu.Unlock()
		c.enqueueJSON(chatServerMessage{Type: "subscribed", Scope: fmt.Sprintf("direct:%06d", incoming.PilotNumber)})
	default:
		c.sendError("invalid_subscription")
	}
}

func (c *chatClient) sendChat(incoming chatClientMessage) {
	text := strings.TrimSpace(incoming.Text)
	if text == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > service.ChatMaxTextRunes {
		c.sendError("invalid_message")
		return
	}
	now := time.Now()
	c.hub.mu.Lock()
	if !c.lastSentAt.IsZero() && now.Sub(c.lastSentAt) < 750*time.Millisecond {
		c.hub.mu.Unlock()
		c.sendError("rate_limited")
		return
	}
	c.lastSentAt = now
	c.hub.mu.Unlock()
	ctx, cancel := context.WithTimeout(c.hub.ctx, 5*time.Second)
	message, err := c.hub.chat.Send(ctx, c.userID, service.SendChatRequest{
		Scope: incoming.Scope, ChannelNumber: incoming.ChannelNumber,
		PilotNumber: incoming.PilotNumber, Text: text,
	})
	cancel()
	if err != nil {
		c.sendError(chatErrorCode(err))
		return
	}
	audience := chatAudience{
		Scope:         message.Scope,
		ChannelNumber: dereferenceNumber(message.ChannelNumber),
		SenderID:      c.userID,
		RecipientID:   message.RecipientID,
	}
	c.hub.publish(chatEnvelope{Audience: audience, Message: message})
}

func (c *chatClient) sendError(code string) {
	c.enqueueJSON(chatServerMessage{Type: "error", Error: code})
}

func (c *chatClient) enqueueJSON(message chatServerMessage) {
	payload, err := json.Marshal(message)
	if err != nil {
		return
	}
	c.enqueue(payload)
}

func (c *chatClient) writePump() {
	ticker := time.NewTicker(chatPingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()
	for {
		select {
		case <-c.done:
			return
		case payload := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(chatWriteWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(chatWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func dereferenceNumber(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func chatErrorCode(err error) string {
	switch {
	case errors.Is(err, service.ErrInvalidChatRequest):
		return "invalid_request"
	case errors.Is(err, service.ErrChatUserNotFound):
		return "pilot_not_found"
	case errors.Is(err, service.ErrChatRateLimited):
		return "rate_limited"
	default:
		return "chat_unavailable"
	}
}
