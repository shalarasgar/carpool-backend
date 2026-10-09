package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shalarasgar/carpool-backend/internal/service"
)

func TestLive(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	Health{}.Live(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("body = %v", body)
	}
}

func TestReadyUp(t *testing.T) {
	t.Parallel()

	h := Health{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Checker: fakeReady{
			status: service.Status{Database: "up", SMSGatewayHealthy: false},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.Ready(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
	}
	var body readyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body.Status != "ok" || body.Database != "up" || body.SMSGatewayHealthy {
		t.Fatalf("body = %+v", body)
	}
}

func TestReadyDown(t *testing.T) {
	t.Parallel()

	h := Health{
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Checker: fakeReady{err: errors.New("db down")},
	}
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.Ready(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
	var body readyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body.Status != "unavailable" || body.Database != "down" {
		t.Fatalf("body = %+v", body)
	}
}

type fakeReady struct {
	status service.Status
	err    error
}

func (f fakeReady) CheckReady(context.Context) (service.Status, error) {
	return f.status, f.err
}
