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

	"github.com/joho/godotenv"
	"github.com/shalarasgar/carpool-backend/internal/auth"
	"github.com/shalarasgar/carpool-backend/internal/config"
	"github.com/shalarasgar/carpool-backend/internal/http"
	"github.com/shalarasgar/carpool-backend/internal/jobs"
	appmigrate "github.com/shalarasgar/carpool-backend/internal/migrate"
	"github.com/shalarasgar/carpool-backend/internal/ratelimit"
	"github.com/shalarasgar/carpool-backend/internal/service"
	"github.com/shalarasgar/carpool-backend/internal/sms"
	"github.com/shalarasgar/carpool-backend/internal/storage"
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

	var provider sms.Provider
	switch cfg.SMSProvider {
	case "noop":
		provider = sms.NewNoopProvider(logger)
	default:
		provider = sms.NewQueueProvider(pool, cfg.SMSGatewayHealthyWindow, logger)
	}

	authSvc := service.NewAuth(
		pool,
		auth.NewPhoneNormalizer(cfg.AllowedPhonePrefixes),
		auth.NewOTP(cfg.OTPHMACSecret, cfg.OTPMaxAttempts),
		auth.NewJWT(cfg.JWTSecret, cfg.AppName, cfg.AccessTokenTTL),
		provider,
		ratelimit.NewMemory(),
		service.AuthConfig{
			AppName:         cfg.AppName,
			DevMode:         cfg.DevMode,
			OTPTTL:          cfg.OTPTTL,
			PhoneCooldown:   cfg.OTPPhoneCooldown,
			PhoneDailyLimit: cfg.OTPPhoneDailyLimit,
			IPLimit:         cfg.OTPIPLimit,
			IPWindow:        cfg.OTPIPWindow,
			DeviceLimit:     cfg.OTPDeviceLimit,
			DeviceWindow:    cfg.OTPDeviceWindow,
			RefreshTokenTTL: cfg.RefreshTokenTTL,
		},
		logger,
	)
	gatewaySvc := service.NewGateway(pool, service.GatewayConfig{
		LockDuration: cfg.SMSLockDuration,
		MaxAttempts:  cfg.SMSMaxAttempts,
		MaxBatch:     cfg.SMSGatewayMaxBatch,
	}, logger)
	photos, err := storage.NewLocal(cfg.PhotoDir, cfg.PhotoURLPrefix)
	if err != nil {
		return err
	}

	router := server.NewRouter(server.Deps{
		Logger:     logger,
		Health:     service.NewHealth(store.NewReadiness(pool), logger).WithGatewayWindow(cfg.SMSGatewayHealthyWindow),
		Auth:       authSvc,
		Gateway:    gatewaySvc,
		Users:      service.NewUsers(pool, photos, service.UserConfig{PhotoMaxBytes: cfg.PhotoMaxBytes, AllowedTypes: cfg.PhotoAllowedTypes, PhoneHashKey: cfg.OTPHMACSecret}, logger),
		Vehicles:   service.NewVehicles(pool, cfg.MaxVehiclesPerUser, logger),
		Locations:  service.NewLocations(pool),
		Photos:     photos,
		PhotoMax:   cfg.PhotoMaxBytes,
		AdminToken: cfg.AdminToken,
		TrustProxy: cfg.TrustProxyHeaders,
	})

	jobsCtx, stopJobs := context.WithCancel(ctx)
	defer stopJobs()
	jobsDone := make(chan struct{})
	go func() {
		defer close(jobsDone)
		jobs.New(pool, jobs.Config{
			Interval:             cfg.JobsInterval,
			GatewayHealthyWindow: cfg.SMSGatewayHealthyWindow,
		}, logger).Run(jobsCtx)
	}()

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
		stopJobs()
		<-jobsDone
		return nil
	}
}
