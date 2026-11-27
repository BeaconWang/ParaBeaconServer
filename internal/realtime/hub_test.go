package realtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/flight"
	"github.com/parabeacon/server/internal/model"
)

type realtimePointStore struct {
	userID    uuid.UUID
	sessionID uuid.UUID
	points    []model.LiveTrackPoint
	err       error
}

func (s *realtimePointStore) StorePoints(_ context.Context, userID, sessionID uuid.UUID, points []model.LiveTrackPoint) (int64, error) {
	s.userID = userID
	s.sessionID = sessionID
	s.points = append([]model.LiveTrackPoint(nil), points...)
	return int64(len(points)), s.err
}

func TestLocationUpdatePersistsLatestPointToTrackingStore(t *testing.T) {
	userID, roomID := uuid.New(), uuid.New()
	heading := 123.0
	store := &realtimePointStore{}
	hub := NewHub(nil, nil, nil, store)
	defer hub.Close()
	hub.rooms[roomID] = &room{clients: make(map[*client]struct{}), locations: make(map[uuid.UUID]Location)}
	client := &client{hub: hub, userID: userID, roomID: roomID, pilotName: "Pilot", canPublish: true}
	pointAt := time.Now().UTC()

	if err := hub.update(client, Location{
		Latitude: 47.1, Longitude: 8.5, Altitude: 1200, Speed: 12,
		Heading: &heading, Timestamp: pointAt,
	}); err != nil {
		t.Fatalf("update() error = %v", err)
	}
	if store.userID != userID || store.sessionID != roomID || len(store.points) != 1 {
		t.Fatalf("persisted point identity = user %s session %s points %d", store.userID, store.sessionID, len(store.points))
	}
	point := store.points[0]
	if point.Latitude != 47.1 || point.Longitude != 8.5 || point.Altitude != 1200 || point.GroundSpeed != 12 || point.Heading == nil || *point.Heading != heading || !point.Timestamp.Equal(pointAt) {
		t.Fatalf("persisted realtime point = %+v", point)
	}
}

func TestLocationUpdateFailsWhenDurablePersistenceFails(t *testing.T) {
	store := &realtimePointStore{err: context.DeadlineExceeded}
	hub := NewHub(nil, nil, nil, store)
	defer hub.Close()
	userID, roomID := uuid.New(), uuid.New()
	hub.rooms[roomID] = &room{clients: make(map[*client]struct{}), locations: make(map[uuid.UUID]Location)}
	client := &client{hub: hub, userID: userID, roomID: roomID, canPublish: true}
	err := hub.update(client, Location{Latitude: 47, Longitude: 8, Altitude: 100, Speed: 1, Timestamp: time.Now().UTC()})
	if err == nil {
		t.Fatal("update() succeeded without durable point persistence")
	}
	if _, exists := hub.rooms[roomID].locations[userID]; exists {
		t.Fatal("location was published to memory despite persistence failure")
	}
}

func TestResetFlightAcknowledgesPublisherAndSetsLifecycleIdle(t *testing.T) {
	engine := flight.NewLifecycleEngine(context.Background(), nil, nil)
	defer engine.Close()
	userID := uuid.New()
	base := time.Now().UTC().Add(-10 * time.Second).Truncate(time.Second)
	for second := 0; second <= 3; second++ {
		if err := engine.Observe(userID, flight.TrackPoint{
			Latitude: 37.5, Longitude: -122.5, AltitudeMeters: 100 + float64(second),
			GroundSpeedMPS: 8, Timestamp: base.Add(time.Duration(second) * time.Second),
		}); err != nil {
			t.Fatalf("Observe() error = %v", err)
		}
	}
	if got := engine.State(userID); got != flight.InFlight {
		t.Fatalf("state before reset = %s, want %s", got, flight.InFlight)
	}

	hub := NewHub(nil, engine, nil)
	defer hub.Close()
	client := &client{
		roomID:     uuid.New(),
		userID:     userID,
		canPublish: true,
		send:       make(chan []byte, 1),
		done:       make(chan struct{}),
	}
	resetAt := time.Now().UTC()
	if err := hub.resetFlight(client, resetAt); err != nil {
		t.Fatalf("resetFlight() error = %v", err)
	}
	if got := engine.State(userID); got != flight.Idle {
		t.Fatalf("state after reset = %s, want %s", got, flight.Idle)
	}
	var ack flightResetAck
	if err := json.Unmarshal(<-client.send, &ack); err != nil {
		t.Fatalf("decode reset acknowledgment: %v", err)
	}
	if ack.Type != "flight_reset_ack" || !ack.ResetAt.Equal(resetAt) {
		t.Fatalf("reset acknowledgment = %+v", ack)
	}
}

