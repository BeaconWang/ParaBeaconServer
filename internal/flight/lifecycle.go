package flight

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type State string

const (
	Idle     State = "IDLE"
	TakeOff  State = "TAKE_OFF"
	InFlight State = "IN_FLIGHT"
	Landing  State = "LANDING"

	maxFlightPoints = 300_000
)

var ErrInvalidPoint = errors.New("invalid flight telemetry point")

type TrackPoint struct {
	Latitude          float64
	Longitude         float64
	AltitudeMeters    float64
	GPSAltitudeMeters *float64
	PressureAltitude  *float64
	GroundSpeedMPS    float64
	HeadingDegrees    *float64
	Timestamp         time.Time
}

type Metrics struct {
	DistanceMeters   float64       `json:"distance_meters"`
	MaximumAltitudeM float64       `json:"maximum_altitude_m"`
	MaximumSpeedMPS  float64       `json:"maximum_speed_mps"`
	Duration         time.Duration `json:"duration"`
}

type CompletedFlight struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	StartedAt time.Time
	EndedAt   time.Time
	Points    []TrackPoint
	Metrics   Metrics
	IGC       []byte
}

type SettlementSink interface {
	Settle(context.Context, CompletedFlight) error
}

type LifecycleEngine struct {
	mu       sync.Mutex
	submitMu sync.RWMutex
	users    map[uuid.UUID]*userFlight
	queue    chan CompletedFlight
	parent   context.Context
	ctx      context.Context
	cancel   context.CancelFunc
	closed   bool
	workers  sync.WaitGroup
	sink     SettlementSink
	logger   *log.Logger
}

type userFlight struct {
	state         State
	flightID      uuid.UUID
	startedAt     time.Time
	lastPoint     TrackPoint
	points        []TrackPoint
	takeoffSince  time.Time
	takeoffPoints []TrackPoint
	landingSince  time.Time
	landingMinAlt float64
	landingMaxAlt float64
}

func NewLifecycleEngine(parent context.Context, sink SettlementSink, logger *log.Logger) *LifecycleEngine {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(context.Background())
	if logger == nil {
		logger = log.Default()
	}
	engine := &LifecycleEngine{
		users:  make(map[uuid.UUID]*userFlight),
		queue:  make(chan CompletedFlight, 128),
		parent: parent,
		ctx:    ctx,
		cancel: cancel,
		sink:   sink,
		logger: logger,
	}
	if sink != nil {
		engine.workers.Add(1)
		go engine.settlementWorker()
	}
	return engine
}

func (e *LifecycleEngine) State(userID uuid.UUID) State {
	e.mu.Lock()
	defer e.mu.Unlock()
	if current := e.users[userID]; current != nil {
		return current.state
	}
	return Idle
}

