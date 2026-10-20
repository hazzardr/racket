package main

import (
	"database/sql"
	"log/slog"
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
	sqlDB := newTestDB(t)
	webUI, err := web.New()
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse("http://racket.test")
	return newHandler(sqlDB, webUI, config{baseURL: base, devLogin: true}).Routes()
}

// newTestDB is an in-memory database, migrated from the embedded
// migrations (goose's base FS is set in init).
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", sqliteDSN(":memory:"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	// Each pooled connection would get its own empty :memory: database.
	sqlDB.SetMaxOpenConns(1)
	if err = goose.Up(sqlDB, "."); err != nil {
		t.Fatal(err)
	}
	return sqlDB
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
				if cfg.host != loopback {
					t.Errorf("host = %q, want dev login on loopback only", cfg.host)
				}
			},
		},
		{
			name: "dev login on a chosen host",
			env:  map[string]string{"RACKET_DEV_LOGIN": "1", "RACKET_HOST": "0.0.0.0"},
			check: func(t *testing.T, cfg config) {
				t.Helper()
				if cfg.host != "0.0.0.0" {
					t.Errorf("host = %q, want the one set", cfg.host)
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
			name: "listen host and client IP header",
			env:  with(discord, "RACKET_HOST", loopback, "RACKET_CLIENT_IP_HEADER", "Cf-Connecting-IP"),
			check: func(t *testing.T, cfg config) {
				t.Helper()
				if cfg.host != loopback || cfg.clientIPHeader != "Cf-Connecting-IP" {
					t.Errorf("host = %q, clientIPHeader = %q", cfg.host, cfg.clientIPHeader)
				}
			},
		},
		{
			name:    "base URL without a scheme",
			env:     with(discord, "RACKET_BASE_URL", "racket.example.com"),
			wantErr: "RACKET_BASE_URL",
		},
		{
			name:    "base URL with a path",
			env:     with(discord, "RACKET_BASE_URL", "https://example.com/racket"),
			wantErr: "no path",
		},
		{
			name:    "base URL with a query",
			env:     with(discord, "RACKET_BASE_URL", "https://example.com?x=1"),
			wantErr: "no path",
		},
	}
	vars := []string{
		"RACKET_DB", "RACKET_HOST", "PORT", "RACKET_BASE_URL", "RACKET_DEV_LOGIN", "RACKET_CLIENT_IP_HEADER",
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

func TestClientIP(t *testing.T) {
	tests := []struct {
		name   string
		header string // the configured header name
		value  string // what the request sends in Cf-Connecting-IP
		want   string
	}{
		{"header used", "Cf-Connecting-IP", "203.0.113.7", "203.0.113.7"},
		{"IPv6", "Cf-Connecting-IP", "2001:db8::1", "2001:db8::1"},
		{"IPv4-mapped IPv6", "Cf-Connecting-IP", "::ffff:203.0.113.7", "203.0.113.7"},
		{"not an IP", "Cf-Connecting-IP", "evil, 1.2.3.4", "192.0.2.1:1234"},
		{"header missing", "Cf-Connecting-IP", "", "192.0.2.1:1234"},
		{"no header configured", "", "203.0.113.7", "192.0.2.1:1234"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			h := clientIP(tt.header, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = r.RemoteAddr
			}))
			req := httptest.NewRequest(http.MethodGet, "http://racket.test/", nil)
			req.RemoteAddr = "192.0.2.1:1234"
			// A header the server wasn't told to trust changes nothing.
			req.Header.Set("X-Forwarded-For", "198.51.100.9")
			if tt.value != "" {
				req.Header.Set("Cf-Connecting-Ip", tt.value) // canonical form of the configured name
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got != tt.want {
				t.Errorf("RemoteAddr = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRequestLog(t *testing.T) {
	var buf strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	h := requestLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	h.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "http://racket.test/auth/discord/callback?code=secret", nil))

	got := buf.String()
	for _, want := range []string{"method=GET", "path=/auth/discord/callback", "status=418"} {
		if !strings.Contains(got, want) {
			t.Errorf("log %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "secret") {
		t.Errorf("log %q includes the query", got)
	}
}
