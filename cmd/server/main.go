package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/parabeacon/server/internal/cache"
	"github.com/parabeacon/server/internal/config"
	"github.com/parabeacon/server/internal/database"
	"github.com/parabeacon/server/internal/repository"
	"github.com/parabeacon/server/internal/router"
	"github.com/parabeacon/server/internal/service"
	"github.com/parabeacon/server/pkg/utils"
)

func main() {
	if err := run(); err != nil {
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}

func run() error {
	logger := log.New(os.Stderr, "", log.LstdFlags|log.LUTC)
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(ctx, cfg.DatabaseDSN)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get database connection pool: %w", err)
	}
	defer sqlDB.Close()

	redisClient, err := cache.Open(ctx, cfg.RedisURL)
	if err != nil {
		return err
	}
	if redisClient != nil {
		defer redisClient.Close()
	}

	tokens := utils.NewTokenManager(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTExpiry)
	users := repository.NewUserRepository(db)
	authService := service.NewAuthService(users, tokens)
	engine, err := router.New(cfg, db, authService, tokens)
	if err != nil {
		return fmt.Errorf("configure HTTP routes: %w", err)
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           engine,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serveErrors := make(chan error, 1)
	go func() {
		logger.Printf("HTTP server listening on %s", cfg.HTTPAddress)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrors <- err
		}
	}()

	select {
	case err := <-serveErrors:
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}
	logger.Println("HTTP server stopped")
	return nil
}
