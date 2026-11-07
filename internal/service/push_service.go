package service

import (
	"context"
	"strings"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/ride"
)

// PushTokenStore persists device tokens.
type PushTokenStore interface {
	SavePushToken(ctx context.Context, userID, token, platform string, now time.Time) error
	DeletePushToken(ctx context.Context, userID, token string) error
}

// PushTokens registers the signed-in user's devices for push notifications.
type PushTokens struct {
	store PushTokenStore
	now   func() time.Time
}

// NewPushTokens returns the push token service.
func NewPushTokens(store PushTokenStore) *PushTokens {
	return &PushTokens{store: store, now: time.Now}
}

const maxPushTokenLength = 4096

// Register binds token to userID. Registering again refreshes the token's age.
func (s *PushTokens) Register(ctx context.Context, userID, token, platform string) error {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > maxPushTokenLength {
		return &ride.FieldError{Field: "token", Reason: "invalid"}
	}
	if platform != "android" && platform != "ios" {
		return &ride.FieldError{Field: "platform", Reason: "unsupported"}
	}
	return s.store.SavePushToken(ctx, userID, token, platform, s.now())
}

// Unregister removes token from userID, for example on sign-out.
func (s *PushTokens) Unregister(ctx context.Context, userID, token string) error {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > maxPushTokenLength {
		return &ride.FieldError{Field: "token", Reason: "invalid"}
	}
	return s.store.DeletePushToken(ctx, userID, token)
}
