package realtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/flight"
)

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
