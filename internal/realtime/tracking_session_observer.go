package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

const roomEventChannel = "parabeacon:realtime:room-events"

type roomAccessEvent struct {
	RoomID  string `json:"room_id"`
	OwnerID string `json:"owner_id"`
	Ended   bool   `json:"ended"`
}

func (h *Hub) Start() {
	h.ensureStarted()
}

func (h *Hub) ensureStarted() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.started || h.ctx.Err() != nil {
		return
	}
	h.started = true
	h.workers.Add(1)
	go h.broadcastLoop()
	if h.redis != nil {
		h.workers.Add(1)
		go h.roomEventLoop()
	}
}

func (h *Hub) PublicSharingChanged(ownerID, sessionID uuid.UUID, enabled bool) {
	if enabled || ownerID == uuid.Nil || sessionID == uuid.Nil {
		return
	}
	event := roomAccessEvent{RoomID: sessionID.String(), OwnerID: ownerID.String()}
	h.applyRoomAccessEvent(event)
	h.publishRoomAccessEvent(event)
}

func (h *Hub) LiveSessionEnded(ownerID, sessionID uuid.UUID) {
	if sessionID == uuid.Nil {
		return
	}
	event := roomAccessEvent{RoomID: sessionID.String(), OwnerID: ownerID.String(), Ended: true}
	h.applyRoomAccessEvent(event)
	h.publishRoomAccessEvent(event)
}

func (h *Hub) publishRoomAccessEvent(event roomAccessEvent) {
	if h.redis == nil {
		return
	}
	payload, err := json.Marshal(event)
	if err != nil {
		h.logger.Printf("encode realtime room event: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(h.ctx, time.Second)
	defer cancel()
	if err := h.redis.Publish(ctx, roomEventChannel, payload).Err(); err != nil && !errors.Is(err, context.Canceled) {
		h.logger.Printf("publish realtime room event room=%s: %v", event.RoomID, err)
	}
}

func (h *Hub) applyRoomAccessEvent(event roomAccessEvent) {
	roomID, err := uuid.Parse(event.RoomID)
	if err != nil {
		h.logger.Printf("invalid realtime room event room_id=%q", event.RoomID)
		return
	}
	ownerID, _ := uuid.Parse(event.OwnerID)
	h.mu.Lock()
	if current := h.rooms[roomID]; current != nil {
		for c := range current.clients {
			if event.Ended || (c.userID != ownerID && !c.canPublish) {
				c.close()
				_ = c.conn.Close()
			}
		}
	}
	h.mu.Unlock()
}

func (h *Hub) roomEventLoop() {
	defer h.workers.Done()
	attempt := 0
	for h.ctx.Err() == nil {
		pubsub := h.redis.Subscribe(h.ctx, roomEventChannel)
		if _, err := pubsub.Receive(h.ctx); err != nil {
			_ = pubsub.Close()
			if h.ctx.Err() != nil {
				return
			}
			h.logger.Printf("subscribe realtime room events: %v", err)
			if !waitContext(h.ctx, time.Second*time.Duration(1<<min(attempt, 5))) {
				return
			}
			attempt++
			continue
		}
		attempt = 0
		messages := pubsub.Channel()
		connected := true
		for connected {
			select {
			case <-h.ctx.Done():
				_ = pubsub.Close()
				return
			case message, ok := <-messages:
				if !ok {
					connected = false
					continue
				}
				var event roomAccessEvent
				if err := json.Unmarshal([]byte(message.Payload), &event); err != nil {
					h.logger.Printf("decode realtime room event: %v", err)
					continue
				}
				h.applyRoomAccessEvent(event)
			}
		}
		_ = pubsub.Close()
		if !waitContext(h.ctx, time.Second) {
			return
		}
	}
}

func waitContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
