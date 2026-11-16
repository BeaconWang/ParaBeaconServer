package router

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/parabeacon/server/internal/config"
	"github.com/parabeacon/server/internal/handler"
	"github.com/parabeacon/server/internal/middleware"
	"github.com/parabeacon/server/internal/repository"
	"github.com/parabeacon/server/internal/service"
	webapp "github.com/parabeacon/server/internal/web"
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
	homePage := gin.WrapH(webapp.HomeHandler())
	releasePage := gin.WrapH(webapp.Handler())
	livePage := gin.WrapH(webapp.LiveHandler())
	i18nScript := gin.WrapH(webapp.I18nScriptHandler())
	authScript := gin.WrapH(webapp.AuthScriptHandler())
	engine.GET("/", homePage)
	engine.GET("/home", homePage)
	engine.GET("/home/", homePage)
	engine.GET("/release", releasePage)
	engine.GET("/release/", releasePage)
	engine.GET("/live", livePage)
	engine.GET("/live/", livePage)
	engine.GET("/assets/i18n.js", i18nScript)
	engine.GET("/assets/auth.js", authScript)
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
	trackingRepo := repository.NewTrackingRepository(db)
	trackingService := service.NewTrackingService(trackingRepo)
	trackingHandler := handler.NewTrackingHandler(trackingService)
	flightLogRepo := repository.NewFlightLogRepository(db)
	flightLogService := service.NewFlightLogService(flightLogRepo)
	flightLogHandler := handler.NewFlightLogHandler(flightLogService)
	landingPointRepo := repository.NewLandingPointRepository(db)
	landingPointService := service.NewLandingPointService(landingPointRepo)
	landingPointHandler := handler.NewLandingPointHandler(landingPointService)
	appReleaseRepository := repository.NewAppReleaseRepository(db)
	appReleaseService, err := service.NewAppReleaseService(
		appReleaseRepository,
		cfg.APKStorageDir,
		cfg.APKPackageName,
	)
	if err != nil {
		return nil, err
	}
	appReleaseHandler := handler.NewAppReleaseHandler(appReleaseService)
	v1 := engine.Group("/api/v1")
	{
		auth := v1.Group("/auth")
		auth.POST("/register", middleware.NewFixedWindowLimiter(5, time.Minute, 10000).Middleware(), authHandler.Register)
		auth.POST("/login", middleware.NewFixedWindowLimiter(10, time.Minute, 10000).Middleware(), authHandler.Login)
		auth.POST("/refresh", middleware.NewFixedWindowLimiter(30, time.Minute, 10000).Middleware(), authHandler.Refresh)
		auth.POST("/logout", middleware.NewFixedWindowLimiter(10, time.Minute, 10000).Middleware(), authHandler.Logout)

		v1.GET("/app/releases", middleware.NewFixedWindowLimiter(60, time.Minute, 10000).Middleware(), appReleaseHandler.List)
		v1.GET("/app/releases/latest", middleware.NewFixedWindowLimiter(60, time.Minute, 10000).Middleware(), appReleaseHandler.Latest)
		v1.GET("/app/releases/:version_code/download", middleware.NewFixedWindowLimiter(20, time.Minute, 10000).Middleware(), appReleaseHandler.Download)
		v1.GET("/admin/app/releases", middleware.NewFixedWindowLimiter(60, time.Minute, 10000).Middleware(), middleware.RequireReleaseAdmin(cfg.APKReleaseAdminToken), appReleaseHandler.List)
		v1.DELETE("/admin/app/releases/:version_code", middleware.NewFixedWindowLimiter(30, time.Minute, 10000).Middleware(), middleware.RequireReleaseAdmin(cfg.APKReleaseAdminToken), appReleaseHandler.Delete)
		v1.POST("/admin/app/releases", middleware.NewFixedWindowLimiter(5, time.Minute, 10000).Middleware(), middleware.RequireReleaseAdmin(cfg.APKReleaseAdminToken), appReleaseHandler.Publish)
		v1.GET("/map/active-locations", middleware.NewFixedWindowLimiter(60, time.Minute, 10000).Middleware(), trackingHandler.ListPublicLocations)

		protected := v1.Group("")
		protected.Use(middleware.RequireAuth(tokens, repository.NewRefreshTokenRepository(db)))
		protected.GET("/user/profile", userHandler.Profile)
		protected.GET("/tracking", trackingHandler.ListActive)
		protected.POST("/tracking/sessions", middleware.NewFixedWindowLimiter(10, time.Minute, 10000).Middleware(), trackingHandler.Start)
		protected.POST("/tracking/sessions/:session_id/points", middleware.NewFixedWindowLimiter(60, time.Minute, 10000).Middleware(), trackingHandler.AddPoints)
		protected.PATCH("/tracking/sessions/:session_id/public-sharing", middleware.NewFixedWindowLimiter(20, time.Minute, 10000).Middleware(), trackingHandler.SetPublicSharing)
		protected.POST("/tracking/sessions/:session_id/stop", trackingHandler.Stop)
		protected.GET("/flight-logs", middleware.NewFixedWindowLimiter(30, time.Minute, 10000).Middleware(), flightLogHandler.List)
		protected.POST("/flight-logs", middleware.NewFixedWindowLimiter(10, time.Minute, 10000).Middleware(), flightLogHandler.Import)
		protected.PUT("/flight-logs/sync", middleware.NewFixedWindowLimiter(5, time.Minute, 10000).Middleware(), flightLogHandler.Sync)
		protected.POST("/flight-logs/syncs", middleware.NewFixedWindowLimiter(10, time.Minute, 10000).Middleware(), flightLogHandler.BeginSync)
		protected.PUT("/flight-logs/syncs/:sync_id/batches/:batch_index", middleware.NewFixedWindowLimiter(600, time.Minute, 10000).Middleware(), flightLogHandler.UploadSyncBatch)
		protected.POST("/flight-logs/syncs/:sync_id/commit", middleware.NewFixedWindowLimiter(10, time.Minute, 10000).Middleware(), flightLogHandler.CommitSync)
		protected.GET("/landing-points", middleware.NewFixedWindowLimiter(30, time.Minute, 10000).Middleware(), landingPointHandler.List)
		protected.POST("/landing-points", middleware.NewFixedWindowLimiter(10, time.Minute, 10000).Middleware(), landingPointHandler.Import)
		protected.GET("/chat", handler.NotImplemented("chat_im"))
	}
	return engine, nil
}
