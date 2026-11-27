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
	RoomID           string `json:"room_id,omitempty"`
	OwnerID          string `json:"owner_id"`
	Ended            bool   `json:"ended,omitempty"`
	DeleteData       bool   `json:"delete_data,omitempty"`
	TrackingDisabled bool   `json:"tracking_disabled,omitempty"`
	TrackingEnabled  bool   `json:"tracking_enabled,omitempty"`
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

func (h *Hub) LiveSessionStarted(ownerID, sessionID uuid.UUID) {
	if ownerID == uuid.Nil || sessionID == uuid.Nil {
		return
	}
	event := roomAccessEvent{RoomID: sessionID.String(), OwnerID: ownerID.String(), TrackingEnabled: true}
	h.applyRoomAccessEvent(event)
	h.publishRoomAccessEvent(event)
}

func (h *Hub) LiveTrackingDataDeleted(ownerID uuid.UUID, sessionIDs []uuid.UUID) {
	if ownerID == uuid.Nil {
		return
	}
	event := roomAccessEvent{OwnerID: ownerID.String(), TrackingDisabled: true}
	h.applyRoomAccessEvent(event)
	h.publishRoomAccessEvent(event)
	for _, sessionID := range sessionIDs {
		if sessionID == uuid.Nil {
			continue
		}
		roomEvent := roomAccessEvent{RoomID: sessionID.String(), OwnerID: ownerID.String(), Ended: true, DeleteData: true}
		h.applyRoomAccessEvent(roomEvent)
		h.publishRoomAccessEvent(roomEvent)
	}
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
	ownerID, ownerErr := uuid.Parse(event.OwnerID)
	if ownerErr != nil || ownerID == uuid.Nil {
		h.logger.Printf("invalid realtime room event owner_id=%q", event.OwnerID)
		return
	}
	var roomID uuid.UUID
	if event.RoomID != "" {
		parsed, err := uuid.Parse(event.RoomID)
		if err != nil {
			h.logger.Printf("invalid realtime room event room_id=%q", event.RoomID)
			return
		}
		roomID = parsed
	}
	h.mu.Lock()
	if event.TrackingDisabled {
		h.disabledUsers[ownerID] = true
	}
	if event.TrackingEnabled {
		delete(h.disabledUsers, ownerID)
	}
	if event.RoomID != "" {
		if current := h.rooms[roomID]; current != nil {
			if event.DeleteData {
				delete(current.locations, ownerID)
			}
			for c := range current.clients {
				if event.Ended || (c.userID != ownerID && !c.canPublish) {
					c.close()
					_ = c.conn.Close()
				}
			}
			if event.DeleteData && len(current.clients) == 0 && len(current.locations) == 0 {
				delete(h.rooms, roomID)
			}
		}
	}
	h.mu.Unlock()
	if event.TrackingDisabled && h.lifecycle != nil {
		h.lifecycle.Clear(ownerID)
	}
	if event.DeleteData {
		h.deleteRedisRoomData(roomID)
	}
}

func (h *Hub) deleteRedisRoomData(roomID uuid.UUID) {
	if h.redis == nil || roomID == uuid.Nil {
		return
	}
	ctx, cancel := context.WithTimeout(h.ctx, time.Second)
	defer cancel()
	if err := h.redis.Del(ctx, redisRoomKey(roomID), redisRoomStreamKey(roomID)).Err(); err != nil && !errors.Is(err, context.Canceled) {
		h.logger.Printf("realtime delete room data room=%s: %v", roomID, err)
	}
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
