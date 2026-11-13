package service

import (
	"math"
	"testing"
	"time"

	"github.com/parabeacon/server/internal/model"
)

func TestValidateTrackingPoint(t *testing.T) {
	now := time.Now().UTC()
	valid := model.LiveTrackPoint{
		Latitude:    47.1,
		Longitude:   8.5,
		Altitude:    1200,
		GroundSpeed: 12.5,
		Timestamp:   now,
	}
	if err := ValidateTrackingPoint(valid); err != nil {
		t.Fatalf("valid point rejected: %v", err)
	}

	tests := map[string]model.LiveTrackPoint{
		"latitude out of range": func() model.LiveTrackPoint {
			point := valid
			point.Latitude = 91
			return point
		}(),
		"non-finite speed": func() model.LiveTrackPoint {
			point := valid
			point.GroundSpeed = math.Inf(1)
			return point
		}(),
		"invalid bearing": func() model.LiveTrackPoint {
			point := valid
			bearing := 360.0
			point.Bearing = &bearing
			return point
		}(),
		"missing timestamp": func() model.LiveTrackPoint {
			point := valid
			point.Timestamp = time.Time{}
			return point
		}(),
	}
	for name, point := range tests {
		t.Run(name, func(t *testing.T) {
			if err := ValidateTrackingPoint(point); err == nil {
				t.Fatal("expected invalid point to be rejected")
			}
		})
	}
}
