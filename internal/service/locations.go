package service

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shalarasgar/carpool-backend/internal/i18n"
	"github.com/shalarasgar/carpool-backend/internal/store"
)

// Location is one row of the location directory, with the name already
// chosen for the request locale.
type Location struct {
	ID        string
	ParentID  *string
	Type      string
	Name      string
	IsActive  bool
	SortOrder int
}

// LocationQuery is the filter for the public directory.
// ActiveOnly defaults to true at the HTTP layer. Q is matched, case-insensitively,
// against the Turkmen, Russian and English names.
type LocationQuery struct {
	Type       string
	ParentID   string
	Q          string
	ActiveOnly bool
	Locale     string
}

// Locations serves the public location directory.
type Locations struct {
	q *store.Queries
}

// NewLocations wires the location service.
func NewLocations(pool *pgxpool.Pool) *Locations {
	return &Locations{q: store.New(pool)}
}

// ParseActiveOnly interprets the active_only query value. A missing parameter
// (set == false) means true.
func ParseActiveOnly(raw string, set bool) (bool, error) {
	if !set {
		return true, nil
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	default:
		return false, validationError(map[string]string{"active_only": "bool"})
	}
}

// List returns locations matching q, ordered by sort_order then English name.
// The table is small (welayats, cities, later etraps), so the filter runs in
// the service. That keeps Unicode case-folding independent of the database locale.
func (s *Locations) List(ctx context.Context, q LocationQuery) ([]Location, error) {
	normalized, err := q.normalized()
	if err != nil {
		return nil, err
	}
	rows, err := s.q.ListLocations(ctx)
	if err != nil {
		return nil, fmt.Errorf("list locations: %w", err)
	}
	records := make([]locationRecord, 0, len(rows))
	for _, row := range rows {
		rec := locationRecord{
			ID:        row.ID.String(),
			Type:      string(row.Type),
			NameTK:    row.NameTk,
			NameRU:    row.NameRu,
			NameEN:    row.NameEn,
			IsActive:  row.IsActive,
			SortOrder: int(row.SortOrder),
		}
		if row.ParentID.Valid {
			parent := row.ParentID.String()
			rec.ParentID = parent
			rec.HasParent = true
		}
		records = append(records, rec)
	}
	return filterLocations(records, normalized), nil
}

func (q LocationQuery) normalized() (LocationQuery, error) {
	switch q.Type {
	case "", "welayat", "city", "etrap":
	default:
		return LocationQuery{}, validationError(map[string]string{"type": "oneof"})
	}
	if q.ParentID != "" {
		parent, ok := canonicalUUID(q.ParentID)
		if !ok {
			return LocationQuery{}, validationError(map[string]string{"parent_id": "uuid"})
		}
		q.ParentID = parent
	}
	q.Q = strings.TrimSpace(q.Q)
	if utf8.RuneCountInString(q.Q) > 64 {
		return LocationQuery{}, validationError(map[string]string{"q": "max"})
	}
	if !i18n.Supported(q.Locale) {
		q.Locale = i18n.DefaultLocale
	}
	return q, nil
}

type locationRecord struct {
	ID        string
	ParentID  string
	HasParent bool
	Type      string
	NameTK    string
	NameRU    string
	NameEN    string
	IsActive  bool
	SortOrder int
}

func filterLocations(rows []locationRecord, q LocationQuery) []Location {
	out := make([]Location, 0)
	needle := strings.ToLower(q.Q)
	for _, row := range rows {
		if q.Type != "" && row.Type != q.Type {
			continue
		}
		if q.ParentID != "" && (!row.HasParent || row.ParentID != q.ParentID) {
			continue
		}
		if q.ActiveOnly && !row.IsActive {
			continue
		}
		if needle != "" && !nameMatches(row, needle) {
			continue
		}
		item := Location{
			ID:        row.ID,
			Type:      row.Type,
			Name:      localizedName(row, q.Locale),
			IsActive:  row.IsActive,
			SortOrder: row.SortOrder,
		}
		if row.HasParent {
			parent := row.ParentID
			item.ParentID = &parent
		}
		out = append(out, item)
	}
	return out
}

func nameMatches(row locationRecord, needle string) bool {
	return strings.Contains(strings.ToLower(row.NameTK), needle) ||
		strings.Contains(strings.ToLower(row.NameRU), needle) ||
		strings.Contains(strings.ToLower(row.NameEN), needle)
}

func localizedName(row locationRecord, locale string) string {
	switch locale {
	case i18n.LocaleRU:
		return row.NameRU
	case i18n.LocaleEN:
		return row.NameEN
	default:
		return row.NameTK
	}
}
