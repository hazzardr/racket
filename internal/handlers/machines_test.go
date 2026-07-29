package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/go-chi/chi/v5"
	"github.com/pressly/goose/v3"

	"racket/internal/db"
	"racket/internal/web"
)

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
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := goose.SetDialect("sqlite3"); err != nil {
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
	h := New(queries, newTestWeb(t))

	r := chi.NewRouter()
	r.Post("/machines", h.CreateMachine)
	r.Get("/machines/{id}", h.GetMachine)
	r.Post("/parts", h.CreatePart)
	r.Post("/machines/{id}/slots", h.CreateSlot)
	r.Delete("/slots/{id}", h.DeleteSlot)
	return r, sqlDB
}

func TestCreateMachineSeedsSlots(t *testing.T) {
	r, sqlDB := newTestRouter(t)
	defer sqlDB.Close()

	// POST a machine
	body := bytes.NewReader([]byte(`{"name":"test"}`))
	req := httptest.NewRequest(http.MethodPost, "/machines", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("CreateMachine: expected 201, got %d body: %s", rr.Code, rr.Body.String())
	}

	// GET the machine and check slots
	req = httptest.NewRequest(http.MethodGet, "/machines/1", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GetMachine: expected 200, got %d body: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Machine MachineResponse `json:"machine"`
		Slots   []SlotResponse  `json:"slots"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Slots) != 9 {
		t.Fatalf("expected 9 slots, got %d", len(resp.Slots))
	}
	for _, slot := range resp.Slots {
		if slot.Part != nil {
			t.Errorf("expected slot %d to be empty, got part %v", slot.ID, slot.Part)
		}
	}
}

func TestFillSlot(t *testing.T) {
	r, sqlDB := newTestRouter(t)
	defer sqlDB.Close()

	// Create machine
	body := bytes.NewReader([]byte(`{"name":"test"}`))
	req := httptest.NewRequest(http.MethodPost, "/machines", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	// GET machine to find first slot
	req = httptest.NewRequest(http.MethodGet, "/machines/1", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	var resp struct {
		Slots []SlotResponse `json:"slots"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Slots) == 0 {
		t.Fatal("no slots found")
	}
	slotID := resp.Slots[0].ID

	// POST a part into slot 0
	partBody := bytes.NewReader([]byte(`{"name":"Motherboard X","slot_id":` + itoa(slotID) + `}`))
	req = httptest.NewRequest(http.MethodPost, "/parts", partBody)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("CreatePart: expected 201, got %d body: %s", rr.Code, rr.Body.String())
	}

	// GET machine — slot should be filled
	req = httptest.NewRequest(http.MethodGet, "/machines/1", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Slots[0].Part == nil {
		t.Fatal("expected slot 0 to be filled")
	}
	if resp.Slots[0].Part.Name != "Motherboard X" {
		t.Fatalf("expected part name 'Motherboard X', got %q", resp.Slots[0].Part.Name)
	}
}

func TestDoubleFillSlotRejected(t *testing.T) {
	r, sqlDB := newTestRouter(t)
	defer sqlDB.Close()

	// Create machine
	body := bytes.NewReader([]byte(`{"name":"test"}`))
	req := httptest.NewRequest(http.MethodPost, "/machines", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	// Fill first slot
	partBody := bytes.NewReader([]byte(`{"name":"Part A","slot_id":1}`))
	req = httptest.NewRequest(http.MethodPost, "/parts", partBody)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("first CreatePart: expected 201, got %d body: %s", rr.Code, rr.Body.String())
	}

	// Try to fill same slot again
	partBody = bytes.NewReader([]byte(`{"name":"Part B","slot_id":1}`))
	req = httptest.NewRequest(http.MethodPost, "/parts", partBody)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("second CreatePart: expected 400, got %d body: %s", rr.Code, rr.Body.String())
	}
}

func TestCreateSlot(t *testing.T) {
	r, sqlDB := newTestRouter(t)
	defer sqlDB.Close()

	// Create machine (9 default slots)
	body := bytes.NewReader([]byte(`{"name":"test"}`))
	req := httptest.NewRequest(http.MethodPost, "/machines", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("CreateMachine: expected 201, got %d body: %s", rr.Code, rr.Body.String())
	}

	// POST a new slot
	slotBody := bytes.NewReader([]byte(`{"category":"NIC"}`))
	req = httptest.NewRequest(http.MethodPost, "/machines/1/slots", slotBody)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("CreateSlot: expected 201, got %d body: %s", rr.Code, rr.Body.String())
	}

	// GET machine — should have 10 slots, last is NIC
	req = httptest.NewRequest(http.MethodGet, "/machines/1", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GetMachine: expected 200, got %d body: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Slots []SlotResponse `json:"slots"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Slots) != 10 {
		t.Fatalf("expected 10 slots, got %d", len(resp.Slots))
	}
	if resp.Slots[9].Category != "NIC" {
		t.Fatalf("expected last slot category 'NIC', got %q", resp.Slots[9].Category)
	}
	if resp.Slots[9].Part != nil {
		t.Fatalf("expected last slot to be empty, got part %v", resp.Slots[9].Part)
	}

	// POST with empty category → 400
	emptyBody := bytes.NewReader([]byte(`{"category":""}`))
	req = httptest.NewRequest(http.MethodPost, "/machines/1/slots", emptyBody)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("CreateSlot with empty category: expected 400, got %d body: %s", rr.Code, rr.Body.String())
	}
}

func TestDeleteSlotUnassignsPart(t *testing.T) {
	r, sqlDB := newTestRouter(t)
	defer sqlDB.Close()

	// Create machine
	body := bytes.NewReader([]byte(`{"name":"test"}`))
	req := httptest.NewRequest(http.MethodPost, "/machines", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("CreateMachine: expected 201, got %d body: %s", rr.Code, rr.Body.String())
	}

	// Fill slot 1 with a part
	partBody := bytes.NewReader([]byte(`{"name":"Part A","slot_id":1}`))
	req = httptest.NewRequest(http.MethodPost, "/parts", partBody)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("CreatePart: expected 201, got %d body: %s", rr.Code, rr.Body.String())
	}

	// DELETE slot 1
	req = httptest.NewRequest(http.MethodDelete, "/slots/1", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("DeleteSlot: expected 204, got %d body: %s", rr.Code, rr.Body.String())
	}

	// GET machine — should have 8 slots
	req = httptest.NewRequest(http.MethodGet, "/machines/1", nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GetMachine: expected 200, got %d body: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Slots []SlotResponse `json:"slots"`
		Parts []PartResponse `json:"parts"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Slots) != 8 {
		t.Fatalf("expected 8 slots after delete, got %d", len(resp.Slots))
	}

	// Part A should still exist but be unassigned
	found := false
	for _, p := range resp.Parts {
		if p.Name == "Part A" {
			found = true
			if p.SlotID != nil {
				t.Fatalf("expected Part A to be unassigned (slot_id nil), got slot_id %d", *p.SlotID)
			}
		}
	}
	if !found {
		t.Fatal("Part A not found in parts list after slot deletion")
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}
