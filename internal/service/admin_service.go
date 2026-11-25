package service

import (
	"context"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/ride"
)

// AdminStore is the read side of the admin panel.
type AdminStore interface {
	Overview(ctx context.Context, since time.Time) (ride.Overview, error)
	AdminUsers(ctx context.Context, q ride.UserQuery, page, limit int) ([]ride.AdminUser, bool, error)
	AdminUser(ctx context.Context, id string) (ride.AdminUserDetail, error)
	DriverTrips(ctx context.Context, driverID, scope string, page, limit int) ([]ride.Trip, bool, error)
	PassengerBookings(ctx context.Context, passengerID, scope string, page, limit int) ([]ride.Booking, bool, error)
	UserReviews(ctx context.Context, userID string, page, limit int) ([]ride.Review, bool, error)
}

// Admin serves the admin panel: the dashboard, account search, and account cards. Actions
// on accounts and reports stay in Moderation and Profiles.
type Admin struct {
	store AdminStore
	loc   *time.Location
	now   func() time.Time
}

// NewAdmin returns the admin service. loc sets where the dashboard's day starts; nil means
// UTC.
func NewAdmin(store AdminStore, loc *time.Location) *Admin {
	if loc == nil {
		loc = time.UTC
	}
	return &Admin{store: store, loc: loc, now: time.Now}
}

// Overview returns the dashboard figures, with "new today" counted from local midnight.
func (a *Admin) Overview(ctx context.Context) (ride.Overview, error) {
	since := ride.DayStart(a.now(), a.loc)
	o, err := a.store.Overview(ctx, since)
	if err != nil {
		return ride.Overview{}, err
	}
	o.Since = since
	return o, nil
}

// Users searches accounts by name or phone.
func (a *Admin) Users(ctx context.Context, q ride.UserQuery, page, limit int) ([]ride.AdminUser, bool, error) {
	if err := q.Normalize(); err != nil {
		return nil, false, err
	}
	page, limit = pageBounds(page, limit)
	return a.store.AdminUsers(ctx, q, page, limit)
}

// User returns the moderator's card of an account.
func (a *Admin) User(ctx context.Context, id string) (ride.AdminUserDetail, error) {
	if !ride.ValidID(id) {
		return ride.AdminUserDetail{}, ride.ErrNotFound
	}
	return a.store.AdminUser(ctx, id)
}

// UserTrips lists the trips an account drives, as that driver sees them. scope is
// "upcoming" or "history".
func (a *Admin) UserTrips(ctx context.Context, id, scope string, page, limit int) ([]ride.Trip, bool, error) {
	if !ride.ValidID(id) {
		return nil, false, ride.ErrNotFound
	}
	page, limit = pageBounds(page, limit)
	return a.store.DriverTrips(ctx, id, scope, page, limit)
}

// UserBookings lists an account's bookings as a passenger, as that passenger sees them.
// scope is "upcoming" or "history".
func (a *Admin) UserBookings(ctx context.Context, id, scope string, page, limit int) ([]ride.Booking, bool, error) {
	if !ride.ValidID(id) {
		return nil, false, ride.ErrNotFound
	}
	page, limit = pageBounds(page, limit)
	return a.store.PassengerBookings(ctx, id, scope, page, limit)
}

// UserReviews lists the reviews an account received.
func (a *Admin) UserReviews(ctx context.Context, id string, page, limit int) ([]ride.Review, bool, error) {
	if !ride.ValidID(id) {
		return nil, false, ride.ErrNotFound
	}
	page, limit = pageBounds(page, limit)
	return a.store.UserReviews(ctx, id, page, limit)
}
