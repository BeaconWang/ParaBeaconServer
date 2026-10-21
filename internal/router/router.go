package router

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/parabeacon/server/internal/config"
	"github.com/parabeacon/server/internal/handler"
	"github.com/parabeacon/server/internal/middleware"
	"github.com/parabeacon/server/internal/service"
	"github.com/parabeacon/server/pkg/utils"
	"gorm.io/gorm"
)

func New(cfg config.Config, db *gorm.DB, authService *service.AuthService, tokens *utils.TokenManager) (*gin.Engine, error) {
	if cfg.Environment != "development" && cfg.Environment != "test" {
		gin.SetMode(gin.ReleaseMode)
	}
	engine := gin.New()
	if err := engine.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		return nil, err
	}
	engine.Use(gin.Logger(), gin.Recovery(), middleware.HTTPHardening(cfg.CORSAllowedOrigin))

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	engine.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	engine.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := sqlDB.PingContext(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	authHandler := handler.NewAuthHandler(authService)
	userHandler := handler.NewUserHandler(authService)
	v1 := engine.Group("/api/v1")
	{
		auth := v1.Group("/auth")
		auth.POST("/register", middleware.NewFixedWindowLimiter(5, time.Minute, 10000).Middleware(), authHandler.Register)
		auth.POST("/login", middleware.NewFixedWindowLimiter(10, time.Minute, 10000).Middleware(), authHandler.Login)

		protected := v1.Group("")
		protected.Use(middleware.RequireAuth(tokens))
		protected.GET("/user/profile", userHandler.Profile)
		protected.GET("/tracking", handler.NotImplemented("live_tracking"))
		protected.GET("/flight-logs", handler.NotImplemented("flight_logs_igc"))
		protected.GET("/chat", handler.NotImplemented("chat_im"))
	}
	return engine, nil
}
