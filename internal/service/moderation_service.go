package service

import (
	"context"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/ride"
)

// ModerationStore is the persistence of user blocks, reports, and account bans.
type ModerationStore interface {
	BlockUser(ctx context.Context, blockerID, blockedID string) error
	UnblockUser(ctx context.Context, blockerID, blockedID string) error
	BlockedUsers(ctx context.Context, blockerID string) ([]ride.UserSummary, error)
	CreateReport(ctx context.Context, in ride.ReportInput) (string, error)
	Reports(ctx context.Context, f ride.ReportFilter, page, limit int) ([]ride.Report, bool, error)
	ResolveReport(ctx context.Context, id string, now time.Time) error
	SetUserBlocked(ctx context.Context, userID string, blocked bool) error
}

// Moderation lets users block and report each other, and lets an admin work the report
// queue and ban accounts.
type Moderation struct {
	store ModerationStore
	now   func() time.Time
}

// NewModeration returns the moderation service.
func NewModeration(store ModerationStore) *Moderation {
	return &Moderation{store: store, now: time.Now}
}

// Block hides userID and targetID from each other.
func (m *Moderation) Block(ctx context.Context, userID, targetID string) error {
	if !ride.ValidID(targetID) {
		return ride.ErrNotFound
	}
	if targetID == userID {
		return &ride.FieldError{Field: "id", Reason: "self"}
	}
	return m.store.BlockUser(ctx, userID, targetID)
}

// Unblock removes the caller's block of targetID.
func (m *Moderation) Unblock(ctx context.Context, userID, targetID string) error {
	if !ride.ValidID(targetID) {
		return ride.ErrNotFound
	}
	return m.store.UnblockUser(ctx, userID, targetID)
}

// Blocked lists the users the caller blocked.
func (m *Moderation) Blocked(ctx context.Context, userID string) ([]ride.UserSummary, error) {
	return m.store.BlockedUsers(ctx, userID)
}

// Report files a report about another user and returns its id.
func (m *Moderation) Report(ctx context.Context, in ride.ReportInput) (string, error) {
	if err := in.Normalize(); err != nil {
		return "", err
	}
	return m.store.CreateReport(ctx, in)
}

// Reports lists the moderation queue. Empty filter fields match every report.
func (m *Moderation) Reports(ctx context.Context, f ride.ReportFilter, page, limit int) ([]ride.Report, bool, error) {
	if err := f.Validate(); err != nil {
		return nil, false, err
	}
	page, limit = pageBounds(page, limit)
	return m.store.Reports(ctx, f, page, limit)
}

// Resolve closes a report.
func (m *Moderation) Resolve(ctx context.Context, id string) error {
	if !ride.ValidID(id) {
		return ride.ErrNotFound
	}
	return m.store.ResolveReport(ctx, id, m.now())
}

// SetBlocked bans or reinstates an account.
func (m *Moderation) SetBlocked(ctx context.Context, userID string, blocked bool) error {
	if !ride.ValidID(userID) {
		return ride.ErrNotFound
	}
	return m.store.SetUserBlocked(ctx, userID, blocked)
}
