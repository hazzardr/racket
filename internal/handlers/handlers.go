package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"racket/internal/components"
	"racket/internal/db"
)

type Handler struct {
	queries *db.Queries
}

func New(q *db.Queries) *Handler {
	return &Handler{queries: q}
}

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	machines, err := h.queries.ListMachines(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	parts, err := h.queries.ListParts(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(r.Context(), w, components.Home(machines, parts))
}

func parseID(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(chi.URLParam(r, name), 10, 64)
}

func render(ctx context.Context, w http.ResponseWriter, component templ.Component) {
	if err := component.Render(ctx, w); err != nil {
		slog.ErrorContext(ctx, "failed to render component", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
