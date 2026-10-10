package handlers

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/storage"
)

// Media serves uploaded files. Only generated photo names are accepted.
type Media struct {
	Store storage.Store
}

// Photo handles GET /v1/media/photos/{name}.
func (h Media) Photo(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		httperr.WriteCode(w, r, http.StatusNotFound, "NOT_FOUND")
		return
	}
	name := chi.URLParam(r, "name")
	f, err := h.Store.Open(name)
	if err != nil {
		httperr.WriteCode(w, r, http.StatusNotFound, "NOT_FOUND")
		return
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", photoContentType(name))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Content-Disposition", "inline")
	_, _ = io.Copy(w, f)
}

func photoContentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return "image/jpeg"
	}
}
