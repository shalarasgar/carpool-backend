package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shalarasgar/carpool-backend/internal/notify"
	"github.com/shalarasgar/carpool-backend/internal/storage"
	"github.com/shalarasgar/carpool-backend/internal/store"
)

// UserConfig tunes profile photos and account anonymization.
type UserConfig struct {
	PhotoMaxBytes int64
	AllowedTypes  []string
	PhoneHashKey  string
}

// PublicProfile is what other users may see. It never includes a phone number.
type PublicProfile struct {
	ID             string
	Name           string
	PhotoURL       *string
	RatingAvg      float64
	RatingCount    int
	Vehicle        *Vehicle
	CompletedTrips int
}

// ProfileUpdate is a partial update of the caller's own profile.
type ProfileUpdate struct {
	Name   *string
	Locale *string
}

// Users implements the caller's profile and public profiles.
type Users struct {
	pool     *pgxpool.Pool
	q        *store.Queries
	photos   storage.Store
	cfg      UserConfig
	allowed  map[string]struct{}
	notifier notify.Notifier
	logger   *slog.Logger
}

// NewUsers wires the profile service. photos may not be nil. notifier may be
// nil, which discards the events account deletion would otherwise store.
func NewUsers(pool *pgxpool.Pool, photos storage.Store, cfg UserConfig, notifier notify.Notifier, logger *slog.Logger) *Users {
	if logger == nil {
		logger = slog.Default()
	}
	if notifier == nil {
		notifier = notify.Nop{Logger: logger}
	}
	allowed := make(map[string]struct{}, len(cfg.AllowedTypes))
	for _, kind := range cfg.AllowedTypes {
		allowed[kind] = struct{}{}
	}
	return &Users{
		pool: pool, q: store.New(pool), photos: photos, cfg: cfg, allowed: allowed,
		notifier: notifier, logger: logger,
	}
}

// Get returns the caller's own profile, including their phone.
func (s *Users) Get(ctx context.Context, userID string) (User, error) {
	user, err := s.loadActive(ctx, s.q, userID)
	if err != nil {
		return User{}, err
	}
	return toUser(user), nil
}

// Update changes name and/or locale. At least one field is required.
func (s *Users) Update(ctx context.Context, userID string, in ProfileUpdate) (User, error) {
	if in.Name == nil && in.Locale == nil {
		return User{}, validationError(map[string]string{"body": "empty"})
	}
	var name, locale string
	if in.Name != nil {
		normalized, err := NormalizePersonName(*in.Name, 80)
		if err != nil {
			return User{}, err
		}
		name = normalized
	}
	if in.Locale != nil {
		normalized, err := NormalizeLocale(*in.Locale)
		if err != nil {
			return User{}, err
		}
		locale = normalized
	}
	uid, err := parseUserID(userID)
	if err != nil {
		return User{}, err
	}
	updated, err := s.q.UpdateUserProfile(ctx, store.UpdateUserProfileParams{
		UpdateName:   in.Name != nil,
		Name:         name,
		UpdateLocale: in.Locale != nil,
		Locale:       locale,
		ID:           uid,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUnauthorized
	}
	if err != nil {
		return User{}, fmt.Errorf("update profile: %w", err)
	}
	s.logger.Info("profile updated", "user_id", userID)
	return toUser(updated), nil
}

// SetPhoto stores a normalized image and points the profile at it.
// The previous file is removed after the database update commits.
func (s *Users) SetPhoto(ctx context.Context, userID string, data []byte) (User, error) {
	body, contentType, ext, err := NormalizePhoto(data, s.allowed, s.cfg.PhotoMaxBytes)
	if err != nil {
		return User{}, err
	}
	uid, err := parseUserID(userID)
	if err != nil {
		return User{}, err
	}
	current, err := s.loadActive(ctx, s.q, userID)
	if err != nil {
		return User{}, err
	}
	key, err := randomPhotoKey(ext)
	if err != nil {
		return User{}, err
	}
	if err := s.photos.Put(ctx, key, contentType, body); err != nil {
		return User{}, fmt.Errorf("store photo: %w", err)
	}
	updated, err := s.q.SetUserPhoto(ctx, store.SetUserPhotoParams{
		ID:       uid,
		PhotoUrl: pgtype.Text{String: s.photos.PublicPath(key), Valid: true},
	})
	if err != nil {
		_ = s.photos.Delete(ctx, key)
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrUnauthorized
		}
		return User{}, fmt.Errorf("save photo url: %w", err)
	}
	if current.PhotoUrl.Valid {
		s.deleteStoredPhoto(ctx, userID, current.PhotoUrl.String)
	}
	s.logger.Info("photo updated", "user_id", userID)
	return toUser(updated), nil
}

