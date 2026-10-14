package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"

	"racket/internal/auth"
	"racket/internal/web"
)

// newTestServer builds the production router over an in-memory database,
// migrated from the embedded migrations (goose's base FS is set in init),
// with dev login on.
func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", sqliteDSN(":memory:"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	// Each pooled connection would get its own empty :memory: database.
	sqlDB.SetMaxOpenConns(1)
	if err := goose.Up(sqlDB, "."); err != nil {
		t.Fatal(err)
	}
	webUI, err := web.New()
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse("http://racket.test")
	return newHandler(sqlDB, webUI, config{baseURL: base, devLogin: true}).Routes()
}

// login signs in through dev login and returns the session cookie.
func login(t *testing.T, srv http.Handler, username string) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://racket.test/auth/dev",
		strings.NewReader(url.Values{"username": {username}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	for _, c := range rr.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			return c
		}
	}
	t.Fatalf("dev login: status %d, no session cookie", rr.Code)
	return nil
}

func TestCrossOriginProtection(t *testing.T) {
	srv := newTestServer(t)
	alice := login(t, srv, "alice")

	tests := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"cross-site fetch metadata", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"foreign origin", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"same-origin fetch metadata", map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusSeeOther},
		{"matching origin", map[string]string{"Origin": "http://racket.test"}, http.StatusSeeOther},
		{"non-browser client", nil, http.StatusSeeOther},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Unique names so successful creates don't collide.
			body := strings.NewReader("name=machine-" + string(rune('a'+i)))
			req := httptest.NewRequest(http.MethodPost, "http://racket.test/machines", body)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(alice)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, req)
			if rr.Code != tt.want {
				t.Fatalf("expected %d, got %d body: %s", tt.want, rr.Code, rr.Body.String())
			}
			// A request that got through created the machine.
			if loc := rr.Header().Get("Location"); tt.want == http.StatusSeeOther && !strings.HasPrefix(loc, "/machines/") {
				t.Fatalf("redirected to %q, want the new machine", loc)
			}
		})
	}
}

func TestCrossOriginProtectionAllowsCrossSiteGET(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "http://racket.test/machines", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.AddCookie(login(t, srv, "alice"))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for cross-site GET (e.g. a followed link), got %d", rr.Code)
	}
}

func TestLoadConfig(t *testing.T) {
	discord := map[string]string{
		"RACKET_DISCORD_CLIENT_ID":     "id",
		"RACKET_DISCORD_CLIENT_SECRET": "secret",
		"RACKET_DISCORD_GUILD_ID":      "guild",
	}
	with := func(base map[string]string, extra ...string) map[string]string {
		env := map[string]string{}
		for k, v := range base {
			env[k] = v
		}
		for i := 0; i+1 < len(extra); i += 2 {
			env[extra[i]] = extra[i+1]
		}
		return env
	}

	tests := []struct {
		name    string
		env     map[string]string
		wantErr string // "" means valid
		check   func(t *testing.T, cfg config)
	}{
		{name: "nothing configured", env: nil, wantErr: "no way to sign in"},
		{
			name: "dev login",
			env:  map[string]string{"RACKET_DEV_LOGIN": "1"},
			check: func(t *testing.T, cfg config) {
				t.Helper()
				if !cfg.devLogin || cfg.discord != nil || cfg.baseURL.String() != "http://localhost:8080" {
					t.Errorf("cfg = %+v", cfg)
				}
			},
		},
		{
			name: "Discord",
			env:  with(discord, "RACKET_BASE_URL", "https://racket.example.com/"),
			check: func(t *testing.T, cfg config) {
				t.Helper()
				if cfg.discord == nil || cfg.discord.RedirectURL != "https://racket.example.com/auth/discord/callback" {
					t.Errorf("discord = %+v, want callback under the base URL", cfg.discord)
				}
			},
		},
		{
			name:    "Discord partly configured",
			env:     map[string]string{"RACKET_DISCORD_CLIENT_ID": "id"},
			wantErr: "RACKET_DISCORD_CLIENT_SECRET, RACKET_DISCORD_GUILD_ID",
		},
		{
			name:    "dev login on an https deployment",
			env:     with(discord, "RACKET_DEV_LOGIN", "1", "RACKET_BASE_URL", "https://racket.example.com"),
			wantErr: "RACKET_DEV_LOGIN",
		},
		{
			name:    "base URL without a scheme",
			env:     with(discord, "RACKET_BASE_URL", "racket.example.com"),
			wantErr: "RACKET_BASE_URL",
		},
	}
	vars := []string{
		"RACKET_DB", "PORT", "RACKET_BASE_URL", "RACKET_DEV_LOGIN",
		"RACKET_DISCORD_CLIENT_ID", "RACKET_DISCORD_CLIENT_SECRET", "RACKET_DISCORD_GUILD_ID",
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, v := range vars {
				t.Setenv(v, tt.env[v])
			}
			cfg, err := loadConfig()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
			case tt.check != nil:
				tt.check(t, cfg)
			}
		})
	}
}
