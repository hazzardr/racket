package handlers

import (
	"database/sql"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"racket/internal/db"
	"racket/internal/web"
)

type Handler struct {
	db      *sql.DB
	queries *db.Queries
	web     *web.Web
}

func New(sqlDB *sql.DB, q *db.Queries, w *web.Web) *Handler {
	return &Handler{db: sqlDB, queries: q, web: w}
}

// renderError renders the error page with the given status and message.
func (h *Handler) renderError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	slog.ErrorContext(r.Context(), "request error", "status", status, "error", msg)
	h.web.Render(w, status, "error.html", struct{ Message string }{Message: msg})
}

func parseID(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(chi.URLParam(r, name), 10, 64)
}

// redirectNativeOrHX issues a 303 for native requests or sets Hx-Redirect for htmx.
// Returns true if the response was written (native redirect) so callers can return early.
func redirectNativeOrHX(w http.ResponseWriter, r *http.Request, url string) bool {
	if r.Header.Get("Hx-Request") == "" {
		http.Redirect(w, r, url, http.StatusSeeOther)
		return true
	}
	w.Header().Set("Hx-Redirect", url)
	return false
}
