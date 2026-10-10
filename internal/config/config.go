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

	// Phone validation: digits that may follow +993. "6,7" accepts +9936xxxxxxx and +9937xxxxxxx.
	AllowedPhonePrefixes []string `envconfig:"ALLOWED_PHONE_PREFIXES" default:"6,7"`

	// OTP
	OTPTTL             time.Duration `envconfig:"OTP_TTL" default:"5m"`
	OTPMaxAttempts     int           `envconfig:"OTP_MAX_ATTEMPTS" default:"5"`
	OTPPhoneCooldown   time.Duration `envconfig:"OTP_PHONE_COOLDOWN" default:"60s"`
	OTPPhoneDailyLimit int           `envconfig:"OTP_PHONE_DAILY_LIMIT" default:"5"`
	OTPIPLimit         int           `envconfig:"OTP_IP_LIMIT" default:"30"`
	OTPIPWindow        time.Duration `envconfig:"OTP_IP_WINDOW" default:"1h"`
	OTPDeviceLimit     int           `envconfig:"OTP_DEVICE_LIMIT" default:"10"`
	OTPDeviceWindow    time.Duration `envconfig:"OTP_DEVICE_WINDOW" default:"1h"`
	RateLimiterBackend string        `envconfig:"RATE_LIMITER" default:"memory"`

	// Tokens
	AccessTokenTTL  time.Duration `envconfig:"ACCESS_TOKEN_TTL" default:"15m"`
	RefreshTokenTTL time.Duration `envconfig:"REFRESH_TOKEN_TTL" default:"720h"`

	// SMS
	SMSProvider             string        `envconfig:"SMS_PROVIDER" default:"queue"`
	SMSMaxAttempts          int           `envconfig:"SMS_MAX_ATTEMPTS" default:"3"`
	SMSLockDuration         time.Duration `envconfig:"SMS_LOCK_DURATION" default:"60s"`
	SMSGatewayHealthyWindow time.Duration `envconfig:"SMS_GATEWAY_HEALTHY_WINDOW" default:"30s"`
	SMSGatewayMaxBatch      int           `envconfig:"SMS_GATEWAY_MAX_BATCH" default:"50"`

	// Background jobs
	JobsInterval time.Duration `envconfig:"JOBS_INTERVAL" default:"30s"`

	// Profile photos. Types are sniffed from the file bytes, not the multipart header.
	PhotoMaxBytes     int64    `envconfig:"PHOTO_MAX_BYTES" default:"2097152"`
	PhotoAllowedTypes []string `envconfig:"PHOTO_ALLOWED_TYPES" default:"image/jpeg,image/png,image/webp"`
	PhotoDir          string   `envconfig:"PHOTO_DIR" default:"data/photos"`
	PhotoURLPrefix    string   `envconfig:"PHOTO_URL_PREFIX" default:"/v1/media/photos"`

	// Vehicles
	MaxVehiclesPerUser int `envconfig:"MAX_VEHICLES_PER_USER" default:"3"`

	// Set to true only behind a reverse proxy that overwrites X-Forwarded-For.
	TrustProxyHeaders bool `envconfig:"TRUST_PROXY_HEADERS" default:"false"`

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
		slog.Any("allowed_phone_prefixes", c.AllowedPhonePrefixes),
		slog.Duration("otp_ttl", c.OTPTTL),
		slog.Int("otp_max_attempts", c.OTPMaxAttempts),
		slog.Duration("otp_phone_cooldown", c.OTPPhoneCooldown),
		slog.Int("otp_phone_daily_limit", c.OTPPhoneDailyLimit),
		slog.String("rate_limiter", c.RateLimiterBackend),
		slog.Duration("access_token_ttl", c.AccessTokenTTL),
		slog.Duration("refresh_token_ttl", c.RefreshTokenTTL),
		slog.String("sms_provider", c.SMSProvider),
		slog.Int("sms_max_attempts", c.SMSMaxAttempts),
		slog.Duration("sms_lock_duration", c.SMSLockDuration),
		slog.Duration("sms_gateway_healthy_window", c.SMSGatewayHealthyWindow),
		slog.Duration("jobs_interval", c.JobsInterval),
		slog.Int64("photo_max_bytes", c.PhotoMaxBytes),
		slog.Any("photo_allowed_types", c.PhotoAllowedTypes),
		slog.String("photo_dir", c.PhotoDir),
		slog.Int("max_vehicles_per_user", c.MaxVehiclesPerUser),
		slog.Bool("trust_proxy_headers", c.TrustProxyHeaders),
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
	if len(c.AllowedPhonePrefixes) == 0 {
		return fmt.Errorf("ALLOWED_PHONE_PREFIXES must list at least one prefix")
	}
	for _, p := range c.AllowedPhonePrefixes {
		if p == "" || len(p) > 8 || strings.Trim(p, "0123456789") != "" {
			return fmt.Errorf("ALLOWED_PHONE_PREFIXES entries must be 1-8 digits, got %q", p)
		}
	}
	if c.OTPTTL <= 0 || c.OTPPhoneCooldown <= 0 || c.OTPIPWindow <= 0 || c.OTPDeviceWindow <= 0 {
		return fmt.Errorf("OTP durations must be greater than 0")
	}
	if c.OTPMaxAttempts < 1 || c.OTPPhoneDailyLimit < 1 || c.OTPIPLimit < 1 || c.OTPDeviceLimit < 1 {
		return fmt.Errorf("OTP limits must be at least 1")
	}
	if c.RateLimiterBackend != "memory" {
		return fmt.Errorf("RATE_LIMITER must be \"memory\"")
	}
	if c.AccessTokenTTL <= 0 || c.RefreshTokenTTL <= 0 {
		return fmt.Errorf("token TTLs must be greater than 0")
	}
	if c.SMSProvider != "queue" && c.SMSProvider != "noop" {
		return fmt.Errorf("SMS_PROVIDER must be \"queue\" or \"noop\"")
	}
	if c.SMSMaxAttempts < 1 || c.SMSGatewayMaxBatch < 1 {
		return fmt.Errorf("SMS_MAX_ATTEMPTS and SMS_GATEWAY_MAX_BATCH must be at least 1")
	}
	if c.SMSLockDuration <= 0 || c.SMSGatewayHealthyWindow <= 0 {
		return fmt.Errorf("SMS durations must be greater than 0")
	}
	if c.JobsInterval <= 0 {
		return fmt.Errorf("JOBS_INTERVAL must be greater than 0")
	}
	if c.PhotoMaxBytes < 1 || c.PhotoMaxBytes > 5<<20 {
		return fmt.Errorf("PHOTO_MAX_BYTES must be between 1 and 5242880")
	}
	if len(c.PhotoAllowedTypes) == 0 {
		return fmt.Errorf("PHOTO_ALLOWED_TYPES must list at least one type")
	}
	for _, kind := range c.PhotoAllowedTypes {
		switch kind {
		case "image/jpeg", "image/png", "image/webp":
		default:
			return fmt.Errorf("PHOTO_ALLOWED_TYPES contains unsupported type %q", kind)
		}
	}
	if strings.TrimSpace(c.PhotoDir) == "" || strings.Contains(c.PhotoDir, "\x00") {
		return fmt.Errorf("PHOTO_DIR is required")
	}
	if !strings.HasPrefix(c.PhotoURLPrefix, "/") || strings.HasSuffix(c.PhotoURLPrefix, "/") || strings.Contains(c.PhotoURLPrefix, "..") {
		return fmt.Errorf("PHOTO_URL_PREFIX must be an absolute path without a trailing slash")
	}
	if c.MaxVehiclesPerUser < 1 || c.MaxVehiclesPerUser > 20 {
		return fmt.Errorf("MAX_VEHICLES_PER_USER must be between 1 and 20")
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