// Anonymize clears the profile, revokes every refresh token and replaces the
// phone with a one-way tombstone so the same number can register a new user.
// In the same transaction it cancels the user's active trips and the bookings
// on them, cancels their pending and approved bookings as a passenger
// (restoring seats on active or full trips), and deletes their vehicles.
func (s *Users) Anonymize(ctx context.Context, userID string) error {
	uid, err := parseUserID(userID)
	if err != nil {
		return err
	}
	var oldPhoto string
	var events []notify.Event
	err = store.RunInTx(ctx, s.pool, func(q *store.Queries) error {
		user, err := q.GetUserByIDForUpdate(ctx, uid)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnauthorized
		}
		if err != nil {
			return fmt.Errorf("load user: %w", err)
		}
		if user.DeletedAt.Valid {
			return ErrUnauthorized
		}
		if user.IsBlocked {
			return ErrUserBlocked
		}
		events, err = ReleaseUserActivity(ctx, q, uid)
		if err != nil {
			return err
		}
		n, err := q.AnonymizeUser(ctx, store.AnonymizeUserParams{
			ID:    uid,
			Phone: AnonymizedPhone(s.cfg.PhoneHashKey, user.Phone, user.ID.String()),
		})
		if err != nil {
			return fmt.Errorf("anonymize user: %w", err)
		}
		if n == 0 {
			return ErrUnauthorized
		}
		if _, err := q.RevokeAllUserRefreshTokens(ctx, uid); err != nil {
			return fmt.Errorf("revoke refresh tokens: %w", err)
		}
		if user.PhotoUrl.Valid {
			oldPhoto = user.PhotoUrl.String
		}
		if err := s.notifier.Notify(store.WithQueries(ctx, q), events...); err != nil {
			return fmt.Errorf("notify: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.deleteStoredPhoto(ctx, userID, oldPhoto)
	s.logger.Info("user anonymized", "user_id", userID)
	return nil
}

// PublicProfile loads the shareable profile. The phone number is not read
// into the result. The vehicle is the one created most recently.
// CompletedTrips counts completed trips driven plus completed trips ridden
// with an approved booking.
func (s *Users) PublicProfile(ctx context.Context, id string) (PublicProfile, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return PublicProfile{}, ErrNotFound
	}
	user, err := s.q.GetUserByID(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return PublicProfile{}, ErrNotFound
	}
	if err != nil {
		return PublicProfile{}, fmt.Errorf("load user: %w", err)
	}
	if user.DeletedAt.Valid {
		return PublicProfile{}, ErrNotFound
	}
	driven, err := s.q.CountCompletedTripsAsDriver(ctx, uid)
	if err != nil {
		return PublicProfile{}, fmt.Errorf("count driven trips: %w", err)
	}
	ridden, err := s.q.CountCompletedTripsAsPassenger(ctx, uid)
	if err != nil {
		return PublicProfile{}, fmt.Errorf("count ridden trips: %w", err)
	}
	profile := PublicProfile{
		ID:             user.ID.String(),
		Name:           user.Name,
		RatingCount:    int(user.RatingCount),
		CompletedTrips: int(driven + ridden),
	}
	if user.PhotoUrl.Valid {
		v := user.PhotoUrl.String
		profile.PhotoURL = &v
	}
	if f, err := user.RatingAvg.Float64Value(); err == nil && f.Valid {
		profile.RatingAvg = f.Float64
	}
	vehicle, err := s.q.GetLatestVehicleByUser(ctx, uid)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return PublicProfile{}, fmt.Errorf("load vehicle: %w", err)
	}
	if err == nil {
		v := vehicleFromStore(vehicle)
		profile.Vehicle = &v
	}
	return profile, nil
}

// AnonymizedPhone is the tombstone stored in users.phone after deletion:
// anon:<hex hmac-sha256>:<user id>. It is unique per account, does not match
// E.164, and cannot be reversed to the original number.
func AnonymizedPhone(secret, phone, userID string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(phone))
	return "anon:" + hex.EncodeToString(mac.Sum(nil)) + ":" + strings.ToLower(userID)
}

func (s *Users) loadActive(ctx context.Context, q *store.Queries, userID string) (store.User, error) {
	uid, err := parseUserID(userID)
	if err != nil {
		return store.User{}, err
	}
	user, err := q.GetUserByID(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.User{}, ErrUnauthorized
	}
	if err != nil {
		return store.User{}, fmt.Errorf("load user: %w", err)
	}
	if user.DeletedAt.Valid {
		return store.User{}, ErrUnauthorized
	}
	if user.IsBlocked {
		return store.User{}, ErrUserBlocked
	}
	return user, nil
}

func (s *Users) deleteStoredPhoto(ctx context.Context, userID, publicPath string) {
	if publicPath == "" || s.photos == nil {
		return
	}
	key, err := s.photos.Key(publicPath)
	if err != nil {
		s.logger.Warn("photo path is not a stored key", "user_id", userID)
		return
	}
	if err := s.photos.Delete(ctx, key); err != nil {
		s.logger.Warn("delete photo file", "user_id", userID, "err", err)
	}
}
