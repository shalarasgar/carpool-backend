package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shalarasgar/carpool-backend/internal/store"
)

// Block is one user the caller has blocked. The phone number is never set.
type Block struct {
	UserID    string
	Name      string
	PhotoURL  *string
	CreatedAt time.Time
}

// BlockPage is a page of the caller's blocks.
type BlockPage struct {
	Blocks   []Block
	Page     int
	PageSize int
	HasMore  bool
}

// Blocks stores user-to-user blocks.
//
// A block hides each user's trips from the other's search and rejects a new
// booking or the approval of a still-pending one. An approved booking is left
// in place: seats and status do not change.
type Blocks struct {
	pool   *pgxpool.Pool
	q      *store.Queries
	logger *slog.Logger
}

// NewBlocks wires the block service.
func NewBlocks(pool *pgxpool.Pool, logger *slog.Logger) *Blocks {
	if logger == nil {
		logger = slog.Default()
	}
	return &Blocks{pool: pool, q: store.New(pool), logger: logger}
}

// Create blocks the other user. created is false when the row already existed.
// Blocking yourself is FORBIDDEN. A missing or deleted account is NOT_FOUND.
func (s *Blocks) Create(ctx context.Context, blockerID, blockedID string) (Block, bool, error) {
	blocker, err := parseUserID(blockerID)
	if err != nil {
		return Block{}, false, err
	}
	blocked, err := parseUUID(blockedID)
	if err != nil {
		return Block{}, false, ErrNotFound
	}
	if sameID(blocker.String(), blocked.String()) {
		return Block{}, false, ErrForbidden
	}
	var out Block
	var created bool
	err = store.RunInTx(ctx, s.pool, func(q *store.Queries) error {
		if err := requireActiveUser(ctx, q, blocker); err != nil {
			return err
		}
		target, err := q.GetUserByID(ctx, blocked)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && target.DeletedAt.Valid) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("load user: %w", err)
		}
		n, err := q.InsertUserBlock(ctx, store.InsertUserBlockParams{
			BlockerID: blocker,
			BlockedID: blocked,
		})
		if err != nil {
			return fmt.Errorf("insert block: %w", err)
		}
		row, err := q.GetUserBlock(ctx, store.GetUserBlockParams{
			BlockerID: blocker,
			BlockedID: blocked,
		})
		if err != nil {
			return fmt.Errorf("load block: %w", err)
		}
		created = n > 0
		out = Block{
			UserID:    blocked.String(),
			Name:      target.Name,
			PhotoURL:  textPtr(target.PhotoUrl),
			CreatedAt: row.CreatedAt.Time,
		}
		return nil
	})
	if err != nil {
		return Block{}, false, err
	}
	s.logger.Info("user blocked", "user_id", blockerID, "blocked_id", blockedID, "created", created)
	return out, created, nil
}

// Delete removes the caller's block. A missing row is still success.
func (s *Blocks) Delete(ctx context.Context, blockerID, blockedID string) error {
	blocker, err := parseUserID(blockerID)
	if err != nil {
		return err
	}
	blocked, err := parseUUID(blockedID)
	if err != nil {
		return ErrNotFound
	}
	if sameID(blocker.String(), blocked.String()) {
		return ErrForbidden
	}
	err = store.RunInTx(ctx, s.pool, func(q *store.Queries) error {
		if err := requireActiveUser(ctx, q, blocker); err != nil {
			return err
		}
		if _, err := q.DeleteUserBlock(ctx, store.DeleteUserBlockParams{
			BlockerID: blocker,
			BlockedID: blocked,
		}); err != nil {
			return fmt.Errorf("delete block: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.logger.Info("user unblocked", "user_id", blockerID, "blocked_id", blockedID)
	return nil
}

// List returns the users the caller has blocked, newest first.
func (s *Blocks) List(ctx context.Context, blockerID string, q PageQuery) (BlockPage, error) {
	blocker, err := parseUserID(blockerID)
	if err != nil {
		return BlockPage{}, err
	}
	page, size, limit, offset, err := parsePage(q.Page, q.PageSize)
	if err != nil {
		return BlockPage{}, err
	}
	rows, err := s.q.ListBlocksByBlocker(ctx, store.ListBlocksByBlockerParams{
		BlockerID:  blocker,
		PageOffset: int32(offset),
		PageLimit:  int32(limit),
	})
	if err != nil {
		return BlockPage{}, fmt.Errorf("list blocks: %w", err)
	}
	trimmed, more := trimPage(rows, size)
	out := make([]Block, 0, len(trimmed))
	for _, row := range trimmed {
		out = append(out, Block{
			UserID:    row.BlockedID.String(),
			Name:      row.Name,
			PhotoURL:  textPtr(row.PhotoUrl),
			CreatedAt: row.CreatedAt.Time,
		})
	}
	return BlockPage{Blocks: out, Page: page, PageSize: size, HasMore: more}, nil
}
