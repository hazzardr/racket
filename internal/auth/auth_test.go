package auth

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"racket/internal/db"
	"racket/migrations"

	_ "modernc.org/sqlite"
)

func newTestQueries(t *testing.T) *db.Queries {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	p, err := goose.NewProvider(goose.DialectSQLite3, sqlDB, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	return db.New(sqlDB)
}

func newTestUser(t *testing.T, q *db.Queries, name string) db.User {
	t.Helper()
	u, err := q.UpsertDiscordUser(t.Context(), db.UpsertDiscordUserParams{DiscordID: "id-" + name, Username: name})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// sessionCookie returns the session cookie a response set.
func sessionCookie(t *testing.T, rr *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rr.Result().Cookies() {
		if c.Name == SessionCookie {
			return c
		}
	}
	t.Fatal("no session cookie set")
	return nil
}

// whoami runs the middleware and reports the user it loaded.
func whoami(s *Sessions, c *http.Cookie) (db.User, bool) {
	var (
		got db.User
		ok  bool
	)
	h := s.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok = UserFrom(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if c != nil {
		req.AddCookie(c)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got, ok
}

func TestSessionLifecycle(t *testing.T) {
	q := newTestQueries(t)
	alice := newTestUser(t, q, "alice")
	s := NewSessions(q, true)

	rr := httptest.NewRecorder()
	if err := s.Start(t.Context(), rr, alice.ID); err != nil {
		t.Fatal(err)
	}
	c := sessionCookie(t, rr)
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Errorf("cookie = %+v, want HttpOnly, Secure, SameSite=Lax, Path=/", c)
	}

	if u, ok := whoami(s, c); !ok || u.ID != alice.ID {
		t.Fatalf("signed in as %+v (ok=%v), want alice", u, ok)
	}
	if _, ok := whoami(s, nil); ok {
		t.Error("no cookie: signed in, want signed out")
	}
	if _, ok := whoami(s, &http.Cookie{Name: SessionCookie, Value: "forged"}); ok {
		t.Error("unknown token: signed in, want signed out")
	}

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(c)
	rr = httptest.NewRecorder()
	if err := s.End(rr, req); err != nil {
		t.Fatal(err)
	}
	if cleared := sessionCookie(t, rr); cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Errorf("logout cookie = %+v, want it cleared", cleared)
	}
	if _, ok := whoami(s, c); ok {
		t.Error("old cookie still works after logout")
	}
}

func TestSessionExpires(t *testing.T) {
	q := newTestQueries(t)
	alice := newTestUser(t, q, "alice")
	s := NewSessions(q, false)
	start := time.Now()

	rr := httptest.NewRecorder()
	if err := s.Start(t.Context(), rr, alice.ID); err != nil {
		t.Fatal(err)
	}
	c := sessionCookie(t, rr)
	if c.Secure {
		t.Error("cookie is Secure, want plain HTTP allowed")
	}

	s.now = func() time.Time { return start.Add(SessionTTL - time.Minute) }
	if _, ok := whoami(s, c); !ok {
		t.Error("session ended before its TTL")
	}
	s.now = func() time.Time { return start.Add(SessionTTL + time.Minute) }
	if _, ok := whoami(s, c); ok {
		t.Error("session outlived its TTL")
	}
}

func TestSessionEndsWhenUserLeaves(t *testing.T) {
	q := newTestQueries(t)
	bob := newTestUser(t, q, "bob")
	s := NewSessions(q, false)

	rr := httptest.NewRecorder()
	if err := s.Start(t.Context(), rr, bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.SoftDeleteUser(t.Context(), bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := whoami(s, sessionCookie(t, rr)); ok {
		t.Error("a user who left is still signed in")
	}
}

func TestRequireUser(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := RequireUser(next)

	tests := []struct {
		name       string
		signedIn   bool
		htmx       bool
		wantStatus int
		wantHeader string // Location or Hx-Redirect
	}{
		{name: "signed in", signedIn: true, wantStatus: http.StatusTeapot},
		{name: "signed out", wantStatus: http.StatusSeeOther, wantHeader: LoginPath},
		{name: "signed out htmx", htmx: true, wantStatus: http.StatusOK, wantHeader: LoginPath},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/machines", nil)
			if tt.signedIn {
				req = req.WithContext(WithUser(req.Context(), db.User{ID: 1}))
			}
			if tt.htmx {
				req.Header.Set("Hx-Request", "true")
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			got := rr.Header().Get("Location")
			if tt.htmx {
				got = rr.Header().Get("Hx-Redirect")
			}
			if got != tt.wantHeader {
				t.Errorf("redirect = %q, want %q", got, tt.wantHeader)
			}
		})
	}
}

func TestDiscordAuthCodeURL(t *testing.T) {
	d := NewDiscord(DiscordConfig{ClientID: "abc", RedirectURL: "https://racket.test/auth/discord/callback"})
	got := d.AuthCodeURL("xyz")
	for _, want := range []string{
		"https://discord.com/oauth2/authorize?",
		"client_id=abc",
		"state=xyz",
		"scope=identify+guilds.members.read",
		"redirect_uri=https%3A%2F%2Fracket.test%2Fauth%2Fdiscord%2Fcallback",
		"response_type=code",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("AuthCodeURL = %q, missing %q", got, want)
		}
	}
}
