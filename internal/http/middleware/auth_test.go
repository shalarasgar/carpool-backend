package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/service"
	"github.com/stretchr/testify/require"
)

type fakeVerifier struct{}

func (fakeVerifier) VerifyAccessToken(token string) (string, string, error) {
	if token == "good" {
		return "user-1", "device-1", nil
	}
	return "", "", service.ErrUnauthorized
}

func (fakeVerifier) AccountStatus(context.Context, string) error { return nil }

type blockingVerifier struct{ fakeVerifier }

func (blockingVerifier) AccountStatus(context.Context, string) error {
	return service.ErrUserBlocked
}

type statusErrVerifier struct{ fakeVerifier }

func (statusErrVerifier) AccountStatus(context.Context, string) error {
	return errors.New("db down")
}

type fakeGateways struct{ dbErr error }

func (f fakeGateways) Authenticate(_ context.Context, key string) (service.GatewayIdentity, error) {
	if f.dbErr != nil {
		return service.GatewayIdentity{}, f.dbErr
	}
	if key == "gw_good" {
		return service.GatewayIdentity{ID: pgtype.UUID{Valid: true}, Name: "phone"}, nil
	}
	return service.GatewayIdentity{}, service.ErrUnauthorized
}

func run(t *testing.T, mw func(http.Handler) http.Handler, authz string, inner http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	rec := httptest.NewRecorder()
	Locale(mw(inner)).ServeHTTP(rec, req)
	return rec
}

func code(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env httperr.Envelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	return env.Error.Code
}

func TestRequireUser(t *testing.T) {
	t.Parallel()
	mw := RequireUser(fakeVerifier{})

	var gotUser, gotDevice string
	ok := run(t, mw, "Bearer good", func(w http.ResponseWriter, r *http.Request) {
		gotUser = UserIDFrom(r.Context())
		gotDevice = DeviceIDFrom(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	require.Equal(t, http.StatusNoContent, ok.Code)
	require.Equal(t, "user-1", gotUser)
	require.Equal(t, "device-1", gotDevice)

	for _, header := range []string{"", "Bearer", "Bearer ", "Bearer bad", "Basic good", "good"} {
		rec := run(t, mw, header, func(w http.ResponseWriter, r *http.Request) { t.Fatal("handler must not run") })
		require.Equal(t, http.StatusUnauthorized, rec.Code, "header %q", header)
		require.Equal(t, "UNAUTHORIZED", code(t, rec))
	}

	lower := run(t, mw, "bearer good", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	require.Equal(t, http.StatusNoContent, lower.Code, "scheme is case-insensitive")
}

func TestRequireUserRejectsBlockedAndUnavailableAccounts(t *testing.T) {
	t.Parallel()

	blocked := run(t, RequireUser(blockingVerifier{}), "Bearer good", func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run")
	})
	require.Equal(t, http.StatusForbidden, blocked.Code)
	require.Equal(t, "USER_BLOCKED", code(t, blocked))

	down := run(t, RequireUser(statusErrVerifier{}), "Bearer good", func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run")
	})
	require.Equal(t, http.StatusInternalServerError, down.Code)
	require.Equal(t, "INTERNAL", code(t, down))
	require.NotContains(t, down.Body.String(), "db down")
}

func TestRequireGateway(t *testing.T) {
	t.Parallel()

	var name string
	rec := run(t, RequireGateway(fakeGateways{}), "Bearer gw_good", func(w http.ResponseWriter, r *http.Request) {
		gw, ok := GatewayFrom(r.Context())
		require.True(t, ok)
		name = gw.Name
	})
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "phone", name)

	rec = run(t, RequireGateway(fakeGateways{}), "Bearer gw_bad", nil)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "UNAUTHORIZED", code(t, rec))

	rec = run(t, RequireGateway(fakeGateways{dbErr: errors.New("db down")}), "Bearer gw_good", nil)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, "INTERNAL", code(t, rec))
}

func TestRequireAdmin(t *testing.T) {
	t.Parallel()
	mw := RequireAdmin("admin-token-value-long")

	rec := run(t, mw, "Bearer admin-token-value-long", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusCreated) })
	require.Equal(t, http.StatusCreated, rec.Code)

	for _, header := range []string{"", "Bearer admin-token-value-lon", "Bearer admin-token-value-longer", "Bearer x"} {
		rec := run(t, mw, header, func(w http.ResponseWriter, r *http.Request) { t.Fatal("handler must not run") })
		require.Equal(t, http.StatusForbidden, rec.Code, "header %q", header)
		require.Equal(t, "FORBIDDEN", code(t, rec))
	}

	empty := RequireAdmin("")
	rec = run(t, empty, "Bearer ", func(w http.ResponseWriter, r *http.Request) { t.Fatal("handler must not run") })
	require.Equal(t, http.StatusForbidden, rec.Code, "an empty admin token never matches")
}

func TestLocaleMiddleware(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en;q=0.8")
	rec := httptest.NewRecorder()
	Locale(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httperr.WriteCode(w, r, http.StatusNotFound, "NOT_FOUND")
	})).ServeHTTP(rec, req)

	require.Equal(t, "ru", rec.Header().Get("Content-Language"))
	var env httperr.Envelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	require.Equal(t, "NOT_FOUND", env.Error.Code)
	require.Equal(t, "Не найдено.", env.Error.Message)
}
