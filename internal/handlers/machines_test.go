package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/go-chi/chi/v5"
	"github.com/pressly/goose/v3"

	"racket/internal/db"
	"racket/internal/web"
)

func init() {
	goose.SetDialect("sqlite3")
}

func newTestWeb(t *testing.T) *web.Web {
	t.Helper()
	w, err := web.New()
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	// FK enforcement matches the production DSN (see sqliteDSN in cmd/server).
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	return db
}

func newTestRouter(t *testing.T) (*chi.Mux, *sql.DB) {
	t.Helper()
	sqlDB := newTestDB(t)
	queries := db.New(sqlDB)
	h := New(sqlDB, queries, newTestWeb(t))

	r := chi.NewRouter()
	r.Post("/machines", h.CreateMachine)
	r.Post("/machines/{id}/delete", h.DeleteMachine)
	r.Post("/machines/{id}/slots", h.CreateSlot)
	r.Post("/slots/{id}/part", h.AssignPart)
	r.Post("/slots/{id}/part/delete", h.UnassignPart)
	r.Post("/slots/{id}/delete", h.DeleteSlot)
	return r, sqlDB
}

// postForm posts urlencoded form data and returns the recorder.
func postForm(r *chi.Mux, path string, data url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

// postFormHX posts urlencoded form data as an htmx request (fragment path).
func postFormHX(r *chi.Mux, path string, data url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Hx-Request", "true")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

// createTestMachine posts the native new-machine form and returns the machine ID.
func createTestMachine(t *testing.T, r *chi.Mux, name string) int64 {
	t.Helper()
	rr := postForm(r, "/machines", url.Values{"name": {name}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("CreateMachine: expected 303, got %d body: %s", rr.Code, rr.Body.String())
	}
	// Location: /machines/{id}
	loc := rr.Header().Get("Location")
	id, err := strconv.ParseInt(strings.TrimPrefix(loc, "/machines/"), 10, 64)
	if err != nil {
		t.Fatalf("CreateMachine: bad Location %q: %v", loc, err)
	}
	return id
}

func testSlots(t *testing.T, sqlDB *sql.DB, machineID int64) []db.ListSlotsByMachineRow {
	t.Helper()
	slots, err := db.New(sqlDB).ListSlotsByMachine(context.Background(), machineID)
	if err != nil {
		t.Fatal(err)
	}
	return slots
}

func TestCreateMachineSeedsSlots(t *testing.T) {
	r, sqlDB := newTestRouter(t)
	defer sqlDB.Close()

	id := createTestMachine(t, r, "test")

	slots := testSlots(t, sqlDB, id)
	if len(slots) != 9 {
		t.Fatalf("expected 9 slots, got %d", len(slots))
	}
	for _, slot := range slots {
		if slot.PartID.Valid {
			t.Errorf("expected slot %d to be empty, got part %v", slot.ID, slot.PartID)
		}
	}
}

func TestCreateMachineHXRedirect(t *testing.T) {
	r, sqlDB := newTestRouter(t)
	defer sqlDB.Close()

	req := httptest.NewRequest(http.MethodPost, "/machines", strings.NewReader("name=hx"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Hx-Request", "true")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", rr.Code, rr.Body.String())
	}
	if loc := rr.Header().Get("Hx-Redirect"); loc != "/machines/1" {
		t.Fatalf("expected HX-Redirect /machines/1, got %q", loc)
	}
}

func TestCreateMachineNameRequired(t *testing.T) {
	r, sqlDB := newTestRouter(t)
	defer sqlDB.Close()

	rr := postForm(r, "/machines", url.Values{"name": {""}})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Name is required.") {
		t.Fatalf("expected error text in re-rendered form, got: %s", rr.Body.String())
	}
}

func TestDeleteMachine(t *testing.T) {
	r, sqlDB := newTestRouter(t)
	defer sqlDB.Close()

	id := createTestMachine(t, r, "doomed")

	rr := postForm(r, "/machines/"+strconv.FormatInt(id, 10)+"/delete", nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("DeleteMachine: expected 303, got %d body: %s", rr.Code, rr.Body.String())
	}
	if loc := rr.Header().Get("Location"); loc != "/machines" {
		t.Fatalf("expected redirect to /machines, got %q", loc)
	}

	_, err := db.New(sqlDB).GetMachine(context.Background(), id)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected machine %d to be deleted, got err=%v", id, err)
	}

	// Foreign keys are enforced: slots cascade-delete with the machine.
	slots := testSlots(t, sqlDB, id)
	if len(slots) != 0 {
		t.Fatalf("expected 0 slots after machine delete, got %d", len(slots))
	}
}

func TestAssignPart(t *testing.T) {
	r, sqlDB := newTestRouter(t)
	defer sqlDB.Close()

	machineID := createTestMachine(t, r, "test")
	slotID := testSlots(t, sqlDB, machineID)[0].ID

	rr := postFormHX(r, "/slots/"+strconv.FormatInt(slotID, 10)+"/part", url.Values{
		"name":         {"B550 Tomahawk"},
		"manufacturer": {"MSI"},
		"model":        {"B550"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("AssignPart: expected 200, got %d body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "B550 Tomahawk") {
		t.Fatalf("expected fragment to contain part name, got: %s", rr.Body.String())
	}

	slots := testSlots(t, sqlDB, machineID)
	if !slots[0].PartID.Valid {
		t.Fatal("expected slot 0 to be filled")
	}
	if slots[0].PartName.String != "B550 Tomahawk" {
		t.Fatalf("expected part name 'B550 Tomahawk', got %q", slots[0].PartName.String)
	}
}

func TestDoubleFillSlotRejected(t *testing.T) {
	r, sqlDB := newTestRouter(t)
	defer sqlDB.Close()

	machineID := createTestMachine(t, r, "test")
	slotID := testSlots(t, sqlDB, machineID)[0].ID
	path := "/slots/" + strconv.FormatInt(slotID, 10) + "/part"

	rr := postFormHX(r, path, url.Values{"name": {"Part A"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("first AssignPart: expected 200, got %d body: %s", rr.Code, rr.Body.String())
	}

	// Second fill: 200 with the error embedded in the fragment (htmx swaps on 200).
	rr = postFormHX(r, path, url.Values{"name": {"Part B"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("second AssignPart: expected 200, got %d body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Slot already filled.") {
		t.Fatalf("expected 'Slot already filled.' in fragment, got: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Part A") {
		t.Fatalf("expected fragment to show existing part, got: %s", rr.Body.String())
	}
}

func TestUnassignPart(t *testing.T) {
	r, sqlDB := newTestRouter(t)
	defer sqlDB.Close()

	machineID := createTestMachine(t, r, "test")
	slotID := testSlots(t, sqlDB, machineID)[0].ID

	rr := postFormHX(r, "/slots/"+strconv.FormatInt(slotID, 10)+"/part", url.Values{"name": {"Part A"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("AssignPart: expected 200, got %d body: %s", rr.Code, rr.Body.String())
	}

	rr = postFormHX(r, "/slots/"+strconv.FormatInt(slotID, 10)+"/part/delete", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("UnassignPart: expected 200, got %d body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `placeholder="Part name"`) {
		t.Fatalf("expected empty-state fragment, got: %s", rr.Body.String())
	}

	slots := testSlots(t, sqlDB, machineID)
	if slots[0].PartID.Valid {
		t.Fatalf("expected slot to be empty, got part %v", slots[0].PartID)
	}

	// The old UI's remove-part deletes the part row entirely.
	parts, err := db.New(sqlDB).ListPartsByMachine(context.Background(), sql.NullInt64{Int64: machineID, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 0 {
		t.Fatalf("expected part row to be deleted, got %d parts", len(parts))
	}
}

func TestCreateSlot(t *testing.T) {
	r, sqlDB := newTestRouter(t)
	defer sqlDB.Close()

	machineID := createTestMachine(t, r, "test")
	path := "/machines/" + strconv.FormatInt(machineID, 10) + "/slots"

	rr := postFormHX(r, path, url.Values{"category": {"NIC"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("CreateSlot: expected 200, got %d body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "NIC") {
		t.Fatalf("expected fragment to contain category, got: %s", rr.Body.String())
	}

	slots := testSlots(t, sqlDB, machineID)
	if len(slots) != 10 {
		t.Fatalf("expected 10 slots, got %d", len(slots))
	}
	if slots[9].Category != "NIC" {
		t.Fatalf("expected last slot category 'NIC', got %q", slots[9].Category)
	}
	if slots[9].PartID.Valid {
		t.Fatalf("expected last slot to be empty, got part %v", slots[9].PartID)
	}

	rr = postFormHX(r, path, url.Values{"category": {""}})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("CreateSlot with empty category: expected 400, got %d body: %s", rr.Code, rr.Body.String())
	}
}

func TestDeleteSlotUnassignsPart(t *testing.T) {
	r, sqlDB := newTestRouter(t)
	defer sqlDB.Close()

	machineID := createTestMachine(t, r, "test")
	slotID := testSlots(t, sqlDB, machineID)[0].ID

	rr := postFormHX(r, "/slots/"+strconv.FormatInt(slotID, 10)+"/part", url.Values{"name": {"Part A"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("AssignPart: expected 200, got %d body: %s", rr.Code, rr.Body.String())
	}

	rr = postFormHX(r, "/slots/"+strconv.FormatInt(slotID, 10)+"/delete", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("DeleteSlot: expected 200, got %d body: %s", rr.Code, rr.Body.String())
	}

	slots := testSlots(t, sqlDB, machineID)
	if len(slots) != 8 {
		t.Fatalf("expected 8 slots after delete, got %d", len(slots))
	}

	// Part A should still exist but be unassigned
	parts, err := db.New(sqlDB).ListPartsByMachine(context.Background(), sql.NullInt64{Int64: machineID, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range parts {
		if p.Name == "Part A" {
			found = true
			if p.SlotID.Valid {
				t.Fatalf("expected Part A to be unassigned, got slot_id %d", p.SlotID.Int64)
			}
		}
	}
	if !found {
		t.Fatal("Part A not found in parts list after slot deletion")
	}
}

// htmx slot actions must swap fragments in place: an Hx-Redirect header would
// make htmx navigate instead and discard the fragment.
func TestSlotHXResponsesHaveNoRedirect(t *testing.T) {
	r, sqlDB := newTestRouter(t)
	defer sqlDB.Close()

	machineID := createTestMachine(t, r, "test")
	slotPath := "/slots/" + strconv.FormatInt(testSlots(t, sqlDB, machineID)[0].ID, 10)

	steps := []struct {
		path string
		data url.Values
	}{
		{"/machines/" + strconv.FormatInt(machineID, 10) + "/slots", url.Values{"category": {"NIC"}}},
		{slotPath + "/part", url.Values{"name": {"Part A"}}},
		{slotPath + "/part/delete", nil},
		{slotPath + "/delete", nil},
	}
	for _, s := range steps {
		rr := postFormHX(r, s.path, s.data)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d body: %s", s.path, rr.Code, rr.Body.String())
		}
		if loc := rr.Header().Get("Hx-Redirect"); loc != "" {
			t.Errorf("%s: unexpected Hx-Redirect %q", s.path, loc)
		}
	}
}

func TestCreateMachineDuplicateName(t *testing.T) {
	r, sqlDB := newTestRouter(t)
	defer sqlDB.Close()

	createTestMachine(t, r, "box")
	rr := postForm(r, "/machines", url.Values{"name": {"box"}, "location": {"closet"}})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "already exists") {
		t.Fatalf("expected duplicate-name error in re-rendered form, got: %s", body)
	}
	if !strings.Contains(body, `value="closet"`) {
		t.Fatalf("expected form input to be preserved, got: %s", body)
	}
}

// Internal errors are logged, not shown: the page must not leak driver text.
func TestServerErrorHidesDetails(t *testing.T) {
	sqlDB := newTestDB(t)
	h := New(sqlDB, db.New(sqlDB), newTestWeb(t))
	sqlDB.Close() // force every query to fail with "sql: database is closed"

	rr := httptest.NewRecorder()
	h.MachinesPage(rr, httptest.NewRequest(http.MethodGet, "/machines", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "sql:") || strings.Contains(body, "closed") {
		t.Fatalf("error page leaked internal error text: %s", body)
	}
	if !strings.Contains(body, "Something went wrong") {
		t.Fatalf("expected generic error message, got: %s", body)
	}
}