func (e *LifecycleEngine) Observe(userID uuid.UUID, point TrackPoint) error {
	if err := e.parent.Err(); err != nil {
		return err
	}
	e.submitMu.RLock()
	closed := e.closed
	e.submitMu.RUnlock()
	if closed {
		return context.Canceled
	}
	if userID == uuid.Nil || !validPoint(point) {
		return ErrInvalidPoint
	}
	var completed *CompletedFlight
	e.mu.Lock()
	current := e.users[userID]
	if current == nil {
		current = &userFlight{state: Idle}
		e.users[userID] = current
	}
	if !current.lastPoint.Timestamp.IsZero() && !point.Timestamp.After(current.lastPoint.Timestamp) {
		e.mu.Unlock()
		return nil
	}
	if point.Timestamp.After(time.Now().UTC().Add(5 * time.Minute)) {
		e.mu.Unlock()
		return ErrInvalidPoint
	}

	switch current.state {
	case Idle:
		if point.GroundSpeedMPS > 6 {
			current.state = TakeOff
			current.takeoffSince = point.Timestamp
			current.takeoffPoints = []TrackPoint{point}
		}
	case TakeOff:
		gap := point.Timestamp.Sub(current.lastPoint.Timestamp)
		if point.GroundSpeedMPS <= 6 || gap > 5*time.Second || point.AltitudeMeters <= current.lastPoint.AltitudeMeters {
			current.state = Idle
			current.takeoffSince = time.Time{}
			current.takeoffPoints = nil
			if point.GroundSpeedMPS > 6 {
				current.state = TakeOff
				current.takeoffSince = point.Timestamp
				current.takeoffPoints = []TrackPoint{point}
			}
		} else {
			current.takeoffPoints = appendBounded(current.takeoffPoints, point)
			if point.Timestamp.Sub(current.takeoffSince) >= 3*time.Second {
				current.state = InFlight
				current.flightID = uuid.New()
				current.startedAt = current.takeoffSince
				current.points = append([]TrackPoint(nil), current.takeoffPoints...)
				current.takeoffPoints = nil
				current.landingSince = time.Time{}
			}
		}
	case InFlight:
		current.points = appendBounded(current.points, point)
		if point.GroundSpeedMPS < 2 {
			current.state = Landing
			current.landingSince = point.Timestamp
			current.landingMinAlt = point.AltitudeMeters
			current.landingMaxAlt = point.AltitudeMeters
		}
	case Landing:
		current.points = appendBounded(current.points, point)
		gap := point.Timestamp.Sub(current.lastPoint.Timestamp)
		if point.GroundSpeedMPS >= 2 || gap > 5*time.Second {
			current.state = InFlight
			current.landingSince = time.Time{}
			break
		}
		current.landingMinAlt = math.Min(current.landingMinAlt, point.AltitudeMeters)
		current.landingMaxAlt = math.Max(current.landingMaxAlt, point.AltitudeMeters)
		if current.landingMaxAlt-current.landingMinAlt > 5 {
			current.state = InFlight
			current.landingSince = time.Time{}
			break
		}
		if point.Timestamp.Sub(current.landingSince) >= 30*time.Second {
			flight := CompletedFlight{
				ID:        current.flightID,
				UserID:    userID,
				StartedAt: current.startedAt,
				EndedAt:   point.Timestamp,
				Points:    append([]TrackPoint(nil), current.points...),
			}
			flight.Metrics = CalculateMetrics(flight.Points, flight.StartedAt, flight.EndedAt)
			igc, err := GenerateIGC(flight)
			if err == nil {
				flight.IGC = igc
				completed = &flight
			} else {
				e.logger.Printf("generate IGC flight=%s: %v", current.flightID, err)
			}
			*current = userFlight{state: Idle}
		}
	}
	current.lastPoint = point
	e.mu.Unlock()

	if completed != nil && e.sink != nil {
		e.submitMu.RLock()
		if e.closed {
			e.submitMu.RUnlock()
			return context.Canceled
		}
		select {
		case e.queue <- *completed:
			e.submitMu.RUnlock()
		case <-e.ctx.Done():
			e.submitMu.RUnlock()
			return e.ctx.Err()
		}
	}
	return nil
}

func (e *LifecycleEngine) settlementWorker() {
	defer e.workers.Done()
	for completed := range e.queue {
		for attempt := 0; ; attempt++ {
			if err := e.sink.Settle(e.ctx, completed); err == nil {
				break
			} else {
				delay := time.Second * time.Duration(1<<min(attempt, 5))
				e.logger.Printf("settle flight=%s attempt=%d: %v", completed.ID, attempt+1, err)
				timer := time.NewTimer(delay)
				select {
				case <-timer.C:
				case <-e.ctx.Done():
					timer.Stop()
					return
				}
			}
		}
	}
}

func (e *LifecycleEngine) Close() {
	e.submitMu.Lock()
	if !e.closed {
		e.closed = true
		close(e.queue)
	}
	e.submitMu.Unlock()
	if e.sink == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		e.workers.Wait()
		close(done)
	}()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
		e.cancel()
	case <-timer.C:
		e.cancel()
		<-done
	}
}

func appendBounded(points []TrackPoint, point TrackPoint) []TrackPoint {
	if len(points) >= maxFlightPoints {
		return points
	}
	return append(points, point)
}

func validPoint(point TrackPoint) bool {
	return point.Timestamp.IsZero() == false &&
		!math.IsNaN(point.Latitude) && !math.IsInf(point.Latitude, 0) && point.Latitude >= -90 && point.Latitude <= 90 &&
		!math.IsNaN(point.Longitude) && !math.IsInf(point.Longitude, 0) && point.Longitude >= -180 && point.Longitude <= 180 &&
		!math.IsNaN(point.AltitudeMeters) && !math.IsInf(point.AltitudeMeters, 0) && point.AltitudeMeters >= -2000 && point.AltitudeMeters <= 30000 &&
		!math.IsNaN(point.GroundSpeedMPS) && !math.IsInf(point.GroundSpeedMPS, 0) && point.GroundSpeedMPS >= 0 && point.GroundSpeedMPS <= 200
}

