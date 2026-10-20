package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"racket/internal/auth"
	"racket/internal/auth/authtest"
)

// The header offers the configured ways to sign in, on every page.
func TestHeaderSignIn(t *testing.T) {
	t.Run("dev login", func(t *testing.T) {
		rr := newTestApp(t).get(nil, "/")
		assertStatus(t, rr, http.StatusOK)
		assertContains(t, rr, `action="/auth/dev"`)
		assertNotContains(t, rr, "Log in with Discord")
	})
	t.Run("Discord", func(t *testing.T) {
		rr := newTestAppWithDiscord(t).get(nil, "/")
		assertStatus(t, rr, http.StatusOK)
		assertContains(t, rr, "Log in with Discord", `href="/auth/discord"`)
		assertNotContains(t, rr, `action="/auth/dev"`)
	})
	t.Run("signed in", func(t *testing.T) {
		app := newTestApp(t)
		rr := app.get(app.login("alice"), "/")
		assertStatus(t, rr, http.StatusOK)
		assertNotContains(t, rr, `action="/auth/dev"`)
	})
}

func TestDevLogin(t *testing.T) {
	app := newTestApp(t)

	for _, bad := range []string{"  ", strings.Repeat("a", maxDevUsername+1)} {
		rr := app.post(nil, devLoginPath, url.Values{"username": {bad}})
		assertStatus(t, rr, http.StatusUnprocessableEntity)
		assertContains(t, rr, "Pick a username")
	}

	alice := app.login("alice")
	rr := app.get(alice, "/machines")
	assertStatus(t, rr, http.StatusOK)
	assertContains(t, rr, `class="nav-user">alice</span>`, "Log out")

	// The same name signs in as the same user.
	app.login("Alice")
	if n := len(mustListUsers(t, app)); n != 1 {
		t.Errorf("%d users after signing in twice, want 1", n)
	}

	// The limit counts characters, as the form's maxlength does, not bytes.
	app.login(strings.Repeat("é", maxDevUsername))
}

