package realtime

import (
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/flight"
	"github.com/parabeacon/server/internal/model"
)

func (h *Hub) PilotFlightState(userID uuid.UUID) (bool, time.Time) {
	if h.lifecycle == nil || userID == uuid.Nil {
		return false, time.Time{}
	}
	state, startedAt := h.lifecycle.FlightStartedAt(userID)
	return state == flight.InFlight || state == flight.Landing, startedAt
}

func (h *Hub) ObserveTrackingPoints(userID uuid.UUID, points []model.LiveTrackPoint) {
	if h.lifecycle == nil || userID == uuid.Nil || len(points) == 0 {
		return
	}
	ordered := append([]model.LiveTrackPoint(nil), points...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Timestamp.Before(ordered[j].Timestamp)
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.disabledUsers[userID] {
		return
	}
	for _, point := range ordered {
		if err := h.lifecycle.Observe(userID, flight.TrackPoint{
			Latitude: point.Latitude, Longitude: point.Longitude, AltitudeMeters: point.Altitude,
			GPSAltitudeMeters: point.GPSAltitude, PressureAltitude: point.BarometricAltitude,
			GroundSpeedMPS: point.GroundSpeed, HeadingDegrees: point.Heading, Timestamp: point.Timestamp,
		}); err != nil {
			h.logger.Printf("flight REST point update user=%s: %v", userID, err)
		}
	}
}
