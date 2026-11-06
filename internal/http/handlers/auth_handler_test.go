package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shalarasgar/carpool-backend/internal/auth"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

func TestRequestOTPHidesTheCodeUnlessDevMode(t *testing.T) {
	t.Parallel()
	const code = "654321"
	body := `{"phone":"+993611112233","device_id":"device-1"}`

	hidden := httptest.NewRecorder()
	Auth{Service: stubOTP{code: code}, DevMode: false}.RequestOTP(hidden, httptest.NewRequest(http.MethodPost, "/v1/auth/request-otp", strings.NewReader(body)))
	if hidden.Code != http.StatusAccepted {
		t.Fatalf("status = %d", hidden.Code)
	}
	if strings.Contains(hidden.Body.String(), code) {
		t.Fatalf("response leaked the code: %s", hidden.Body.String())
	}

	shown := httptest.NewRecorder()
	Auth{Service: stubOTP{code: code}, DevMode: true}.RequestOTP(shown, httptest.NewRequest(http.MethodPost, "/v1/auth/request-otp", strings.NewReader(body)))
	var payload map[string]string
	if err := json.Unmarshal(shown.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["dev_code"] != code {
		t.Fatalf("dev payload = %+v", payload)
	}
}

type stubOTP struct{ code string }

func (s stubOTP) RequestOTP(context.Context, string, string, string, string) (string, error) {
	return s.code, nil
}
func (s stubOTP) VerifyOTP(context.Context, string, string, string, string) (service.Session, error) {
	return service.Session{}, auth.ErrInvalidCode
}
func (s stubOTP) Refresh(context.Context, string, string) (service.Session, error) {
	return service.Session{}, auth.ErrUnauthorized
}
func (s stubOTP) Logout(context.Context, string) error { return nil }
