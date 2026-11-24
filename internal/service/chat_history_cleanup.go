package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
)

const (
	ChatHistoryRetention = 30 * 24 * time.Hour
	chatCleanupBatchSize = 500
)

type ChatHistoryCleanupRepository interface {
	FindChatHistoryCleanupBatch(context.Context, time.Time, int) ([]model.ChatMessage, []model.ChatMedia, error)
	DeleteChatHistoryCleanupBatch(context.Context, time.Time, []uuid.UUID, []uuid.UUID) error
}

type ChatHistoryCleanupService struct {
	repository ChatHistoryCleanupRepository
	storage    *ChatMediaStorage
}

func NewChatHistoryCleanupService(repository ChatHistoryCleanupRepository, storage *ChatMediaStorage) *ChatHistoryCleanupService {
	return &ChatHistoryCleanupService{repository: repository, storage: storage}
}

func (s *ChatHistoryCleanupService) Cleanup(ctx context.Context, now time.Time) error {
	if s == nil || s.repository == nil || s.storage == nil {
		return fmt.Errorf("chat history cleanup is not configured")
	}
	if now.IsZero() {
		now = time.Now()
	}
	before := now.UTC().Add(-ChatHistoryRetention)
	for {
		messages, media, err := s.repository.FindChatHistoryCleanupBatch(ctx, before, chatCleanupBatchSize)
		if err != nil {
			return err
		}
		if len(messages) == 0 && len(media) == 0 {
			return nil
		}
		for _, item := range media {
			if err := s.storage.Delete(item.ID, item.Kind); err != nil {
				return fmt.Errorf("delete expired chat media file %s: %w", item.ID, err)
			}
		}
		messageIDs := make([]uuid.UUID, len(messages))
		for i := range messages {
			messageIDs[i] = messages[i].ID
		}
		mediaIDs := make([]uuid.UUID, len(media))
		for i := range media {
			mediaIDs[i] = media[i].ID
		}
		if err := s.repository.DeleteChatHistoryCleanupBatch(ctx, before, messageIDs, mediaIDs); err != nil {
			return err
		}
	}
}

func (s *ChatHistoryCleanupService) RunDailyCleanup(ctx context.Context, logger *log.Logger) {
	cleanup := func() {
		if err := s.Cleanup(ctx, time.Now().UTC()); err != nil && ctx.Err() == nil && logger != nil {
			logger.Printf("clean expired chat history: %v", err)
		}
	}
	cleanup()
	for {
		now := time.Now().UTC()
		timer := time.NewTimer(time.Until(nextUTCMidnight(now)))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
			cleanup()
		}
	}
}

func nextUTCMidnight(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
}
