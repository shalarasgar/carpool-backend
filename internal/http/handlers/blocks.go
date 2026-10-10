package handlers

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/shalarasgar/carpool-backend/internal/http/dto"
	httperr "github.com/shalarasgar/carpool-backend/internal/http/errors"
	"github.com/shalarasgar/carpool-backend/internal/http/middleware"
	"github.com/shalarasgar/carpool-backend/internal/service"
)

// Blocks serves user-to-user blocks.
type Blocks struct {
	Logger  *slog.Logger
	Service *service.Blocks
}

// Create handles POST /v1/users/{id}/block.
func (h Blocks) Create(w http.ResponseWriter, r *http.Request) {
	block, created, err := h.Service.Create(r.Context(), middleware.UserIDFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httperr.WriteJSON(w, status, toBlockDTO(block))
}

// Delete handles DELETE /v1/users/{id}/block.
func (h Blocks) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.Service.Delete(r.Context(), middleware.UserIDFrom(r.Context()), chi.URLParam(r, "id")); err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// List handles GET /v1/me/blocks.
func (h Blocks) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := h.Service.List(r.Context(), middleware.UserIDFrom(r.Context()), service.PageQuery{
		Page:     q.Get("page"),
		PageSize: q.Get("page_size"),
	})
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	out := dto.BlockPage{
		Blocks:   make([]dto.Block, 0, len(page.Blocks)),
		Page:     page.Page,
		PageSize: page.PageSize,
		HasMore:  page.HasMore,
	}
	for _, block := range page.Blocks {
		out.Blocks = append(out.Blocks, toBlockDTO(block))
	}
	httperr.WriteJSON(w, http.StatusOK, out)
}

func toBlockDTO(block service.Block) dto.Block {
	return dto.Block{
		UserID:    block.UserID,
		Name:      block.Name,
		PhotoURL:  block.PhotoURL,
		CreatedAt: block.CreatedAt,
	}
}
