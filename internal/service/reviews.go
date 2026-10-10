package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shalarasgar/carpool-backend/internal/store"
)

const defaultReviewWindow = 14 * 24 * time.Hour

// ReviewConfig tunes how long after departure a participant may leave a review.
type ReviewConfig struct {
	Window time.Duration
}

// ReviewAuthor is the person who wrote a review. The phone number is never set.
type ReviewAuthor struct {
	ID       string
	Name     string
	PhotoURL *string
}

// Review is one driver's or passenger's rating of the other.
type Review struct {
	ID        string
	TripID    string
	From      ReviewAuthor
	ToUserID  string
	Rating    int
	Comment   *string
	CreatedAt time.Time
}

// ReviewPage is a page of reviews about one user.
type ReviewPage struct {
	Reviews  []Review
	Page     int
	PageSize int
	HasMore  bool
}

// ReviewInput is POST /v1/trips/{id}/reviews.
type ReviewInput struct {
	ToUserID string
	Rating   int
	Comment  *string
}

// Reviews stores ratings for completed trips.
type Reviews struct {
	pool   *pgxpool.Pool
	q      *store.Queries
	window time.Duration
	logger *slog.Logger
	now    func() time.Time
}

// NewReviews wires the review service. A non-positive window uses 14 days.
func NewReviews(pool *pgxpool.Pool, cfg ReviewConfig, logger *slog.Logger) *Reviews {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Window <= 0 {
		cfg.Window = defaultReviewWindow
	}
	return &Reviews{
		pool: pool, q: store.New(pool), window: cfg.Window, logger: logger, now: time.Now,
	}
}

// Create stores one review and updates the recipient's rating average in the
// same transaction. The reviewed user is locked before the trip, and the trip
// before the approved booking. When the reviewer is locked as well, the two
// user rows are locked in id order so mutual reviews cannot deadlock.
//
// Only a completed trip, only the driver and a passenger with an approved
// booking, only once per direction, and only until Window after departs_at.
func (s *Reviews) Create(ctx context.Context, fromID, tripID string, in ReviewInput) (Review, error) {
	from, err := parseUserID(fromID)
	if err != nil {
		return Review{}, err
	}
	tripUUID, err := parseUUID(tripID)
	if err != nil {
		return Review{}, ErrTripNotFound
	}
	to, err := parseFieldUUID(in.ToUserID, "to_user_id")
	if err != nil {
		return Review{}, err
	}
	if in.Rating < 1 || in.Rating > 5 {
		return Review{}, validationError(map[string]string{"rating": "min"})
	}
	comment, err := optionalNote(in.Comment, "comment")
	if err != nil {
		return Review{}, err
	}
	if sameID(from.String(), to.String()) {
		return Review{}, ErrForbidden
	}

	var created store.Review
	var author store.User
	err = store.RunInTx(ctx, s.pool, func(q *store.Queries) error {
		locked, err := lockReviewUsers(ctx, q, from, to)
		if err != nil {
			return err
		}
		author = locked[from.String()]
		if author.DeletedAt.Valid {
			return ErrUnauthorized
		}
		if author.IsBlocked {
			return ErrUserBlocked
		}

		trip, err := q.GetTripForUpdate(ctx, tripUUID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTripNotFound
		}
		if err != nil {
			return fmt.Errorf("lock trip: %w", err)
		}
		if trip.Status != store.TripStatusCompleted {
			return ErrInvalidState
		}
		if s.clock().After(trip.DepartsAt.Time.Add(s.window)) {
			return ErrReviewWindowClosed
		}

		passenger, ok := reviewPassenger(from, to, trip.DriverID)
		if !ok {
			return ErrForbidden
		}
		if _, err := q.GetApprovedBookingForPassenger(ctx, store.GetApprovedBookingForPassengerParams{
			TripID:      trip.ID,
			PassengerID: passenger,
		}); errors.Is(err, pgx.ErrNoRows) {
			return ErrForbidden
		} else if err != nil {
			return fmt.Errorf("lock booking: %w", err)
		}

		created, err = q.CreateReview(ctx, store.CreateReviewParams{
			TripID:     trip.ID,
			FromUserID: from,
			ToUserID:   to,
			Rating:     int16(in.Rating),
			Comment:    comment,
		})
		if isUniqueViolation(err) {
			return ErrReviewExists
		}
		if err != nil {
			return fmt.Errorf("create review: %w", err)
		}
		if _, err := q.AddUserRating(ctx, to); err != nil {
			return fmt.Errorf("update rating: %w", err)
		}
		return nil
	})
	if err != nil {
		return Review{}, err
	}
	s.logger.Info("review created", "user_id", fromID, "trip_id", tripID, "to_user_id", to.String())
	return reviewFrom(created, author.Name, author.PhotoUrl), nil
}

