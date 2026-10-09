package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestCheckReadyDatabaseDown(t *testing.T) {
	t.Parallel()

	h := NewHealth(fakeStore{pingErr: errors.New("connection refused")}, discardLogger())
	status, err := h.CheckReady(context.Background())
	if err == nil {
		t.Fatal("CheckReady() error = nil")
	}
	if status.Database != "down" || status.SMSGatewayHealthy {
		t.Fatalf("status = %+v", status)
	}
}

func TestCheckReadySMSGatewayOptional(t *testing.T) {
	t.Parallel()

	h := NewHealth(fakeStore{healthy: false}, discardLogger())
	status, err := h.CheckReady(context.Background())
	if err != nil {
		t.Fatalf("CheckReady() error = %v", err)
	}
	if status.Database != "up" || status.SMSGatewayHealthy {
		t.Fatalf("status = %+v", status)
	}
}

func TestCheckReadySMSQueryFailureIsNonFatal(t *testing.T) {
	t.Parallel()

	h := NewHealth(fakeStore{queryErr: errors.New("relation does not exist")}, discardLogger())
	status, err := h.CheckReady(context.Background())
	if err != nil {
		t.Fatalf("CheckReady() error = %v", err)
	}
	if status.Database != "up" || status.SMSGatewayHealthy {
		t.Fatalf("status = %+v", status)
	}
}

func TestCheckReadyUsesHeartbeatWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	store := &captureStore{}
	h := NewHealth(store, discardLogger())
	h.now = func() time.Time { return now }

	status, err := h.CheckReady(context.Background())
	if err != nil {
		t.Fatalf("CheckReady() error = %v", err)
	}
	if !status.SMSGatewayHealthy || status.Database != "up" {
		t.Fatalf("status = %+v", status)
	}
	want := now.Add(-smsGatewayHealthyWindow)
	if !store.since.Equal(want) {
		t.Fatalf("since = %s, want %s", store.since, want)
	}
}

type fakeStore struct {
	pingErr  error
	healthy  bool
	queryErr error
}

func (f fakeStore) Ping(context.Context) error { return f.pingErr }

func (f fakeStore) HasHealthySMSGateway(context.Context, time.Time) (bool, error) {
	return f.healthy, f.queryErr
}

type captureStore struct {
	since time.Time
}

func (c *captureStore) Ping(context.Context) error { return nil }

func (c *captureStore) HasHealthySMSGateway(_ context.Context, since time.Time) (bool, error) {
	c.since = since
	return true, nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
