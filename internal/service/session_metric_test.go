package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
)

type sessionMetricServiceTestRepository struct {
	event *model.SessionMetricEvent
	err   error
}

func (f *sessionMetricServiceTestRepository) Ingest(_ context.Context, event *model.SessionMetricEvent) error {
	copy := *event
	f.event = &copy
	return f.err
}

func TestSessionMetricsServiceAcceptsValidEvent(t *testing.T) {
	repository := &sessionMetricServiceTestRepository{}
	service := NewSessionMetricsService(repository)
	event := validSessionMetricEvent()
	if err := service.Ingest(context.Background(), event); err != nil {
		t.Fatalf("ingest valid metric: %v", err)
	}
	if repository.event == nil || repository.event.ID != event.ID {
		t.Fatal("expected valid metric to be persisted")
	}
	if repository.event.ClientIP != "2001:db8::1" {
		t.Fatalf("expected normalized client IP, got %q", repository.event.ClientIP)
	}
}

func TestSessionMetricsServiceRejectsInvalidFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*model.SessionMetricEvent)
	}{
		{name: "negative runtime", mutate: func(event *model.SessionMetricEvent) { event.RunTimeSec = -1 }},
		{name: "excessive runtime", mutate: func(event *model.SessionMetricEvent) { event.RunTimeSec = MaxSessionMetricRuntimeSec + 1 }},
		{name: "invalid IP", mutate: func(event *model.SessionMetricEvent) { event.ClientIP = "not-an-ip" }},
		{name: "non-object diagnostics", mutate: func(event *model.SessionMetricEvent) { event.Diagnostics = `[]` }},
		{name: "oversized diagnostics", mutate: func(event *model.SessionMetricEvent) {
			event.Diagnostics = `{"value":"` + string(make([]byte, MaxSessionMetricDiagnosticsBytes)) + `"}`
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &sessionMetricServiceTestRepository{}
			service := NewSessionMetricsService(repository)
			event := validSessionMetricEvent()
			test.mutate(event)
			if err := service.Ingest(context.Background(), event); !errors.Is(err, ErrInvalidSessionMetric) {
				t.Fatalf("expected invalid metric error, got %v", err)
			}
			if repository.event != nil {
				t.Fatal("invalid metric must not reach repository")
			}
		})
	}
}

func validSessionMetricEvent() *model.SessionMetricEvent {
	return &model.SessionMetricEvent{
		ID:               uuid.New(),
		EventID:          uuid.New(),
		SessionID:        uuid.New(),
		HardwareID:       uuid.New(),
		AppVersion:       "1.2.3+45",
		ClientIP:         "2001:0db8:0:0:0:0:0:1",
		RunTimeSec:       60,
		ClientTimestamp:  time.Now().UTC(),
		ServerReceivedAt: time.Now().UTC(),
		Diagnostics:      `{"os":"test"}`,
	}
}
