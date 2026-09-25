package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"

	"racket/internal/db"
	"racket/internal/handlers"
	"racket/internal/web"
)

// newTestServer builds the production router over an in-memory database,
// migrated from the embedded migrations (goose's base FS is set in init).
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
	return newRouter(handlers.New(sqlDB, db.New(sqlDB), webUI), webUI)
}

func TestCrossOriginProtection(t *testing.T) {
	srv := newTestServer(t)

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
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, req)
			if rr.Code != tt.want {
				t.Fatalf("expected %d, got %d body: %s", tt.want, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestCrossOriginProtectionAllowsCrossSiteGET(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "http://racket.test/machines", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for cross-site GET (e.g. a followed link), got %d", rr.Code)
	}
}
