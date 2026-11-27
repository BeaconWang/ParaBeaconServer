package flight

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type testSettlementSink chan CompletedFlight

func (sink testSettlementSink) Settle(ctx context.Context, completed CompletedFlight) error {
	select {
	case sink <- completed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestLifecycleDetectsTakeoffLandingAndGeneratesIGC(t *testing.T) {
	sink := make(testSettlementSink, 1)
	engine := NewLifecycleEngine(context.Background(), sink, nil)
	defer engine.Close()
	userID := uuid.New()
	base := time.Now().UTC().Truncate(time.Second)

	observe := func(at time.Time, altitude, speed float64) {
		t.Helper()
		err := engine.Observe(userID, TrackPoint{
			Latitude: 37.5, Longitude: -122.5 + at.Sub(base).Seconds()*0.00001, AltitudeMeters: altitude,
			GroundSpeedMPS: speed, Timestamp: at,
		})
		if err != nil {
			t.Fatalf("Observe() error = %v", err)
		}
	}

	for second := 0; second <= 3; second++ {
		observe(base.Add(time.Duration(second)*time.Second), 100+float64(second), 8)
	}
	if got := engine.State(userID); got != InFlight {
		t.Fatalf("state after sustained takeoff = %s, want %s", got, InFlight)
	}
	for second := 4; second <= 34; second++ {
		observe(base.Add(time.Duration(second)*time.Second), 104, 1)
	}
	if got := engine.State(userID); got != Idle {
		t.Fatalf("state after landing confirmation = %s, want %s", got, Idle)
	}

	select {
	case completed := <-sink:
		if completed.ID == uuid.Nil || completed.UserID != userID {
			t.Fatalf("completed flight identity is invalid: %+v", completed)
		}
		if completed.Metrics.MaximumSpeedMPS != 8 || completed.Metrics.MaximumAltitudeM != 104 {
			t.Fatalf("unexpected metrics: %+v", completed.Metrics)
		}
		if completed.Metrics.Duration != 34*time.Second || completed.Metrics.DistanceMeters <= 0 {
			t.Fatalf("unexpected duration or distance: %+v", completed.Metrics)
		}
		igc := string(completed.IGC)
		if !strings.Contains(igc, "HFDTE") || !strings.Contains(igc, "B") || !strings.Contains(igc, "3730000N12230000WA") {
			t.Fatalf("IGC header or coordinate record is missing: %q", igc)
		}
	case <-time.After(time.Second):
		t.Fatal("flight settlement was not queued")
	}
}

func TestLifecycleResetClearsFlightAndRejectsPreResetTelemetry(t *testing.T) {
	engine := NewLifecycleEngine(context.Background(), nil, nil)
	defer engine.Close()
	userID := uuid.New()
	base := time.Now().UTC().Truncate(time.Second)
	for second := 0; second <= 3; second++ {
		if err := engine.Observe(userID, TrackPoint{
			Latitude: 37.5, Longitude: -122.5, AltitudeMeters: 100 + float64(second),
			GroundSpeedMPS: 8, Timestamp: base.Add(time.Duration(second) * time.Second),
		}); err != nil {
			t.Fatalf("Observe() error = %v", err)
		}
	}
	if got := engine.State(userID); got != InFlight {
		t.Fatalf("state before reset = %s, want %s", got, InFlight)
	}

	resetAt := base.Add(5 * time.Second)
	if err := engine.Reset(userID, resetAt); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	if got := engine.State(userID); got != Idle {
		t.Fatalf("state after reset = %s, want %s", got, Idle)
	}
	if err := engine.Observe(userID, TrackPoint{
		Latitude: 37.5, Longitude: -122.5, AltitudeMeters: 105,
		GroundSpeedMPS: 8, Timestamp: base.Add(4 * time.Second),
	}); err != nil {
		t.Fatalf("stale Observe() error = %v", err)
	}
	if got := engine.State(userID); got != Idle {
		t.Fatalf("state after stale telemetry = %s, want %s", got, Idle)
	}
	if err := engine.Reset(userID, resetAt); err != ErrStaleReset {
		t.Fatalf("replayed Reset() error = %v, want %v", err, ErrStaleReset)
	}
}

func TestGenerateIGCEncodesBothLongitudeHemisphereSigns(t *testing.T) {
	userID := uuid.New()
	base := time.Date(2026, 6, 4, 12, 34, 56, 0, time.UTC)
	flight := CompletedFlight{
		ID: uuid.New(), UserID: userID,
		Points: []TrackPoint{
			{Latitude: -1.25, Longitude: 2.5, AltitudeMeters: 50, GroundSpeedMPS: 10, Timestamp: base},
		},
	}
	igc, err := GenerateIGC(flight)
	if err != nil {
		t.Fatalf("GenerateIGC() error = %v", err)
	}
	if !strings.Contains(string(igc), "B1234560115000S00230000EA0005000050") {
		t.Fatalf("IGC coordinate/altitude fields are malformed: %q", igc)
	}
}
