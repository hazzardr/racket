package handlers

import (
	"net/http"
	"testing"
)

func TestHealthz(t *testing.T) {
	app := newTestApp(t)

	rr := app.get(nil, "/healthz")
	assertStatus(t, rr, http.StatusOK)
	if got := rr.Body.String(); got != "ok\n" {
		t.Errorf("body = %q, want %q", got, "ok\n")
	}

	// A database that's gone fails the check instead of reporting ok.
	if err := app.db.Close(); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, app.get(nil, "/healthz"), http.StatusServiceUnavailable)
}
