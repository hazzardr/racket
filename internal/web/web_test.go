package web

import (
	"bytes"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"racket/internal/db"
)

// Pages with their real data types render in the handlers tests
// (TestRenderTemplates there); these cover the web package's own behavior.

func TestHeaderShowsSignInState(t *testing.T) {
	w, err := New()
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	w.Render(rr, http.StatusOK, "landing.html", Page{})
	if body := rr.Body.String(); !strings.Contains(body, `href="/login"`) || strings.Contains(body, "Log out") {
		t.Errorf("signed-out header should offer login only:\n%s", body)
	}

	rr = httptest.NewRecorder()
	w.Render(rr, http.StatusOK, "landing.html", Page{User: &db.User{ID: 1, Username: "alice"}})
	body := rr.Body.String()
	for _, want := range []string{`<span class="nav-user">alice</span>`, `action="/logout"`, `href="/import"`} {
		if !strings.Contains(body, want) {
			t.Errorf("signed-in page missing %q", want)
		}
	}
}

func TestMoney(t *testing.T) {
	money, ok := funcs["money"].(func(int64) string)
	if !ok {
		t.Fatal("money has the wrong type")
	}
	for cents, want := range map[int64]string{
		0: "0.00", 5: "0.05", 14999: "149.99", 123456: "1,234.56", 123456789: "1,234,567.89", -250: "-2.50",
	} {
		if got := money(cents); got != want {
			t.Errorf("money(%d) = %q, want %q", cents, got, want)
		}
	}
}

// A template that fails mid-render must yield a clean 500, not a truncated
// page under the caller's status.
func TestRenderFailureIs500(t *testing.T) {
	tmpl := template.Must(template.New("broken").Parse(`partial output {{.Missing.Field}}`))
	w := &Web{tmpl: tmpl}

	rr := httptest.NewRecorder()
	w.Render(rr, http.StatusOK, "broken", struct{ Missing *struct{ Field string } }{})
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if bytes.Contains(rr.Body.Bytes(), []byte("partial output")) {
		t.Error("response includes the partially rendered page")
	}
}

func TestAgo(t *testing.T) {
	now := time.Now()
	for _, tt := range []struct {
		t    time.Time
		want string
	}{
		{now.Add(-time.Hour), agoToday},
		{now.Add(time.Hour), agoToday}, // clock skew
		{now.Add(-30 * time.Hour), "yesterday"},
		{now.Add(-73 * time.Hour), "3 days ago"},
	} {
		if got := ago(tt.t); got != tt.want {
			t.Errorf("ago(now%+v) = %q, want %q", tt.t.Sub(now).Round(time.Hour), got, tt.want)
		}
	}
}
