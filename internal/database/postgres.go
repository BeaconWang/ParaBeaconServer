package database

import (
	"context"
	"fmt"
	"time"

	"github.com/parabeacon/server/internal/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func Open(ctx context.Context, dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get PostgreSQL connection pool: %w", err)
	}
	sqlDB.SetMaxOpenConns(50)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	if err := db.WithContext(ctx).AutoMigrate(&model.User{}, &model.RefreshToken{}, &model.LiveTrackSession{}, &model.LiveTrackPoint{}, &model.FlightLog{}, &model.LandingPoint{}, &model.LandingPointSettings{}, &model.AppRelease{}); err != nil {
		return nil, fmt.Errorf("migrate database schema: %w", err)
	}
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("ALTER TABLE refresh_tokens ALTER COLUMN expires_at DROP NOT NULL").Error; err != nil {
			return err
		}
		if err := tx.Exec(`
			WITH ranked_sessions AS (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY user_id ORDER BY created_at DESC, id DESC) AS session_rank
				FROM refresh_tokens
				WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
			)
			UPDATE refresh_tokens AS tokens
			SET revoked_at = CURRENT_TIMESTAMP
			FROM ranked_sessions
			WHERE tokens.id = ranked_sessions.id AND ranked_sessions.session_rank > 1
		`).Error; err != nil {
			return err
		}
		return tx.Exec("UPDATE refresh_tokens SET expires_at = NULL WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)").Error
	}); err != nil {
		return nil, fmt.Errorf("migrate persistent account sessions: %w", err)
	}
	return db, nil
}
