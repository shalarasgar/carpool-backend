package config

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	setConfigEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.AppName != "carpool-test" {
		t.Fatalf("AppName = %q", cfg.AppName)
	}
	if cfg.Port != 9090 {
		t.Fatalf("Port = %d", cfg.Port)
	}
	if cfg.DevMode {
		t.Fatal("DevMode = true, want false")
	}
	if cfg.DBMaxConns != 4 || cfg.DBMinConns != 1 {
		t.Fatalf("pool conns = %d/%d", cfg.DBMaxConns, cfg.DBMinConns)
	}
	if cfg.DBMaxConnLifetime != time.Minute {
		t.Fatalf("lifetime = %s", cfg.DBMaxConnLifetime)
	}
	if cfg.Addr() != ":9090" {
		t.Fatalf("Addr() = %q", cfg.Addr())
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("DATABASE_URL", "")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want a missing DATABASE_URL error")
	}
}

func TestLoadRejectsShortSecret(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("JWT_SECRET", "too-short")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want a secret length error")
	}
}

func TestLoadRejectsPoolBounds(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("DB_MIN_CONNS", "8")
	t.Setenv("DB_MAX_CONNS", "2")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want a pool bounds error")
	}
}

func TestConfigLogValueOmitsSecrets(t *testing.T) {
	cfg := Config{
		AppName:       "carpool",
		Port:          8080,
		DatabaseURL:   "postgres://carpool:super-db-password@localhost:5432/carpool",
		JWTSecret:     "jwt-secret-value-should-not-leak",
		OTPHMACSecret: "otp-hmac-secret-should-not-leak",
		AdminToken:    "admin-token-value-should-not-leak",
		DBMaxConns:    10,
		DBMinConns:    2,
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("starting", "config", cfg)

	out := buf.String()
	for _, secret := range []string{cfg.DatabaseURL, cfg.JWTSecret, cfg.OTPHMACSecret, cfg.AdminToken, "super-db-password"} {
		if strings.Contains(out, secret) {
			t.Fatalf("log contains secret %q: %s", secret, out)
		}
	}
	if !strings.Contains(out, `"app_name":"carpool"`) {
		t.Fatalf("log missing app name: %s", out)
	}
}

func TestReviewLogin(t *testing.T) {
	cases := []struct {
		name, phone, code string
		wantPhone         string
		wantErr           bool
	}{
		{name: "off by default"},
		{name: "normalized", phone: "993 62 00-00-01", code: "482915", wantPhone: "+99362000001"},
		{name: "phone without code", phone: "+99362000001", wantErr: true},
		{name: "code without phone", code: "482915", wantErr: true},
		{name: "not a mobile number", phone: "+99312000001", code: "482915", wantErr: true},
		{name: "short code", phone: "+99362000001", code: "1234", wantErr: true},
		{name: "code with letters", phone: "+99362000001", code: "12a456", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setConfigEnv(t)
			t.Setenv("OTP_REVIEW_PHONE", tc.phone)
			t.Setenv("OTP_REVIEW_CODE", tc.code)

			cfg, err := Load()
			if tc.wantErr {
				if err == nil {
					t.Fatal("Load() error = nil, want a review login error")
				}
				if strings.Contains(err.Error(), tc.code) && tc.code != "" {
					t.Fatalf("the error repeats the code: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			phone, code := cfg.ReviewLogin()
			if phone != tc.wantPhone || code != tc.code {
				t.Fatalf("ReviewLogin() = %q, %q", phone, code)
			}
		})
	}
}

// The log line of the configuration says whether the review login is on, never its values.
func TestReviewLoginIsNotLogged(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("OTP_REVIEW_PHONE", "+99362000001")
	t.Setenv("OTP_REVIEW_CODE", "482915")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("starting", "config", cfg)
	out := buf.String()
	if strings.Contains(out, "482915") || strings.Contains(out, "62000001") {
		t.Fatalf("log line leaks the review login: %s", out)
	}
	if !strings.Contains(out, `"otp_review_login":true`) {
		t.Fatalf("log line does not say the review login is on: %s", out)
	}
}

func setConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_NAME", "carpool-test")
	t.Setenv("PORT", "9090")
	t.Setenv("DEV_MODE", "false")
	t.Setenv("DATABASE_URL", "postgres://carpool:carpool@localhost:5432/carpool?sslmode=disable")
	t.Setenv("JWT_SECRET", "test-jwt-secret-value")
	t.Setenv("OTP_HMAC_SECRET", "test-otp-hmac-secret")
	t.Setenv("ADMIN_TOKEN", "test-admin-token-value")
	t.Setenv("MIGRATE_ON_START", "false")
	t.Setenv("DB_MAX_CONNS", "4")
	t.Setenv("DB_MIN_CONNS", "1")
	t.Setenv("DB_MAX_CONN_LIFETIME", "1m")
	t.Setenv("DB_MAX_CONN_IDLE_TIME", "30s")
	t.Setenv("DB_HEALTH_CHECK_PERIOD", "15s")
	t.Setenv("HTTP_READ_HEADER_TIMEOUT", "2s")
	t.Setenv("HTTP_READ_TIMEOUT", "3s")
	t.Setenv("HTTP_WRITE_TIMEOUT", "4s")
	t.Setenv("HTTP_IDLE_TIMEOUT", "5s")
	t.Setenv("SHUTDOWN_TIMEOUT", "6s")
}
