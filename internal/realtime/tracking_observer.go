package realtime

import (
	"sort"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/flight"
	"github.com/parabeacon/server/internal/model"
)

func (h *Hub) ObserveTrackingPoints(userID uuid.UUID, points []model.LiveTrackPoint) {
	if h.lifecycle == nil || userID == uuid.Nil || len(points) == 0 {
		return
	}
	ordered := append([]model.LiveTrackPoint(nil), points...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Timestamp.Before(ordered[j].Timestamp)
	})
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
