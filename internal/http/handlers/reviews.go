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

// Reviews serves trip ratings.
type Reviews struct {
	Logger  *slog.Logger
	Service *service.Reviews
}

// Create handles POST /v1/trips/{id}/reviews.
func (h Reviews) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateReviewRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	review, err := h.Service.Create(r.Context(), middleware.UserIDFrom(r.Context()), chi.URLParam(r, "id"), service.ReviewInput{
		ToUserID: req.ToUserID,
		Rating:   req.Rating,
		Comment:  req.Comment,
	})
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	httperr.WriteJSON(w, http.StatusCreated, toReviewDTO(review))
}

// List handles GET /v1/users/{id}/reviews.
func (h Reviews) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := h.Service.List(r.Context(), chi.URLParam(r, "id"), service.PageQuery{
		Page:     q.Get("page"),
		PageSize: q.Get("page_size"),
	})
	if err != nil {
		writeServiceError(w, r, h.Logger, err)
		return
	}
	out := dto.ReviewPage{
		Reviews:  make([]dto.Review, 0, len(page.Reviews)),
		Page:     page.Page,
		PageSize: page.PageSize,
		HasMore:  page.HasMore,
	}
	for _, review := range page.Reviews {
		out.Reviews = append(out.Reviews, toReviewDTO(review))
	}
	httperr.WriteJSON(w, http.StatusOK, out)
}

func toReviewDTO(review service.Review) dto.Review {
	return dto.Review{
		ID:     review.ID,
		TripID: review.TripID,
		From: dto.ReviewAuthor{
			ID:       review.From.ID,
			Name:     review.From.Name,
			PhotoURL: review.From.PhotoURL,
		},
		ToUserID:  review.ToUserID,
		Rating:    review.Rating,
		Comment:   review.Comment,
		CreatedAt: review.CreatedAt,
	}
}
