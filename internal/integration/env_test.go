// Package integration runs the Phase B flows against a real PostgreSQL.
//
// Set TEST_DATABASE_URL (for example the docker-compose database) to enable
// these tests. Each test creates its own throwaway database on that server,
// applies the migrations, and drops the database at the end, so the
// development database is never touched.
package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shalarasgar/carpool-backend/internal/auth"
	server "github.com/shalarasgar/carpool-backend/internal/http"
	"github.com/shalarasgar/carpool-backend/internal/jobs"
	appmigrate "github.com/shalarasgar/carpool-backend/internal/migrate"
	"github.com/shalarasgar/carpool-backend/internal/notify"
	"github.com/shalarasgar/carpool-backend/internal/ratelimit"
	"github.com/shalarasgar/carpool-backend/internal/service"
	"github.com/shalarasgar/carpool-backend/internal/sms"
	"github.com/shalarasgar/carpool-backend/internal/storage"
	"github.com/shalarasgar/carpool-backend/internal/store"
	"github.com/stretchr/testify/require"
)

const (
	testAdminToken = "integration-admin-token-value"
	testAppName    = "Carpool"
)

type env struct {
	t       *testing.T
	pool    *pgxpool.Pool
	srv     *httptest.Server
	auth    *service.Auth
	gateway *service.Gateway
	jobs    *jobs.Runner
	sms     *sms.QueueProvider

	clockMu sync.Mutex
	clock   time.Time
}