func mustListUsers(t *testing.T, app *testApp) []int64 {
	t.Helper()
	rows, err := app.db.QueryContext(t.Context(), `SELECT id FROM users`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestOldLoginPageRedirectsHome(t *testing.T) {
	rr := newTestApp(t).get(nil, "/login")
	assertStatus(t, rr, http.StatusMovedPermanently)
	if loc := rr.Header().Get("Location"); loc != "/" {
		t.Errorf("redirected to %q, want /", loc)
	}
}

// Every route outside the public ones needs a signed-in user, including
// routes added later.
func TestEveryPrivateRouteRequiresSignIn(t *testing.T) {
	app := newTestAppWithDiscord(t)
	public := map[string]bool{
		"GET /healthz": true, "GET /": true, "GET /login": true, "POST /logout": true,
		"GET " + discordLoginPath: true, "GET " + discordCallbackPath: true,
	}
	routes, ok := app.router.(chi.Routes)
	if !ok {
		t.Fatalf("router is %T, want chi.Routes", app.router)
	}
	checked := 0
	err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		// Static assets are served for every method.
		if public[method+" "+route] || route == "/static/*" {
			return nil
		}
		checked++
		path := strings.ReplaceAll(route, "{id}", "1")
		rr := app.do(nil, method, path, nil, false)
		if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != auth.LoginPath {
			t.Errorf("%s %s signed out: status %d, Location %q; want a redirect to sign in",
				method, route, rr.Code, rr.Header().Get("Location"))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 20 {
		t.Errorf("checked %d routes; the walk missed the private ones", checked)
	}
}

func TestDevLoginOffByDefault(t *testing.T) {
	app := newTestAppWithDiscord(t)
	rr := app.post(nil, devLoginPath, url.Values{"username": {"alice"}})
	assertStatus(t, rr, http.StatusNotFound)
}

func TestLogout(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")

	rr := app.post(alice, "/logout", nil)
	assertStatus(t, rr, http.StatusSeeOther)
	if c := responseCookie(rr, auth.SessionCookie); c == nil || c.MaxAge >= 0 {
		t.Errorf("session cookie = %+v, want it cleared", c)
	}
	// The old cookie no longer works.
	assertStatus(t, app.get(alice, "/machines"), http.StatusSeeOther)
}

func TestDiscordLoginRedirects(t *testing.T) {
	app := newTestAppWithDiscord(t)

	rr := app.get(nil, discordLoginPath)
	assertStatus(t, rr, http.StatusFound)
	state := responseCookie(rr, stateCookie)
	if state == nil || state.Value == "" || !state.HttpOnly {
		t.Fatalf("state cookie = %+v, want an HttpOnly random state", state)
	}
	// Discord's redirect back is a cross-site top-level GET to the callback:
	// the cookie must cover that path and be sent on it (Lax, not Strict).
	if !strings.HasPrefix(discordCallbackPath, state.Path) || state.SameSite != http.SameSiteLaxMode {
		t.Errorf("state cookie Path=%q SameSite=%v, want it sent to %s (Lax)",
			state.Path, state.SameSite, discordCallbackPath)
	}
	loc, err := url.Parse(rr.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := loc.Query()
	if !strings.HasSuffix(loc.Path, "/oauth2/authorize") || q.Get("state") != state.Value ||
		q.Get("client_id") != authtest.ClientID || q.Get("redirect_uri") != "http://racket.test"+discordCallbackPath {
		t.Errorf("redirect = %s, want the authorize URL with our state and callback", loc)
	}
}

// discordCallback runs the callback with the state cookie (none if "") and
// query parameters, and returns the response and any session cookie it set.
func discordCallback(app *testApp, cookieState string, params url.Values) (*httptest.ResponseRecorder, *http.Cookie) {
	app.t.Helper()
	var c *http.Cookie
	if cookieState != "" {
		c = &http.Cookie{Name: stateCookie, Value: cookieState}
	}
	rr := app.do(c, http.MethodGet, discordCallbackPath+"?"+params.Encode(), nil, false)
	return rr, responseCookie(rr, auth.SessionCookie)
}

func TestDiscordCallback(t *testing.T) {
	tests := []struct {
		name        string
		cookieState string
		params      url.Values
		wantStatus  int
		wantBody    string
		failMember  int // status for the fake's membership check
		signedIn    bool
	}{
		{
			name:        "member signs in",
			cookieState: "s1",
			params:      url.Values{"state": {"s1"}, "code": {"alice-code"}},
			wantStatus:  http.StatusSeeOther, signedIn: true,
		},
		{
			name:        "not in the server",
			cookieState: "s1",
			params:      url.Values{"state": {"s1"}, "code": {"mallory-code"}},
			wantStatus:  http.StatusForbidden, wantBody: "only open to members",
		},
		{
			name:        "state mismatch",
			cookieState: "s1",
			params:      url.Values{"state": {"s2"}, "code": {"alice-code"}},
			wantStatus:  http.StatusBadRequest, wantBody: "expired",
		},
		{
			name:       "no state cookie",
			params:     url.Values{"state": {"s1"}, "code": {"alice-code"}},
			wantStatus: http.StatusBadRequest, wantBody: "expired",
		},
		{
			name:        "cancelled on Discord",
			cookieState: "s1",
			params:      url.Values{"state": {"s1"}, "error": {"access_denied"}},
			wantStatus:  http.StatusBadRequest, wantBody: "cancelled",
		},
		{
			name:        "bad code",
			cookieState: "s1",
			params:      url.Values{"state": {"s1"}, "code": {"bogus"}},
			wantStatus:  http.StatusBadGateway, wantBody: "Couldn&#39;t sign in with Discord",
		},
		{
			name:        "membership check fails",
			cookieState: "s1",
			params:      url.Values{"state": {"s1"}, "code": {"alice-code"}},
			failMember:  http.StatusServiceUnavailable,
			wantStatus:  http.StatusBadGateway, wantBody: "Couldn&#39;t sign in with Discord",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newTestAppWithDiscord(t)
			app.discord.AddUser("alice-code", auth.Identity{ID: "111", Username: "alice", GlobalName: "Alice"}, true)
			app.discord.AddUser("mallory-code", auth.Identity{ID: "666", Username: "mallory"}, false)
			app.discord.SetFailMember(tt.failMember)

			rr, session := discordCallback(app, tt.cookieState, tt.params)
			assertStatus(t, rr, tt.wantStatus)
			if tt.wantBody != "" {
				assertContains(t, rr, tt.wantBody, "Couldn&#39;t sign you in")
			}
			// The state is single-use: the callback always clears it.
			if c := responseCookie(rr, stateCookie); c == nil || c.MaxAge >= 0 {
				t.Errorf("state cookie = %+v, want it cleared", c)
			}
			if (session != nil) != tt.signedIn {
				t.Fatalf("session cookie set = %v, want %v", session != nil, tt.signedIn)
			}
			if !tt.signedIn {
				if n := len(mustListUsers(t, app)); n != 0 {
					t.Errorf("%d users created, want 0", n)
				}
				return
			}
			rr = app.get(session, "/machines")
			assertStatus(t, rr, http.StatusOK)
			assertContains(t, rr, `class="nav-user">alice</span>`)
		})
	}
}

func TestDiscordCallbackRestoresFormerMember(t *testing.T) {
	app := newTestAppWithDiscord(t)
	app.discord.AddUser("bob-code", auth.Identity{ID: "222", Username: "bob"}, true)

	_, first := discordCallback(app, "s", url.Values{"state": {"s"}, "code": {"bob-code"}})
	if first == nil {
		t.Fatal("first sign-in failed")
	}
	if _, err := app.db.ExecContext(t.Context(), `UPDATE users SET deleted_at = CURRENT_TIMESTAMP`); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, app.get(first, "/machines"), http.StatusSeeOther)

	_, again := discordCallback(app, "s", url.Values{"state": {"s"}, "code": {"bob-code"}})
	if again == nil {
		t.Fatal("signing in again failed")
	}
	assertStatus(t, app.get(again, "/machines"), http.StatusOK)
	if n := len(mustListUsers(t, app)); n != 1 {
		t.Errorf("%d users, want Bob restored rather than duplicated", n)
	}
}
