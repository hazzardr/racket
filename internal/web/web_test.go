package web

import (
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRenderTemplates(t *testing.T) {
	w, err := New()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		data     any
		contains string
	}{
		{"landing.html", nil, "Racket"},
		{"machines.html", []any{}, "No machines yet"},
		{"new_machine.html", struct{ Error, Name, Description, Location, BuiltOn, PurchasedOn string }{}, "New machine"},
		{"machine_detail.html", struct {
			Machine struct {
				Name                                        string
				Description, Location, BuiltOn, PurchasedOn sql.NullString
				ID                                          int64
			}
			Slots []any
			Parts []any
			Cats  []string
		}{Slots: []any{}, Parts: []any{}, Cats: []string{}}, "slots-grid"},
		{"import.html", struct{ Error, MachineName, PCPPURL string }{}, "Import CSV"},
		{"error.html", struct{ Message string }{Message: "test error"}, "test error"},
		{"slot_card", struct {
			ID       int64
			Category string
			PartID   sql.NullInt64
			PartName sql.NullString
			Error    string
		}{}, "slot-card"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			w.Render(rr, 200, tc.name, tc.data)
			body := rr.Body.String()
			if !strings.Contains(body, tc.contains) {
				t.Errorf("expected body to contain %q, got:\n%s", tc.contains, body)
			}
		})
	}
}
