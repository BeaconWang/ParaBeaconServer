package router

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/parabeacon/server/internal/config"
	"github.com/parabeacon/server/internal/handler"
	"github.com/parabeacon/server/internal/middleware"
	"github.com/parabeacon/server/internal/realtime"
	"github.com/parabeacon/server/internal/repository"
	"github.com/parabeacon/server/internal/service"
	webapp "github.com/parabeacon/server/internal/web"
	"github.com/parabeacon/server/pkg/utils"
	"gorm.io/gorm"
)

func New(cfg config.Config, db *gorm.DB, authService *service.AuthService, tokens *utils.TokenManager, liveHub *realtime.Hub, chatHub *realtime.ChatHub, chatMediaServices ...*service.ChatMediaService) (*gin.Engine, error) {
	if liveHub == nil {
		liveHub = realtime.NewHub(nil, nil, nil)
	}
	if chatHub == nil {
		chatHub = realtime.NewChatHub(nil, nil, nil)
	}
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
	profileImageStorage, err := service.NewProfileImageStorage(cfg.ProfileImageStorageDir)
	if err != nil {
		return nil, err
	}
	profileImageService := service.NewProfileImageService(repository.NewUserRepository(db), profileImageStorage)
	profileImageHandler := handler.NewProfileImageHandler(profileImageService)
	trackingRepo := repository.NewTrackingRepository(db)
	trackingService := service.NewTrackingService(trackingRepo)
	trackingHandler := handler.NewTrackingHandler(trackingService, liveHub)
	chatRepository := repository.NewChatRepository(db)
	chatService := service.NewChatService(chatRepository)
	chatHandler := handler.NewChatHandler(chatService, chatHub)
	var chatMediaService *service.ChatMediaService
	if len(chatMediaServices) > 0 {
		chatMediaService = chatMediaServices[0]
	}
	if chatMediaService == nil {
		chatMediaStorage, err := service.NewChatMediaStorage(cfg.ChatMediaStorageDir)
		if err != nil {
			return nil, err
		}
		chatMediaService = service.NewChatMediaService(chatRepository, chatService, chatMediaStorage)
	}
	chatMediaHandler := handler.NewChatMediaHandler(chatMediaService)
	flightLogRepo := repository.NewFlightLogRepository(db)
	flightArchiveStorage, err := service.NewFlightArchiveStorage(cfg.FlightLogStorageDir)
	if err != nil {
		return nil, err
	}
	flightLogService := service.NewFlightLogService(flightLogRepo, flightArchiveStorage)
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
	sessionMetricsHandler := handler.NewSessionMetricsHandler(
		service.NewSessionMetricsService(repository.NewSessionMetricRepository(db)),
		middleware.NewFixedWindowLimiter(120, time.Minute, 10000),
		middleware.NewFixedWindowLimiter(120, time.Minute, 10000),
	)
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
		v1.GET("/map/active-locations", middleware.NewFixedWindowLimiter(60, time.Minute, 10000).Middleware(), middleware.OptionalAuth(tokens, repository.NewRefreshTokenRepository(db)), trackingHandler.ListPublicLocations)
		v1.GET("/profile-images/:image_key", middleware.NewFixedWindowLimiter(120, time.Minute, 10000).Middleware(), profileImageHandler.Get)
		v1.POST("/metrics/session", middleware.NewFixedWindowLimiter(120, time.Minute, 10000).Middleware(), middleware.OptionalAuth(tokens, repository.NewRefreshTokenRepository(db)), sessionMetricsHandler.Ingest)

		protected := v1.Group("")
		protected.Use(middleware.RequireAuth(tokens, repository.NewRefreshTokenRepository(db)))
		protected.GET("/user/profile", userHandler.Profile)
		protected.PATCH("/user/profile", middleware.NewFixedWindowLimiter(10, time.Minute, 10000).Middleware(), userHandler.UpdateProfile)
		protected.POST("/user/profile-image", middleware.NewFixedWindowLimiter(10, time.Hour, 10000).Middleware(), profileImageHandler.Upload)
		protected.DELETE("/user/profile-image", middleware.NewFixedWindowLimiter(10, time.Hour, 10000).Middleware(), profileImageHandler.Remove)
		protected.POST("/user/change-password", middleware.NewFixedWindowLimiter(5, time.Minute, 10000).Middleware(), userHandler.ChangePassword)
		protected.GET("/tracking", trackingHandler.ListActive)
		protected.GET("/tracking/live", middleware.NewFixedWindowLimiter(30, time.Minute, 10000).Middleware(), realtime.NewWebSocketHandler(
			liveHub,
			cfg.CORSAllowedOrigin,
			repository.NewLiveRoomAuthorizer(db),
		))
		protected.POST("/tracking/sessions", middleware.NewFixedWindowLimiter(10, time.Minute, 10000).Middleware(), trackingHandler.Start)
		protected.POST("/tracking/sessions/:session_id/points", middleware.NewFixedWindowLimiter(60, time.Minute, 10000).Middleware(), trackingHandler.AddPoints)
		protected.PATCH("/tracking/sessions/:session_id/public-sharing", middleware.NewFixedWindowLimiter(20, time.Minute, 10000).Middleware(), trackingHandler.SetPublicSharing)
		protected.POST("/tracking/sessions/:session_id/stop", trackingHandler.Stop)
		protected.GET("/flight-logs", middleware.NewFixedWindowLimiter(30, time.Minute, 10000).Middleware(), flightLogHandler.List)
		protected.POST("/flight-logs", middleware.NewFixedWindowLimiter(10, time.Minute, 10000).Middleware(), flightLogHandler.Import)
		protected.PUT("/flight-logs/sync", middleware.NewFixedWindowLimiter(5, time.Minute, 10000).Middleware(), flightLogHandler.Sync)
		protected.POST("/flight-logs/syncs", middleware.NewFixedWindowLimiter(10, time.Minute, 10000).Middleware(), flightLogHandler.BeginSync)
		protected.PUT("/flight-logs/syncs/:sync_id/batches/:batch_index", middleware.NewFixedWindowLimiter(600, time.Minute, 10000).Middleware(), flightLogHandler.UploadSyncBatch)
		protected.PUT("/flight-logs/syncs/:sync_id/flights/:flight_index", middleware.NewFixedWindowLimiter(600, time.Minute, 10000).Middleware(), flightLogHandler.UploadSyncFlightArchive)
		protected.POST("/flight-logs/syncs/:sync_id/commit", middleware.NewFixedWindowLimiter(10, time.Minute, 10000).Middleware(), flightLogHandler.CommitSync)
		protected.GET("/landing-points", middleware.NewFixedWindowLimiter(30, time.Minute, 10000).Middleware(), landingPointHandler.List)
		protected.POST("/landing-points", middleware.NewFixedWindowLimiter(10, time.Minute, 10000).Middleware(), landingPointHandler.Import)
		protected.GET("/chat/messages", middleware.NewFixedWindowLimiter(120, time.Minute, 10000).Middleware(), chatHandler.ListMessages)
		protected.POST("/chat/media", middleware.NewFixedWindowLimiter(20, time.Hour, 10000).Middleware(), chatMediaHandler.Upload)
		protected.GET("/chat/media/:media_id", middleware.NewFixedWindowLimiter(120, time.Minute, 10000).Middleware(), chatMediaHandler.Get)
		protected.GET("/chat/accounts", middleware.NewFixedWindowLimiter(30, time.Minute, 10000).Middleware(), chatHandler.SearchAccounts)
		protected.GET("/chat/conversations", middleware.NewFixedWindowLimiter(60, time.Minute, 10000).Middleware(), chatHandler.ListConversations)
		protected.PUT("/chat/conversations/:pilot_number/pin", middleware.NewFixedWindowLimiter(30, time.Minute, 10000).Middleware(), chatHandler.SetConversationPinned)
		protected.PUT("/chat/conversations/:pilot_number/read", middleware.NewFixedWindowLimiter(120, time.Minute, 10000).Middleware(), chatHandler.MarkConversationRead)
		protected.GET("/chat/pilots/:pilot_number/location-status", middleware.NewFixedWindowLimiter(60, time.Minute, 10000).Middleware(), chatHandler.PilotLocationStatus)
		protected.POST("/chat/socket-ticket", middleware.NewFixedWindowLimiter(20, time.Minute, 10000).Middleware(), chatHandler.IssueSocketTicket)
		v1.GET("/chat/ws", middleware.NewFixedWindowLimiter(60, time.Minute, 10000).Middleware(), chatHandler.WebSocket(cfg.CORSAllowedOrigin))
	}
	return engine, nil
}
