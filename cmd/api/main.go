package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // APP_TIMEZONE must resolve on hosts and images without a zoneinfo database

	"github.com/joho/godotenv"
	"github.com/shalarasgar/carpool-backend/internal/config"
	"github.com/shalarasgar/carpool-backend/internal/http"
	"github.com/shalarasgar/carpool-backend/internal/http/middleware"
	"github.com/shalarasgar/carpool-backend/internal/jobs"
	"github.com/shalarasgar/carpool-backend/internal/media"
	appmigrate "github.com/shalarasgar/carpool-backend/internal/migrate"
	"github.com/shalarasgar/carpool-backend/internal/push"
	"github.com/shalarasgar/carpool-backend/internal/service"
	"github.com/shalarasgar/carpool-backend/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("load .env: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	logger.Info("starting", "config", cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.MigrateOnStart {
		logger.Info("applying migrations")
		if err := appmigrate.Up(cfg.DatabaseURL); err != nil {
			return err
		}
	}

	pool, err := store.NewPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	appDB := store.NewAppDB(pool)
	reviewPhone, reviewCode := cfg.ReviewLogin()
	authSvc := service.NewAuth(appDB, service.AuthConfig{
		AppName:         cfg.AppName,
		OTPSecret:       cfg.OTPHMACSecret,
		JWTSecret:       cfg.JWTSecret,
		Prefixes:        cfg.MobilePrefixes(),
		OTPTTL:          cfg.OTPTTL,
		MaxAttempts:     cfg.OTPMaxAttempts,
		PhoneInterval:   cfg.OTPPhoneInterval,
		PhoneDailyMax:   cfg.OTPPhoneDailyMax,
		IPHourlyMax:     cfg.OTPIPHourlyMax,
		DeviceHourlyMax: cfg.OTPDeviceHourlyMax,
		AccessTTL:       cfg.AccessTokenTTL,
		RefreshTTL:      cfg.RefreshTokenTTL,
		ReturnDevCode:   cfg.DevMode,
		TestPhones:      cfg.TestPhones(),
		ReviewPhone:     reviewPhone,
		ReviewCode:      reviewCode,
	}, logger)
	gatewaySvc := service.NewGateway(appDB, service.GatewayConfig{
		Lease:         cfg.SMSLease,
		SendingLease:  cfg.SMSSendingLease,
		MaxAttempts:   cfg.SMSMaxAttempts,
		MessageMaxAge: cfg.OTPTTL,
		RatePerMinute: cfg.GatewayRatePerMin,
	}, logger)
	go jobs.NewReaper(gatewaySvc, 15*time.Second, logger).Run(ctx)

	tripSvc := service.NewTrips(appDB, service.TripConfig{
		OverlapBuffer: cfg.TripOverlapBuffer,
		TimeZone:      cfg.TimeZone,
		CompleteGrace: cfg.TripCompleteGrace,
		RemindBefore:  cfg.TripRemindBefore,
	}, logger)
	go jobs.NewReaper(tripSvc, time.Minute, logger.With("job", "trip_lifecycle")).Run(ctx)

	if cfg.PushEnabled() {
		fcm, err := push.LoadFCM(cfg.FCMCredentialsFile)
		if err != nil {
			return err
		}
		dispatcher := push.NewDispatcher(appDB, fcm, push.Config{
			Title:    cfg.AppName,
			MaxAge:   cfg.PushMaxAge,
			TokenTTL: cfg.PushTokenTTL,
		}, logger.With("job", "push"))
		go jobs.NewReaper(dispatcher, cfg.PushInterval, logger.With("job", "push")).Run(ctx)
		logger.Info("push notifications enabled", "firebase_project", fcm.ProjectID())
	} else {
		logger.Info("push notifications disabled: FCM_CREDENTIALS_FILE is not set")
	}

	router := server.NewRouter(server.Deps{
		Logger:     logger,
		Health:     service.NewHealth(store.NewReadiness(pool), logger),
		Auth:       authSvc,
		Gateway:    gatewaySvc,
		AdminToken: cfg.AdminToken,
		DevMode:    cfg.DevMode,
		TrustProxy: cfg.TrustProxy,
		JWTSecret:  cfg.JWTSecret,
		Users:      appDB,
		Profiles:   service.NewProfiles(appDB, media.Disk{Dir: cfg.MediaDir}, authSvc, logger),
		Trips:      tripSvc,
		Bookings:   service.NewBookings(appDB),
		PushTokens: service.NewPushTokens(appDB),
		Moderation: service.NewModeration(appDB),
		Limiter:    &middleware.WindowLimiter{Max: cfg.APIRatePerMin, Window: time.Minute},
		MediaDir:   cfg.MediaDir,
	})

	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           router,
		ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
		ReadTimeout:       cfg.HTTPReadTimeout,
		WriteTimeout:      cfg.HTTPWriteTimeout,
		IdleTimeout:       cfg.HTTPIdleTimeout,
		MaxHeaderBytes:    1 << 20,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Addr())
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}
