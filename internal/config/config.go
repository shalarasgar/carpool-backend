// Package config loads process configuration from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

const minSecretLength = 16

// Config is the process configuration. It implements slog.LogValuer so a
// structured log of the value omits secrets and the database URL.
type Config struct {
	AppName string `envconfig:"APP_NAME" default:"carpool"`
	Port    int    `envconfig:"PORT" default:"8080"`
	DevMode bool   `envconfig:"DEV_MODE" default:"false"`

	DatabaseURL   string `envconfig:"DATABASE_URL" required:"true"`
	JWTSecret     string `envconfig:"JWT_SECRET" required:"true"`
	OTPHMACSecret string `envconfig:"OTP_HMAC_SECRET" required:"true"`
	AdminToken    string `envconfig:"ADMIN_TOKEN" required:"true"`

	MigrateOnStart bool `envconfig:"MIGRATE_ON_START" default:"false"`

	DBMaxConns          int32         `envconfig:"DB_MAX_CONNS" default:"10"`
	DBMinConns          int32         `envconfig:"DB_MIN_CONNS" default:"2"`
	DBMaxConnLifetime   time.Duration `envconfig:"DB_MAX_CONN_LIFETIME" default:"30m"`
	DBMaxConnIdleTime   time.Duration `envconfig:"DB_MAX_CONN_IDLE_TIME" default:"5m"`
	DBHealthCheckPeriod time.Duration `envconfig:"DB_HEALTH_CHECK_PERIOD" default:"1m"`

	HTTPReadHeaderTimeout time.Duration `envconfig:"HTTP_READ_HEADER_TIMEOUT" default:"5s"`
	HTTPReadTimeout       time.Duration `envconfig:"HTTP_READ_TIMEOUT" default:"10s"`
	HTTPWriteTimeout      time.Duration `envconfig:"HTTP_WRITE_TIMEOUT" default:"15s"`
	HTTPIdleTimeout       time.Duration `envconfig:"HTTP_IDLE_TIMEOUT" default:"60s"`
	ShutdownTimeout       time.Duration `envconfig:"SHUTDOWN_TIMEOUT" default:"15s"`
}

// Load reads configuration from the environment.
func Load() (Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Addr is the HTTP listen address.
func (c Config) Addr() string {
	return fmt.Sprintf(":%d", c.Port)
}

// LogValue implements slog.LogValuer.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("app_name", c.AppName),
		slog.Int("port", c.Port),
		slog.Bool("dev_mode", c.DevMode),
		slog.Bool("migrate_on_start", c.MigrateOnStart),
		slog.Int("db_max_conns", int(c.DBMaxConns)),
		slog.Int("db_min_conns", int(c.DBMinConns)),
		slog.Duration("db_max_conn_lifetime", c.DBMaxConnLifetime),
		slog.Duration("db_max_conn_idle_time", c.DBMaxConnIdleTime),
		slog.Duration("db_health_check_period", c.DBHealthCheckPeriod),
	)
}

func (c Config) validate() error {
	if strings.TrimSpace(c.AppName) == "" {
		return fmt.Errorf("APP_NAME is required")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("PORT must be between 1 and 65535")
	}
	if err := validateDatabaseURL(c.DatabaseURL); err != nil {
		return err
	}
	if err := validateSecret("JWT_SECRET", c.JWTSecret); err != nil {
		return err
	}
	if err := validateSecret("OTP_HMAC_SECRET", c.OTPHMACSecret); err != nil {
		return err
	}
	if err := validateSecret("ADMIN_TOKEN", c.AdminToken); err != nil {
		return err
	}
	if c.DBMaxConns < 1 {
		return fmt.Errorf("DB_MAX_CONNS must be at least 1")
	}
	if c.DBMinConns < 0 || c.DBMinConns > c.DBMaxConns {
		return fmt.Errorf("DB_MIN_CONNS must be between 0 and DB_MAX_CONNS")
	}
	if c.DBMaxConnLifetime <= 0 {
		return fmt.Errorf("DB_MAX_CONN_LIFETIME must be greater than 0")
	}
	if c.DBMaxConnIdleTime <= 0 {
		return fmt.Errorf("DB_MAX_CONN_IDLE_TIME must be greater than 0")
	}
	if c.DBHealthCheckPeriod <= 0 {
		return fmt.Errorf("DB_HEALTH_CHECK_PERIOD must be greater than 0")
	}
	if c.HTTPReadHeaderTimeout <= 0 || c.HTTPReadTimeout <= 0 || c.HTTPWriteTimeout <= 0 || c.HTTPIdleTimeout <= 0 {
		return fmt.Errorf("HTTP timeouts must be greater than 0")
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("SHUTDOWN_TIMEOUT must be greater than 0")
	}
	return nil
}

func validateDatabaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("DATABASE_URL is invalid: %w", err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return fmt.Errorf("DATABASE_URL must use the postgres or postgresql scheme")
	}
	if parsed.Host == "" || parsed.Path == "" || parsed.Path == "/" {
		return fmt.Errorf("DATABASE_URL must include a host and database name")
	}
	return nil
}

func validateSecret(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if len(value) < minSecretLength {
		return fmt.Errorf("%s must be at least %d characters", name, minSecretLength)
	}
	return nil
}
