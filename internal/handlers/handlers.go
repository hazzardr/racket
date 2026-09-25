package handlers

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

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

// renderError renders the error page with the given status and a user-facing
// message. Use serverError for internal failures.
func (h *Handler) renderError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	slog.DebugContext(r.Context(), "request error", "status", status, "message", msg)
	h.web.Render(w, status, "error.html", struct{ Message string }{Message: msg})
}

// serverError logs err and renders a generic 500, so database and driver
// details never reach the client.
func (h *Handler) serverError(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "internal error", "method", r.Method, "path", r.URL.Path, "error", err)
	h.web.Render(w, http.StatusInternalServerError, "error.html", struct{ Message string }{
		Message: "Something went wrong on our end. Please try again.",
	})
}

// isUniqueViolation reports whether err is a SQLite UNIQUE constraint failure.
func isUniqueViolation(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}

func parseID(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(chi.URLParam(r, name), 10, 64)
}

// machinesPath is the machines list route, used in redirect targets.
const machinesPath = "/machines"

func isHX(r *http.Request) bool {
	return r.Header.Get("Hx-Request") != ""
}

// redirectNativeOrHX issues a 303 for native requests or sets Hx-Redirect for
// htmx, for actions that navigate to another page (create, delete machine).
// Callers pass static paths or "/machines/<int64 id>" built from DB rows.
func redirectNativeOrHX(w http.ResponseWriter, r *http.Request, url string) {
	if !isHX(r) {
		http.Redirect(w, r, url, http.StatusSeeOther) //nolint:gosec // G710: url from static path or validated DB id
		return
	}
	w.Header().Set("Hx-Redirect", url)
}

// redirectNative issues a 303 for native requests and reports whether it did.
// htmx requests get nothing here: the caller renders a fragment to swap in
// place (an Hx-Redirect would make htmx reload the page and skip the swap).
func redirectNative(w http.ResponseWriter, r *http.Request, url string) bool {
	if isHX(r) {
		return false
	}
	http.Redirect(w, r, url, http.StatusSeeOther) //nolint:gosec // G710: url from validated DB id
	return true
}