// newEnv provisions a fresh database and a full HTTP stack. It skips the test
// when TEST_DATABASE_URL is not set.
func newEnv(t *testing.T) *env {
	t.Helper()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	ctx := context.Background()

	dbName := "carpool_test_" + randomSuffix()
	admin, err := pgx.Connect(ctx, baseURL)
	require.NoError(t, err, "connect to TEST_DATABASE_URL")
	_, err = admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s`, pgx.Identifier{dbName}.Sanitize()))
	require.NoError(t, err, "create test database")
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), fmt.Sprintf(`DROP DATABASE %s WITH (FORCE)`, pgx.Identifier{dbName}.Sanitize()))
		_ = admin.Close(context.Background())
	})

	dbURL := withDatabase(t, baseURL, dbName)
	require.NoError(t, appmigrate.Up(dbURL), "apply migrations")

	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := &env{t: t, pool: pool, clock: time.Now()}

	limiter := ratelimit.NewMemoryWithClock(e.now)
	notifier := notify.Inbox{}
	e.sms = sms.NewQueueProvider(pool, 30*time.Second, logger)
	e.auth = service.NewAuth(
		pool,
		auth.NewPhoneNormalizer([]string{"6", "7"}),
		auth.NewOTP("integration-otp-secret-value", 5),
		auth.NewJWT("integration-jwt-secret-value", testAppName, 15*time.Minute),
		e.sms,
		limiter,
		service.AuthConfig{
			AppName:         testAppName,
			DevMode:         true,
			OTPTTL:          5 * time.Minute,
			PhoneCooldown:   60 * time.Second,
			PhoneDailyLimit: 5,
			IPLimit:         1000,
			IPWindow:        time.Hour,
			DeviceLimit:     1000,
			DeviceWindow:    time.Hour,
			RefreshTokenTTL: 30 * 24 * time.Hour,
		},
		logger,
	)
	e.gateway = service.NewGateway(pool, service.GatewayConfig{
		LockDuration: 60 * time.Second,
		MaxAttempts:  3,
		MaxBatch:     50,
	}, logger)
	e.jobs = jobs.New(pool, jobs.Config{Interval: time.Minute, GatewayHealthyWindow: 30 * time.Second}, logger)

	photos, err := storage.NewLocal(t.TempDir(), "/v1/media/photos")
	require.NoError(t, err)
	const photoMax = 8 << 10
	router := server.NewRouter(server.Deps{
		Logger:        logger,
		Health:        service.NewHealth(store.NewReadiness(pool), logger),
		Auth:          e.auth,
		Gateway:       e.gateway,
		Users:         service.NewUsers(pool, photos, service.UserConfig{PhotoMaxBytes: photoMax, AllowedTypes: []string{"image/jpeg", "image/png", "image/webp"}, PhoneHashKey: "integration-otp-secret-value"}, notifier, logger),
		Vehicles:      service.NewVehicles(pool, 2, logger),
		Locations:     service.NewLocations(pool),
		Trips:         service.NewTrips(pool, notifier, logger),
		Bookings:      service.NewBookings(pool, notifier, logger),
		Reviews:       service.NewReviews(pool, service.ReviewConfig{Window: 14 * 24 * time.Hour}, logger),
		Blocks:        service.NewBlocks(pool, logger),
		Reports:       service.NewReports(pool, ratelimit.NewMemory(), service.ReportConfig{Limit: 5, Window: time.Hour}, logger),
		Notifications: service.NewNotifications(pool),
		Admin:         service.NewAdmin(pool, notifier, service.AdminConfig{GatewayHealthyWindow: 30 * time.Second}, logger),
		Photos:        photos,
		PhotoMax:      photoMax,
		AdminToken:    testAdminToken,
	})
	e.srv = httptest.NewServer(router)
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) now() time.Time {
	e.clockMu.Lock()
	defer e.clockMu.Unlock()
	return e.clock
}

// advance moves the rate limiter clock forward.
func (e *env) advance(d time.Duration) {
	e.clockMu.Lock()
	defer e.clockMu.Unlock()
	e.clock = e.clock.Add(d)
}

type response struct {
	Status  int
	Header  http.Header
	Body    []byte
	Err     errorEnvelope
	decoded map[string]any
}

type errorEnvelope struct {
	Error struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Details map[string]string `json:"details"`
	} `json:"error"`
}

// call performs a JSON request. body may be nil, a string (sent raw) or a value to marshal.
func (e *env) call(method, path string, body any, headers map[string]string) response {
	e.t.Helper()
	var reader io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		require.NoError(e.t, err)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, reader)
	require.NoError(e.t, err)
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := e.srv.Client().Do(req)
	require.NoError(e.t, err)
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	require.NoError(e.t, err)

	out := response{Status: res.StatusCode, Header: res.Header, Body: raw}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out.Err)
		_ = json.Unmarshal(raw, &out.decoded)
	}
	return out
}

// field reads a top-level JSON field as a string, number-as-string, or bool.
func (r response) field(name string) any {
	return r.decoded[name]
}

func (r response) str(name string) string {
	v, _ := r.decoded[name].(string)
	return v
}

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

func (e *env) createGateway(name string, prefixes ...string) (id, apiKey string) {
	e.t.Helper()
	res := e.call(http.MethodPost, "/admin/v1/gateways", map[string]any{
		"name":              name,
		"operator_prefixes": prefixes,
	}, bearer(testAdminToken))
	require.Equal(e.t, http.StatusCreated, res.Status, string(res.Body))
	require.NotEmpty(e.t, res.str("api_key"))
	return res.str("id"), res.str("api_key")
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	_, err := e.pool.Exec(context.Background(), sql, args...)
	require.NoError(e.t, err)
}

func (e *env) queryInt(sql string, args ...any) int {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.pool.QueryRow(context.Background(), sql, args...).Scan(&n))
	return n
}

func (e *env) queryString(sql string, args ...any) string {
	e.t.Helper()
	var s string
	require.NoError(e.t, e.pool.QueryRow(context.Background(), sql, args...).Scan(&s))
	return s
}

func withDatabase(t *testing.T, baseURL, dbName string) string {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	require.NoError(t, err)
	parsed.Path = "/" + dbName
	return parsed.String()
}

func randomSuffix() string {
	var buf [6]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}
