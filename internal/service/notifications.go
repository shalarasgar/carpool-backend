package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shalarasgar/carpool-backend/internal/i18n"
	"github.com/shalarasgar/carpool-backend/internal/store"
)

// Notification is one inbox row. Title and body are filled from the
// recipient's stored locale at read time. Payload never includes a phone.
type Notification struct {
	ID        string
	Type      string
	Title     string
	Body      string
	Payload   map[string]string
	ReadAt    *time.Time
	CreatedAt time.Time
}

// NotificationPage is a cursor page plus the caller's total unread count.
type NotificationPage struct {
	Notifications []Notification
	UnreadCount   int
	NextCursor    string
}

// MarkRead marks specific ids, or every unread row when All is true.
type MarkRead struct {
	IDs []string
	All bool
}

// Notifications is the in-app inbox.
type Notifications struct {
	q *store.Queries
}

// NewNotifications wires the inbox reader.
func NewNotifications(pool *pgxpool.Pool) *Notifications {
	return &Notifications{q: store.New(pool)}
}

// List returns the caller's notifications, newest first. Text uses
// users.locale, not the request language. limit is the page size; an empty
// cursor starts at the newest row.
func (s *Notifications) List(ctx context.Context, userID, cursor, limitRaw string) (NotificationPage, error) {
	id, err := parseUserID(userID)
	if err != nil {
		return NotificationPage{}, err
	}
	limit, err := parseLimit(limitRaw)
	if err != nil {
		return NotificationPage{}, err
	}
	cursorAt, cursorID, hasCursor, err := decodeCursor(cursor)
	if err != nil {
		return NotificationPage{}, err
	}
	user, err := s.q.GetUserByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && user.DeletedAt.Valid) {
		return NotificationPage{}, ErrUnauthorized
	}
	if err != nil {
		return NotificationPage{}, fmt.Errorf("load user: %w", err)
	}
	rows, err := s.q.ListNotifications(ctx, store.ListNotificationsParams{
		UserID:    id,
		HasCursor: hasCursor,
		CursorAt:  cursorAt,
		CursorID:  cursorID,
		PageLimit: int32(limit + 1),
	})
	if err != nil {
		return NotificationPage{}, fmt.Errorf("list notifications: %w", err)
	}
	unread, err := s.q.CountUnreadNotifications(ctx, id)
	if err != nil {
		return NotificationPage{}, fmt.Errorf("count unread: %w", err)
	}
	trimmed, more := trimPage(rows, limit)
	out := make([]Notification, 0, len(trimmed))
	for _, row := range trimmed {
		out = append(out, notificationFrom(row, user.Locale))
	}
	page := NotificationPage{Notifications: out, UnreadCount: int(unread)}
	if more && len(out) > 0 {
		last := trimmed[len(trimmed)-1]
		page.NextCursor = encodeCursor(last.CreatedAt.Time, last.ID)
	}
	return page, nil
}

// MarkRead sets read_at on the requested rows belonging to the caller.
// Ids that are not theirs are ignored. The result is the remaining unread count.
func (s *Notifications) MarkRead(ctx context.Context, userID string, in MarkRead) (int, error) {
	id, err := parseUserID(userID)
	if err != nil {
		return 0, err
	}
	if !in.All && len(in.IDs) == 0 {
		return 0, validationError(map[string]string{"ids": "required"})
	}
	if in.All {
		if _, err := s.q.MarkAllNotificationsRead(ctx, id); err != nil {
			return 0, fmt.Errorf("mark notifications read: %w", err)
		}
	} else {
		ids := make([]pgtype.UUID, 0, len(in.IDs))
		for _, raw := range in.IDs {
			parsed, err := parseFieldUUID(raw, "ids")
			if err != nil {
				return 0, err
			}
			ids = append(ids, parsed)
		}
		if _, err := s.q.MarkNotificationsRead(ctx, store.MarkNotificationsReadParams{
			UserID: id,
			Ids:    ids,
		}); err != nil {
			return 0, fmt.Errorf("mark notifications read: %w", err)
		}
	}
	unread, err := s.q.CountUnreadNotifications(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("count unread: %w", err)
	}
	return int(unread), nil
}

func notificationFrom(row store.ListNotificationsRow, locale string) Notification {
	title, body := i18n.Notification(locale, row.Type)
	out := Notification{
		ID:        row.ID.String(),
		Type:      row.Type,
		Title:     title,
		Body:      body,
		Payload:   publicPayload(row.Payload),
		CreatedAt: row.CreatedAt.Time,
	}
	if row.ReadAt.Valid {
		t := row.ReadAt.Time
		out.ReadAt = &t
	}
	return out
}

// publicPayload keeps only the ids the inbox is allowed to show.
func publicPayload(raw []byte) map[string]string {
	out := map[string]string{}
	if len(raw) == 0 {
		return out
	}
	var in map[string]string
	if err := json.Unmarshal(raw, &in); err != nil {
		return out
	}
	for _, key := range []string{"actor_id", "trip_id", "booking_id"} {
		if value := in[key]; value != "" {
			out[key] = value
		}
	}
	return out
}

func encodeCursor(t time.Time, id pgtype.UUID) string {
	raw := t.UTC().Format(time.RFC3339Nano) + "|" + id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(raw string) (pgtype.Timestamptz, pgtype.UUID, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return pgtype.Timestamptz{}, pgtype.UUID{}, false, nil
	}
	buf, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return pgtype.Timestamptz{}, pgtype.UUID{}, false, validationError(map[string]string{"cursor": "invalid"})
	}
	stamp, idRaw, ok := strings.Cut(string(buf), "|")
	if !ok {
		return pgtype.Timestamptz{}, pgtype.UUID{}, false, validationError(map[string]string{"cursor": "invalid"})
	}
	t, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return pgtype.Timestamptz{}, pgtype.UUID{}, false, validationError(map[string]string{"cursor": "invalid"})
	}
	var id pgtype.UUID
	if err := id.Scan(idRaw); err != nil || !id.Valid {
		return pgtype.Timestamptz{}, pgtype.UUID{}, false, validationError(map[string]string{"cursor": "invalid"})
	}
	return pgtype.Timestamptz{Time: t, Valid: true}, id, true, nil
}

func parseLimit(raw string) (int, error) {
	if raw == "" {
		return defaultPageSize, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxPageSize {
		return 0, validationError(map[string]string{"limit": "max"})
	}
	return n, nil
}
