package handlers

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/shalarasgar/carpool-backend/internal/http/dto"
	"github.com/shalarasgar/carpool-backend/internal/i18n"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

// Locations serves the public directory.
type Locations struct {
	Logger  *slog.Logger
	Service *service.Locations
}

// List handles GET /v1/locations.
func (h Locations) List(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	active, err := service.ParseActiveOnly(query.Get("active_only"), query.Has("active_only"))
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	rows, err := h.Service.List(r.Context(), service.LocationQuery{
		Type:       query.Get("type"),
		ParentID:   query.Get("parent_id"),
		Q:          query.Get("q"),
		ActiveOnly: active,
		Locale:     i18n.FromContext(r.Context()),
	})
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	body := dto.LocationList{Locations: make([]dto.Location, 0, len(rows))}
	for _, row := range rows {
		body.Locations = append(body.Locations, dto.Location{
			ID:        row.ID,
			ParentID:  row.ParentID,
			Type:      row.Type,
			Name:      row.Name,
			IsActive:  row.IsActive,
			SortOrder: row.SortOrder,
		})
	}
	raw, err := json.Marshal(body)
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	sum := sha256.Sum256(raw)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Vary", "Accept-Language, Accept-Encoding")
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if acceptsGzip(r) {
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusOK)
		zw := gzip.NewWriter(w)
		_, _ = zw.Write(raw)
		_ = zw.Close()
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		enc := strings.TrimSpace(part)
		if i := strings.IndexByte(enc, ';'); i >= 0 {
			enc = strings.TrimSpace(enc[:i])
		}
		if enc == "gzip" || enc == "*" {
			return true
		}
	}
	return false
}

func etagMatches(header, etag string) bool {
	if header == "" {
		return false
	}
	want := strings.Trim(etag, `"`)
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "*" || part == etag {
			return true
		}
		part = strings.TrimPrefix(part, "W/")
		part = strings.Trim(part, `"`)
		if part == want {
			return true
		}
	}
	return false
}
