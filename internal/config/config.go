// Package config loads process configuration from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/shalarasgar/carpool-backend/internal/auth"
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

	OTPTTL             time.Duration `envconfig:"OTP_TTL" default:"5m"`
	OTPMaxAttempts     int           `envconfig:"OTP_MAX_ATTEMPTS" default:"5"`
	OTPPhoneInterval   time.Duration `envconfig:"OTP_PHONE_INTERVAL" default:"1m"`
	OTPPhoneDailyMax   int           `envconfig:"OTP_PHONE_DAILY_MAX" default:"5"`
	OTPIPHourlyMax     int           `envconfig:"OTP_IP_HOURLY_MAX" default:"30"`
	OTPDeviceHourlyMax int           `envconfig:"OTP_DEVICE_HOURLY_MAX" default:"10"`
	OTPMobilePrefixes  string        `envconfig:"OTP_MOBILE_PREFIXES" default:"6,7"`
	OTPTestPhones      string        `envconfig:"OTP_TEST_PHONES"`
	SMSMaxAttempts     int           `envconfig:"SMS_MAX_ATTEMPTS" default:"3"`
	SMSLease           time.Duration `envconfig:"SMS_LEASE" default:"60s"`
	SMSSendingLease    time.Duration `envconfig:"SMS_SENDING_LEASE" default:"3m"`
	GatewayRatePerMin  int           `envconfig:"GATEWAY_RATE_PER_MIN" default:"120"`
	AccessTokenTTL     time.Duration `envconfig:"ACCESS_TOKEN_TTL" default:"15m"`
	RefreshTokenTTL    time.Duration `envconfig:"REFRESH_TOKEN_TTL" default:"720h"`

	// Product rules.
	TimeZone          string        `envconfig:"APP_TIMEZONE" default:"Asia/Ashgabat"`
	TripOverlapBuffer time.Duration `envconfig:"TRIP_OVERLAP_BUFFER" default:"30m"`
	TripCompleteGrace time.Duration `envconfig:"TRIP_COMPLETE_GRACE" default:"6h"`
	TripRemindBefore  time.Duration `envconfig:"TRIP_REMIND_BEFORE" default:"2h"`
	MediaDir          string        `envconfig:"MEDIA_DIR" default:"./media"`
	APIRatePerMin     int           `envconfig:"API_RATE_PER_MIN" default:"120"`
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
		slog.Int("otp_test_phones", len(c.TestPhones())),
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
	if c.OTPTTL <= 0 || c.OTPPhoneInterval <= 0 || c.SMSLease <= 0 || c.SMSSendingLease <= 0 {
		return fmt.Errorf("OTP and SMS durations must be greater than 0")
	}
	if c.OTPMaxAttempts < 1 || c.OTPPhoneDailyMax < 1 || c.OTPIPHourlyMax < 1 || c.OTPDeviceHourlyMax < 1 || c.SMSMaxAttempts < 1 {
		return fmt.Errorf("OTP and SMS attempt limits must be at least 1")
	}
	if c.AccessTokenTTL <= 0 || c.RefreshTokenTTL <= 0 || c.GatewayRatePerMin < 1 {
		return fmt.Errorf("token TTL and gateway rate must be positive")
	}
	if _, err := time.LoadLocation(c.TimeZone); err != nil {
		return fmt.Errorf("APP_TIMEZONE is not a known time zone")
	}
	if c.TripOverlapBuffer < 0 || c.TripCompleteGrace < 0 || c.TripRemindBefore < 0 {
		return fmt.Errorf("trip durations must not be negative")
	}
	if c.APIRatePerMin < 1 || strings.TrimSpace(c.MediaDir) == "" {
		return fmt.Errorf("API_RATE_PER_MIN must be positive and MEDIA_DIR is required")
	}
	if _, err := splitPrefixes(c.OTPMobilePrefixes); err != nil {
		return err
	}
	if _, err := c.parseTestPhones(); err != nil {
		return err
	}
	return nil
}

// MobilePrefixes returns the allowed first digits of a national Turkmenistan number.
func (c Config) MobilePrefixes() []string {
	prefixes, _ := splitPrefixes(c.OTPMobilePrefixes)
	return prefixes
}

// TestPhones returns the normalized numbers that skip OTP rate limits.
func (c Config) TestPhones() map[string]bool {
	phones, _ := c.parseTestPhones()
	return phones
}

func (c Config) parseTestPhones() (map[string]bool, error) {
	out := map[string]bool{}
	for _, raw := range strings.Split(c.OTPTestPhones, ",") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		phone, err := auth.NormalizePhone(raw, c.MobilePrefixes())
		if err != nil {
			return nil, fmt.Errorf("OTP_TEST_PHONES has an invalid number %q", strings.TrimSpace(raw))
		}
		out[phone] = true
	}
	return out, nil
}

func splitPrefixes(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return nil, fmt.Errorf("OTP_MOBILE_PREFIXES must be comma-separated digits")
			}
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("OTP_MOBILE_PREFIXES must contain at least one prefix")
	}
	return out, nil
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
