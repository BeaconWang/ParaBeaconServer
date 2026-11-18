package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/flight"
	"github.com/parabeacon/server/internal/model"
)

type IGCObjectStore interface {
	PutIGC(context.Context, string, []byte) error
}

type AutomaticFlightSettlement struct {
	logs FlightLogRepository
	igc  IGCObjectStore
}

func NewAutomaticFlightSettlement(logs FlightLogRepository, igc IGCObjectStore) *AutomaticFlightSettlement {
	return &AutomaticFlightSettlement{logs: logs, igc: igc}
}

func (s *AutomaticFlightSettlement) Settle(ctx context.Context, completed flight.CompletedFlight) error {
	if s == nil || s.logs == nil || completed.ID == uuid.Nil || completed.UserID == uuid.Nil || completed.StartedAt.IsZero() || completed.EndedAt.Before(completed.StartedAt) || len(completed.Points) == 0 || len(completed.IGC) == 0 {
		return errors.New("invalid automatic flight settlement")
	}
	objectKey := fmt.Sprintf("automatic-flights/%s/%s.igc", completed.UserID, completed.ID)
	if s.igc != nil {
		if err := s.igc.PutIGC(ctx, objectKey, completed.IGC); err != nil {
			return fmt.Errorf("archive IGC object: %w", err)
		}
	}
	record := struct {
		ID              string                 `json:"id"`
		StartTime       time.Time              `json:"startTime"`
		EndTime         time.Time              `json:"endTime"`
		PointCount      int                    `json:"pointCount"`
		DistanceMeters  float64                `json:"distanceMeters"`
		MaximumAltitude float64                `json:"maximumAltitude"`
		MaximumSpeed    float64                `json:"maximumSpeed"`
		DurationSeconds float64                `json:"durationSeconds"`
		IGCObjectKey    string                 `json:"igcObjectKey,omitempty"`
		IGC             string                 `json:"igc,omitempty"`
		Samples         []automaticFlightPoint `json:"samples"`
	}{
		ID: completed.ID.String(), StartTime: completed.StartedAt.UTC(), EndTime: completed.EndedAt.UTC(),
		PointCount: len(completed.Points), DistanceMeters: completed.Metrics.DistanceMeters,
		MaximumAltitude: completed.Metrics.MaximumAltitudeM, MaximumSpeed: completed.Metrics.MaximumSpeedMPS,
		DurationSeconds: completed.Metrics.Duration.Seconds(), Samples: make([]automaticFlightPoint, 0, len(completed.Points)),
	}
	if s.igc != nil {
		record.IGCObjectKey = objectKey
	} else {
		record.IGC = string(completed.IGC)
	}
	for _, point := range completed.Points {
		record.Samples = append(record.Samples, automaticFlightPoint{
			Latitude: point.Latitude, Longitude: point.Longitude, Altitude: point.AltitudeMeters,
			Speed: point.GroundSpeedMPS, Timestamp: point.Timestamp.UTC(),
			GPSAltitude: point.GPSAltitudeMeters, PressureAltitude: point.PressureAltitude,
		})
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode automatic flight: %w", err)
	}
	if len(payload) > MaxFlightLogBytes {
		return fmt.Errorf("automatic flight exceeds maximum stored size")
	}
	logEntry := model.FlightLog{
		ID: completed.ID, UserID: completed.UserID, StartedAt: completed.StartedAt.UTC(),
		PointCount: len(completed.Points), Data: string(payload),
	}
	_, _, err = s.logs.Import(ctx, completed.UserID, []model.FlightLog{logEntry})
	if err != nil {
		return fmt.Errorf("persist automatic flight metadata: %w", err)
	}
	return nil
}

type automaticFlightPoint struct {
	Latitude         float64   `json:"lat"`
	Longitude        float64   `json:"lon"`
	Altitude         float64   `json:"altitude"`
	Speed            float64   `json:"speed"`
	Timestamp        time.Time `json:"timestamp"`
	GPSAltitude      *float64  `json:"gps_altitude,omitempty"`
	PressureAltitude *float64  `json:"pressure_altitude,omitempty"`
}
