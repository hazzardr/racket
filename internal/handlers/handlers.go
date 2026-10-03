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

	"racket/internal/auth"
	"racket/internal/db"
	"racket/internal/web"
)

// Auth configures sign-in.
type Auth struct {
	Sessions *auth.Sessions
	// Discord is nil when Discord sign-in isn't configured.
	Discord *auth.Discord
	// DevLogin lets anyone sign in as any username, for local development.
	DevLogin bool
	// SecureCookies marks cookies HTTPS-only.
	SecureCookies bool
}

type Handler struct {
	db      *sql.DB
	queries *db.Queries
	web     *web.Web
	auth    Auth
}

func New(sqlDB *sql.DB, q *db.Queries, w *web.Web, a Auth) *Handler {
	return &Handler{db: sqlDB, queries: q, web: w, auth: a}
}

// viewer returns the signed-in user. Routes behind auth.RequireUser always
// have one; elsewhere it's the zero User, whose ID 0 matches no owner.
func viewer(r *http.Request) db.User {
	u, _ := auth.UserFrom(r.Context())
	return u
}

// render renders a full page with the signed-in user for the header.
func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, name string, data any) {
	page := web.Page{Path: r.URL.Path, Data: data}
	if u, ok := auth.UserFrom(r.Context()); ok {
		page.User = &u
	}
	h.web.Render(w, status, name, page)
}

// renderError renders the error page with the given status and a user-facing
// message. Use serverError for internal failures.
func (h *Handler) renderError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	slog.DebugContext(r.Context(), "request error", "status", status, "message", msg)
	h.render(w, r, status, "error.html", struct{ Message string }{Message: msg})
}

// forbidden renders the error page for a change to someone else's things.
func (h *Handler) forbidden(w http.ResponseWriter, r *http.Request) {
	h.renderError(w, r, http.StatusForbidden, "You can only change your own machines and parts.")
}

// serverError logs err and renders a generic 500, so database and driver
// details never reach the client.
func (h *Handler) serverError(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "internal error", "method", r.Method, "path", r.URL.Path, "error", err)
	h.render(w, r, http.StatusInternalServerError, "error.html", struct{ Message string }{
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
	http.Redirect(w, r, url, http.StatusSeeOther)
	return true
}