func CalculateMetrics(points []TrackPoint, startedAt, endedAt time.Time) Metrics {
	metrics := Metrics{Duration: endedAt.Sub(startedAt)}
	for index, point := range points {
		if index == 0 {
			metrics.MaximumAltitudeM = point.AltitudeMeters
		}
		metrics.MaximumAltitudeM = math.Max(metrics.MaximumAltitudeM, point.AltitudeMeters)
		metrics.MaximumSpeedMPS = math.Max(metrics.MaximumSpeedMPS, point.GroundSpeedMPS)
		if index > 0 {
			metrics.DistanceMeters += haversine(points[index-1], point)
		}
	}
	return metrics
}

func haversine(a, b TrackPoint) float64 {
	const earthRadius = 6371008.8
	lat1, lat2 := a.Latitude*math.Pi/180, b.Latitude*math.Pi/180
	dLat := (b.Latitude - a.Latitude) * math.Pi / 180
	dLon := (b.Longitude - a.Longitude) * math.Pi / 180
	x := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadius * math.Asin(math.Sqrt(math.Min(1, x)))
}

func GenerateIGC(flight CompletedFlight) ([]byte, error) {
	if flight.ID == uuid.Nil || flight.UserID == uuid.Nil || len(flight.Points) == 0 {
		return nil, ErrInvalidPoint
	}
	points := append([]TrackPoint(nil), flight.Points...)
	sort.SliceStable(points, func(i, j int) bool { return points[i].Timestamp.Before(points[j].Timestamp) })
	date := points[0].Timestamp.UTC()
	var output strings.Builder
	fmt.Fprintf(&output, "AParaBeacon\nHFDTE%02d%02d%02d\nHFFXA050\nHFGTYGLIDERTYPE:Paraglider\nHFGPSGPS\nHFDTM100GPS\n", date.Day(), int(date.Month()), date.Year()%100)
	for _, point := range points {
		lat, err := formatIGCCoordinate(point.Latitude, 2)
		if err != nil {
			return nil, err
		}
		lon, err := formatIGCCoordinate(point.Longitude, 3)
		if err != nil {
			return nil, err
		}
		pressureAlt := point.AltitudeMeters
		if point.PressureAltitude != nil {
			pressureAlt = *point.PressureAltitude
		}
		gpsAlt := point.AltitudeMeters
		if point.GPSAltitudeMeters != nil {
			gpsAlt = *point.GPSAltitudeMeters
		}
		pressureField, err := formatIGCAltitude(pressureAlt)
		if err != nil {
			return nil, err
		}
		gpsField, err := formatIGCAltitude(gpsAlt)
		if err != nil {
			return nil, err
		}
		stamp := point.Timestamp.UTC()
		fmt.Fprintf(&output, "B%02d%02d%02d%s%sA%s%s\n", stamp.Hour(), stamp.Minute(), stamp.Second(), lat, lon, pressureField, gpsField)
	}
	return []byte(output.String()), nil
}

func formatIGCCoordinate(coordinate float64, degreeDigits int) (string, error) {
	limit := 90.0
	if degreeDigits == 3 {
		limit = 180.0
	}
	if math.IsNaN(coordinate) || math.IsInf(coordinate, 0) || math.Abs(coordinate) > limit {
		return "", ErrInvalidPoint
	}
	absolute := math.Abs(coordinate)
	degrees := int(math.Floor(absolute))
	minutes := int(math.Round((absolute - float64(degrees)) * 60 * 1000))
	if minutes >= 60000 {
		degrees++
		minutes = 0
	}
	if float64(degrees) > limit {
		return "", ErrInvalidPoint
	}
	hemisphere := byte('N')
	if degreeDigits == 3 {
		hemisphere = 'E'
		if coordinate < 0 {
			hemisphere = 'W'
		}
	} else if coordinate < 0 {
		hemisphere = 'S'
	}
	return fmt.Sprintf("%0*d%05d%c", degreeDigits, degrees, minutes, hemisphere), nil
}

func formatIGCAltitude(altitude float64) (string, error) {
	if math.IsNaN(altitude) || math.IsInf(altitude, 0) || altitude < -9999 || altitude > 99999 {
		return "", ErrInvalidPoint
	}
	return fmt.Sprintf("%05d", int(math.Round(altitude))), nil
}