// List returns reviews about the user, newest first. A missing or deleted
// user is NOT_FOUND. Reviewer phone numbers are not read.
func (s *Reviews) List(ctx context.Context, userID string, q PageQuery) (ReviewPage, error) {
	id, err := parseUUID(userID)
	if err != nil {
		return ReviewPage{}, ErrNotFound
	}
	user, err := s.q.GetUserByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && user.DeletedAt.Valid) {
		return ReviewPage{}, ErrNotFound
	}
	if err != nil {
		return ReviewPage{}, fmt.Errorf("load user: %w", err)
	}
	page, size, limit, offset, err := parsePage(q.Page, q.PageSize)
	if err != nil {
		return ReviewPage{}, err
	}
	rows, err := s.q.ListReviewsForUser(ctx, store.ListReviewsForUserParams{
		ToUserID:   id,
		PageOffset: int32(offset),
		PageLimit:  int32(limit),
	})
	if err != nil {
		return ReviewPage{}, fmt.Errorf("list reviews: %w", err)
	}
	trimmed, more := trimPage(rows, size)
	out := make([]Review, 0, len(trimmed))
	for _, row := range trimmed {
		out = append(out, Review{
			ID:       row.ID.String(),
			TripID:   row.TripID.String(),
			ToUserID: row.ToUserID.String(),
			From: ReviewAuthor{
				ID:       row.FromUserID.String(),
				Name:     row.FromName,
				PhotoURL: textPtr(row.FromPhotoUrl),
			},
			Rating:    int(row.Rating),
			Comment:   textPtr(row.Comment),
			CreatedAt: row.CreatedAt.Time,
		})
	}
	return ReviewPage{Reviews: out, Page: page, PageSize: size, HasMore: more}, nil
}

func (s *Reviews) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

// lockReviewUsers locks both participants in id order, then the caller locks
// the trip and the booking.
func lockReviewUsers(ctx context.Context, q *store.Queries, from, to pgtype.UUID) (map[string]store.User, error) {
	order := []pgtype.UUID{from, to}
	if order[0].String() > order[1].String() {
		order[0], order[1] = order[1], order[0]
	}
	locked := make(map[string]store.User, 2)
	for _, id := range order {
		row, err := q.GetUserByIDForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			if sameID(id.String(), from.String()) {
				return nil, ErrUnauthorized
			}
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("lock user: %w", err)
		}
		locked[id.String()] = row
	}
	return locked, nil
}

// reviewPassenger returns the passenger id when the pair is driver plus
// passenger. Passenger-to-passenger and anyone outside the trip are refused.
func reviewPassenger(from, to, driver pgtype.UUID) (pgtype.UUID, bool) {
	fromDriver := sameID(from.String(), driver.String())
	toDriver := sameID(to.String(), driver.String())
	switch {
	case fromDriver && !toDriver:
		return to, true
	case toDriver && !fromDriver:
		return from, true
	default:
		return pgtype.UUID{}, false
	}
}

func reviewFrom(row store.Review, name string, photo pgtype.Text) Review {
	return Review{
		ID:     row.ID.String(),
		TripID: row.TripID.String(),
		From: ReviewAuthor{
			ID:       row.FromUserID.String(),
			Name:     name,
			PhotoURL: textPtr(photo),
		},
		ToUserID:  row.ToUserID.String(),
		Rating:    int(row.Rating),
		Comment:   textPtr(row.Comment),
		CreatedAt: row.CreatedAt.Time,
	}
}
