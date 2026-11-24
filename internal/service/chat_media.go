package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/repository"
)

const chatMediaUploadTTL = time.Hour

var ErrChatMediaUploadExpired = errors.New("chat media upload expired")

type ChatMediaService struct {
	repository repository.ChatMediaRepository
	chat       *ChatService
	storage    *ChatMediaStorage
}

func NewChatMediaService(repository repository.ChatMediaRepository, chat *ChatService, storage *ChatMediaStorage) *ChatMediaService {
	return &ChatMediaService{repository: repository, chat: chat, storage: storage}
}

func (s *ChatMediaService) Upload(ctx context.Context, senderID uuid.UUID, scope string, channelNumber, pilotNumber int, kind string, source io.Reader) (uuid.UUID, error) {
	if s == nil || s.repository == nil || s.chat == nil || s.storage == nil || senderID == uuid.Nil ||
		(kind != ChatKindImage && kind != ChatKindVoice) {
		return uuid.Nil, ErrInvalidChatMedia
	}
	recipientID, err := s.chat.ValidateMediaTarget(ctx, senderID, scope, channelNumber, pilotNumber)
	if err != nil {
		return uuid.Nil, err
	}
	now := time.Now().UTC()
	if err := s.CleanupExpired(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("clean expired chat uploads: %w", err)
	}
	id := uuid.New()
	contentType, err := s.storage.Store(ctx, id, kind, source)
	if err != nil {
		return uuid.Nil, err
	}
	media := &model.ChatMedia{
		ID:            id,
		SenderID:      senderID,
		Scope:         scope,
		ChannelNumber: optionalNumber(channelNumber),
		RecipientID:   recipientID,
		Kind:          kind,
		ContentType:   contentType,
		StorageKey:    id.String(),
		ExpiresAt:     now.Add(chatMediaUploadTTL),
		CreatedAt:     now,
	}
	if err := s.repository.CreateChatMedia(ctx, media); err != nil {
		_ = s.storage.Delete(id, kind)
		return uuid.Nil, fmt.Errorf("record staged chat media: %w", err)
	}
	return id, nil
}

func (s *ChatMediaService) CleanupExpired(ctx context.Context) error {
	if s == nil || s.repository == nil || s.storage == nil {
		return ErrInvalidChatMedia
	}
	expired, err := s.repository.DeleteExpiredChatMedia(ctx, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("delete expired staged chat media: %w", err)
	}
	for _, media := range expired {
		if err := s.storage.Delete(media.ID, media.Kind); err != nil {
			return fmt.Errorf("remove expired staged chat media %s: %w", media.ID, err)
		}
	}
	return nil
}

func (s *ChatMediaService) RunCleanup(ctx context.Context, logger *log.Logger) {
	cleanup := func() {
		if err := s.CleanupExpired(ctx); err != nil && ctx.Err() == nil && logger != nil {
			logger.Printf("clean expired chat media: %v", err)
		}
	}
	cleanup()
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanup()
		}
	}
}

func optionalNumber(number int) *int {
	if number == 0 {
		return nil
	}
	return &number
}

func (s *ChatMediaService) Open(ctx context.Context, requesterID, mediaID uuid.UUID) (*os.File, os.FileInfo, string, error) {
	if s == nil || s.repository == nil || s.storage == nil || requesterID == uuid.Nil || mediaID == uuid.Nil {
		return nil, nil, "", ErrChatMediaNotFound
	}
	media, err := s.repository.FindChatMediaForUser(ctx, mediaID, requesterID)
	if err != nil {
		return nil, nil, "", err
	}
	file, info, err := s.storage.Open(mediaID, media.Kind)
	if err != nil {
		return nil, nil, "", err
	}
	return file, info, media.ContentType, nil
}
