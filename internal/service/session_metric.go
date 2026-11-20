package service

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
)

var ErrInvalidSessionMetric = errors.New("invalid session metric")

const (
	MaxSessionMetricRuntimeSec       = int64(10 * 365 * 24 * 60 * 60)
	MaxSessionMetricDiagnosticsBytes = 16 * 1024
)

type SessionMetricRepository interface {
	Ingest(ctx context.Context, event *model.SessionMetricEvent) error
}

type SessionMetricsService struct {
	repository SessionMetricRepository
}

func NewSessionMetricsService(repository SessionMetricRepository) *SessionMetricsService {
	return &SessionMetricsService{repository: repository}
}

func (s *SessionMetricsService) Ingest(ctx context.Context, event *model.SessionMetricEvent) error {
	if event == nil ||
		event.ID == uuid.Nil ||
		event.EventID == uuid.Nil ||
		event.SessionID == uuid.Nil ||
		event.HardwareID == uuid.Nil ||
		event.AppVersion == "" ||
		!utf8.ValidString(event.AppVersion) ||
		len([]rune(event.AppVersion)) > 128 ||
		event.RunTimeSec < 0 || event.RunTimeSec > MaxSessionMetricRuntimeSec ||
		event.ServerReceivedAt.IsZero() || event.ClientTimestamp.IsZero() {
		return ErrInvalidSessionMetric
	}
	if ip := net.ParseIP(event.ClientIP); ip == nil {
		return ErrInvalidSessionMetric
	} else {
		event.ClientIP = ip.String()
	}
	if event.TotalRuntimeSec != nil &&
		(*event.TotalRuntimeSec < 0 || *event.TotalRuntimeSec > MaxSessionMetricRuntimeSec) {
		return ErrInvalidSessionMetric
	}
	if event.UserID != nil && *event.UserID == uuid.Nil {
		return ErrInvalidSessionMetric
	}
	if event.Diagnostics == "" || len(event.Diagnostics) > MaxSessionMetricDiagnosticsBytes {
		return ErrInvalidSessionMetric
	}
	var diagnostics map[string]json.RawMessage
	if err := json.Unmarshal([]byte(event.Diagnostics), &diagnostics); err != nil || diagnostics == nil {
		return ErrInvalidSessionMetric
	}
	event.AppVersion = strings.TrimSpace(event.AppVersion)
	if event.AppVersion == "" {
		return ErrInvalidSessionMetric
	}
	event.ClientTimestamp = event.ClientTimestamp.UTC()
	event.ServerReceivedAt = event.ServerReceivedAt.UTC()
	return s.repository.Ingest(ctx, event)
}
