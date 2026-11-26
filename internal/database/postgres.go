package database

import (
	"context"
	"fmt"
	"time"

	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/repository"
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
	if err := db.WithContext(ctx).AutoMigrate(&model.User{}, &model.RefreshToken{}, &model.LiveTrackSession{}, &model.LiveTrackPoint{}, &model.FlightLog{}, &model.FlightLogSync{}, &model.FlightLogSyncBatch{}, &model.FlightLogSyncRecord{}, &model.LandingPoint{}, &model.LandingPointSettings{}, &model.CompetitionTask{}, &model.AppRelease{}, &model.ChatMessage{}, &model.ChatMedia{}, &model.ChatConversationPin{}, &model.ChatConversationRead{}, &model.SessionMetricEvent{}, &model.SessionMetricRuntime{}, &model.SessionMetricDeviceRuntime{}, &model.Permission{}, &model.AccountGroup{}, &model.AccountGroupMembership{}, &model.AccountGroupPermission{}); err != nil {
		return nil, fmt.Errorf("migrate database schema: %w", err)
	}
	if err := migrateCurrentCompetitionTaskSelection(db.WithContext(ctx)); err != nil {
		return nil, fmt.Errorf("migrate current competition task selections: %w", err)
	}
	if err := migratePilotNumbers(db.WithContext(ctx)); err != nil {
		return nil, fmt.Errorf("migrate pilot numbers: %w", err)
	}
	if err := repository.SeedAccountGroups(ctx, db); err != nil {
		return nil, fmt.Errorf("seed account groups: %w", err)
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

func migrateCurrentCompetitionTaskSelection(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(748392017)").Error; err != nil {
			return err
		}
		if err := tx.Exec("CREATE TABLE IF NOT EXISTS parabeacon_schema_migrations (name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP)").Error; err != nil {
			return err
		}
		var alreadyApplied bool
		if err := tx.Raw("SELECT EXISTS (SELECT 1 FROM parabeacon_schema_migrations WHERE name = ?)", "current_competition_task_selection_v1").Scan(&alreadyApplied).Error; err != nil {
			return err
		}
		if alreadyApplied {
			return nil
		}
		if err := tx.Exec(`
			UPDATE users AS u
			SET current_competition_task_owner_id = tasks.user_id
			FROM competition_tasks AS tasks
			WHERE tasks.user_id = u.id AND u.current_competition_task_owner_id IS NULL
		`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`
			UPDATE live_track_sessions AS sessions
			SET current_competition_task_owner_id = users.current_competition_task_owner_id
			FROM users
			WHERE sessions.user_id = users.id
			  AND sessions.ended_at IS NULL
			  AND sessions.current_competition_task_owner_id IS NULL
			  AND users.current_competition_task_owner_id IS NOT NULL
		`).Error; err != nil {
			return err
		}
		return tx.Exec("INSERT INTO parabeacon_schema_migrations (name) VALUES (?)", "current_competition_task_selection_v1").Error
	})
}

func migratePilotNumbers(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(748392015)").Error; err != nil {
			return err
		}
		if err := tx.Exec("LOCK TABLE users IN ACCESS EXCLUSIVE MODE").Error; err != nil {
			return err
		}
		if err := tx.Exec("ALTER TABLE users ADD COLUMN IF NOT EXISTS pilot_number integer").Error; err != nil {
			return err
		}
		if err := tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_users_pilot_number ON users (pilot_number)").Error; err != nil {
			return err
		}
		var sequenceExists bool
		if err := tx.Raw("SELECT to_regclass('pilot_number_seq') IS NOT NULL").Scan(&sequenceExists).Error; err != nil {
			return err
		}
		if err := tx.Exec("CREATE SEQUENCE IF NOT EXISTS pilot_number_seq MINVALUE 100000 MAXVALUE 999999 START WITH 100000 NO CYCLE").Error; err != nil {
			return err
		}

		var totalUsers, usersWithoutNumber int64
		if err := tx.Raw("SELECT COUNT(*) FROM users").Scan(&totalUsers).Error; err != nil {
			return err
		}
		if totalUsers > 900000 {
			return fmt.Errorf("%d accounts exceed the six-digit pilot-number capacity", totalUsers)
		}
		if err := tx.Raw("SELECT COUNT(*) FROM users WHERE pilot_number IS NULL").Scan(&usersWithoutNumber).Error; err != nil {
			return err
		}
		var maximumNumber int
		if err := tx.Raw("SELECT COALESCE(MAX(pilot_number), 99999) FROM users").Scan(&maximumNumber).Error; err != nil {
			return err
		}
		if int64(maximumNumber)+usersWithoutNumber > 999999 {
			return fmt.Errorf("pilot-number sequence has insufficient six-digit numbers")
		}
		if usersWithoutNumber > 0 {
			if err := tx.Exec(`
				WITH numbered_users AS (
					SELECT id, (?::bigint + ROW_NUMBER() OVER (ORDER BY created_at, id))::integer AS pilot_number
					FROM users
					WHERE pilot_number IS NULL
				)
				UPDATE users AS target
				SET pilot_number = numbered_users.pilot_number
				FROM numbered_users
				WHERE target.id = numbered_users.id
			`, maximumNumber).Error; err != nil {
				return err
			}
		}
		if err := tx.Exec("ALTER TABLE users ALTER COLUMN pilot_number SET DEFAULT nextval('pilot_number_seq')").Error; err != nil {
			return err
		}
		if err := tx.Exec("ALTER TABLE users ALTER COLUMN pilot_number SET NOT NULL").Error; err != nil {
			return err
		}
		if err := tx.Exec(`
			DO $$ BEGIN
				IF NOT EXISTS (
					SELECT 1 FROM pg_constraint
					WHERE conname = 'users_pilot_number_six_digits'
				) THEN
					ALTER TABLE users ADD CONSTRAINT users_pilot_number_six_digits
					CHECK (pilot_number BETWEEN 100000 AND 999999);
				END IF;
			END $$;
		`).Error; err != nil {
			return err
		}
		if !sequenceExists {
			var currentMaximum int
			if err := tx.Raw("SELECT COALESCE(MAX(pilot_number), 100000) FROM users").Scan(&currentMaximum).Error; err != nil {
				return err
			}
			sequenceHasValue := totalUsers > 0
			if err := tx.Exec("SELECT setval('pilot_number_seq', ?, ?)", currentMaximum, sequenceHasValue).Error; err != nil {
				return err
			}
		}
		return tx.Exec("ALTER SEQUENCE pilot_number_seq OWNED BY users.pilot_number").Error
	})
}