func TestLiveTrackingDataDeletedBlocksOldRoomsUntilNewSession(t *testing.T) {
	engine := flight.NewLifecycleEngine(context.Background(), nil, nil)
	defer engine.Close()
	userID := uuid.New()
	hub := NewHub(nil, engine, nil)
	defer hub.Close()

	hub.LiveTrackingDataDeleted(userID, []uuid.UUID{uuid.New()})
	if _, ok := hub.disabledUsers[userID]; !ok {
		t.Fatal("tracking deletion did not block the disabled user")
	}
	if got := engine.State(userID); got != flight.Idle {
		t.Fatalf("lifecycle state after deletion = %s, want %s", got, flight.Idle)
	}
	oldClient := &client{hub: hub, userID: userID, roomID: uuid.New()}
	if hub.register(oldClient) {
		t.Fatal("disabled user rejoined a live room")
	}

	newRoomID := uuid.New()
	hub.LiveSessionStarted(userID, newRoomID)
	newClient := &client{hub: hub, userID: userID, roomID: newRoomID, done: make(chan struct{})}
	if !hub.register(newClient) {
		t.Fatal("new LiveTracking session remained disabled")
	}
	hub.unregister(newClient)
}

func TestDeleteEventRemovesOnlyDeletedOwnersLocation(t *testing.T) {
	hub := NewHub(nil, nil, nil)
	defer hub.Close()
	roomID := uuid.New()
	ownerID := uuid.New()
	otherID := uuid.New()
	hub.rooms[roomID] = &room{
		clients:   make(map[*client]struct{}),
		locations: map[uuid.UUID]Location{ownerID: {UserID: ownerID}, otherID: {UserID: otherID}},
	}
	hub.applyRoomAccessEvent(roomAccessEvent{
		RoomID: roomID.String(), OwnerID: ownerID.String(), Ended: true, DeleteData: true,
	})
	current := hub.rooms[roomID]
	if current == nil {
		t.Fatal("room without clients should remain for unrelated location data")
	}
	if _, exists := current.locations[ownerID]; exists {
		t.Fatal("deleted owner location remains in room")
	}
	if _, exists := current.locations[otherID]; !exists {
		t.Fatal("delete event removed another owner's location")
	}
}

func TestResetFlightRejectsReadOnlySubscriber(t *testing.T) {
	engine := flight.NewLifecycleEngine(context.Background(), nil, nil)
	defer engine.Close()
	userID := uuid.New()
	base := time.Now().UTC().Add(-10 * time.Second).Truncate(time.Second)
	for second := 0; second <= 3; second++ {
		if err := engine.Observe(userID, flight.TrackPoint{
			Latitude: 37.5, Longitude: -122.5, AltitudeMeters: 100 + float64(second),
			GroundSpeedMPS: 8, Timestamp: base.Add(time.Duration(second) * time.Second),
		}); err != nil {
			t.Fatalf("Observe() error = %v", err)
		}
	}

	hub := NewHub(nil, engine, nil)
	defer hub.Close()
	client := &client{userID: userID, send: make(chan []byte, 1), done: make(chan struct{})}
	if err := hub.resetFlight(client, time.Now().UTC()); err == nil {
		t.Fatal("resetFlight() succeeded for a read-only subscriber")
	}
	if got := engine.State(userID); got != flight.InFlight {
		t.Fatalf("read-only reset changed lifecycle state to %s", got)
	}
	if len(client.send) != 0 {
		t.Fatal("read-only reset unexpectedly received an acknowledgment")
	}
}
