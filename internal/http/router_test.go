package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

func TestHealthzRoute(t *testing.T) {
	t.Parallel()

	router := NewRouter(Deps{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Health: service.NewHealth(readyStore{}, nil),
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-ID", "route-test")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("X-Request-ID") != "route-test" {
		t.Fatalf("request id = %q", rec.Header().Get("X-Request-ID"))
	}
}

func TestNotFoundEnvelope(t *testing.T) {
	t.Parallel()

	router := NewRouter(Deps{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Health: service.NewHealth(readyStore{}, nil),
	})
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	var body httperr.Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body.Error.Code != "NOT_FOUND" {
		t.Fatalf("code = %q", body.Error.Code)
	}
}

type readyStore struct{}

func (readyStore) Ping(context.Context) error { return nil }

func (readyStore) HasHealthySMSGateway(context.Context, time.Time) (bool, error) {
	return false, nil
}
