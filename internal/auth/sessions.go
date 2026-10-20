// Package auth handles sessions and Discord sign-in.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"racket/internal/db"
)

const (
	// SessionCookie holds the session token. The database stores only its
	// SHA-256, so a copy of the database can't be replayed as live sessions.
	SessionCookie = "racket_session"

	// SessionTTL is how long a sign-in lasts.
	SessionTTL = 30 * 24 * time.Hour

	// LoginPath is where signed-out visitors are sent: the landing page,
	// whose header has the sign-in buttons.
	LoginPath = "/"
)

type userKey struct{}

// WithUser returns ctx carrying the signed-in user.
func WithUser(ctx context.Context, u db.User) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}

// UserFrom returns the signed-in user, if any.
func UserFrom(ctx context.Context) (db.User, bool) {
	u, ok := ctx.Value(userKey{}).(db.User)
	return u, ok
}

// Sessions creates, loads, and ends database-backed sessions.
type Sessions struct {
	q      *db.Queries
	secure bool
	now    func() time.Time
}

// NewSessions returns a session store. secure marks cookies HTTPS-only; set
// it whenever the app is served over HTTPS.
func NewSessions(q *db.Queries, secure bool) *Sessions {
	return &Sessions{q: q, secure: secure, now: time.Now}
}

// Secure reports whether cookies are HTTPS-only, for other cookies the app
// sets beside the session's.
func (s *Sessions) Secure() bool {
	return s.secure
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func (s *Sessions) cookie(value string, expires time.Time) *http.Cookie {
	//nolint:gosec // G124: Secure follows the base URL's scheme; local dev is plain HTTP
	c := &http.Cookie{
		Name:     SessionCookie,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	}
	if value == "" {
		c.MaxAge = -1
	}
	return c
}

// Start creates a session for userID and sets its cookie.
func (s *Sessions) Start(ctx context.Context, w http.ResponseWriter, userID int64) error {
	now := s.now().UTC()
	// Expired rows are only ever read to be rejected; sweep them here.
	if err := s.q.DeleteExpiredSessions(ctx, now); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	token := rand.Text()
	expires := now.Add(SessionTTL)
	err := s.q.CreateSession(ctx, db.CreateSessionParams{
		TokenHash: hashToken(token),
		UserID:    userID,
		ExpiresAt: expires,
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	http.SetCookie(w, s.cookie(token, expires))
	return nil
}

// End deletes the request's session, if any, and clears its cookie.
func (s *Sessions) End(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(SessionCookie); err == nil {
		if err := s.q.DeleteSession(r.Context(), hashToken(c.Value)); err != nil {
			return fmt.Errorf("delete session: %w", err)
		}
	}
	http.SetCookie(w, s.cookie("", time.Unix(0, 0)))
	return nil
}

// Middleware loads the signed-in user, if any, into the request context.
// Expired sessions and users who have left count as signed out, and their
// cookie is cleared so later requests skip the lookup.
func (s *Sessions) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(SessionCookie)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		u, err := s.q.GetSessionUser(r.Context(), db.GetSessionUserParams{
			TokenHash: hashToken(c.Value),
			Now:       s.now().UTC(),
		})
		switch {
		case err == nil:
			r = r.WithContext(WithUser(r.Context(), u))
		case errors.Is(err, sql.ErrNoRows):
			http.SetCookie(w, s.cookie("", time.Unix(0, 0)))
		default:
			slog.ErrorContext(r.Context(), "load session", "error", err)
		}
		next.ServeHTTP(w, r)
	})
}

// RequireUser sends signed-out requests to LoginPath: a 303 for normal
// requests, an Hx-Redirect for htmx ones.
func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFrom(r.Context()); ok {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Hx-Request") != "" {
			w.Header().Set("Hx-Redirect", LoginPath)
			return
		}
		http.Redirect(w, r, LoginPath, http.StatusSeeOther)
	})
}
