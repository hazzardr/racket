package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"racket/internal/auth"
	"racket/internal/db"
)

func TestAppRequiresLogin(t *testing.T) {
	app := newTestApp(t)

	for _, path := range []string{"/machines", "/machines/new", "/machines/1", "/import"} {
		rr := app.get(nil, path)
		assertStatus(t, rr, http.StatusSeeOther)
		if loc := rr.Header().Get("Location"); loc != auth.LoginPath {
			t.Errorf("GET %s redirected to %q, want %s", path, loc, auth.LoginPath)
		}
	}
	rr := app.postHX(nil, "/machines", url.Values{"name": {"x"}})
	if loc := rr.Header().Get("Hx-Redirect"); loc != auth.LoginPath {
		t.Errorf("htmx POST /machines: Hx-Redirect %q, want %s", loc, auth.LoginPath)
	}
	for _, path := range []string{"/", auth.LoginPath, "/static/app.css"} {
		assertStatus(t, app.get(nil, path), http.StatusOK)
	}
}

func TestCreateMachineSeedsSlots(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")

	id := app.createMachine(alice, "test")

	slots := app.slots(id)
	if len(slots) != len(defaultSlots) {
		t.Fatalf("expected %d slots, got %d", len(defaultSlots), len(slots))
	}
	for i, slot := range slots {
		if slot.PartID.Valid {
			t.Errorf("expected slot %d to be empty, got part %v", slot.ID, slot.PartID)
		}
		if slot.Category != defaultSlots[i] {
			t.Errorf("slot %d = %q, want %q", i, slot.Category, defaultSlots[i])
		}
	}
	m, err := app.queries.GetMachine(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if m.OwnerName != "alice" || m.Machine.Kind != "desktop" || m.Machine.Status != "active" {
		t.Errorf("machine = %+v owner %q, want alice's active desktop", m.Machine, m.OwnerName)
	}
}

func TestCreatePlannedServer(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")

	rr := app.post(alice, "/machines", url.Values{"name": {"nas"}, "kind": {"nas"}, "status": {"planned"}})
	assertStatus(t, rr, http.StatusSeeOther)

	rr = app.get(alice, "/machines")
	assertContains(t, rr, "nas", `<span class="badge">planned</span>`, "yours nas")
}

func TestCreateMachineHXRedirect(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")

	rr := app.postHX(alice, "/machines", url.Values{"name": {"hx"}})
	assertStatus(t, rr, http.StatusOK)
	if loc := rr.Header().Get("Hx-Redirect"); loc != "/machines/1" {
		t.Fatalf("expected HX-Redirect /machines/1, got %q", loc)
	}
}

func TestCreateMachineValidation(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")

	tests := []struct {
		name string
		form url.Values
		want string
	}{
		{"name required", url.Values{"name": {""}}, "Name is required."},
		{"unknown kind", url.Values{"name": {"x"}, "kind": {"toaster"}}, "Pick a kind from the list."},
		{"unknown status", url.Values{"name": {"x"}, "status": {"lost"}}, "Pick a status from the list."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := app.post(alice, "/machines", tt.form)
			assertStatus(t, rr, http.StatusUnprocessableEntity)
			assertContains(t, rr, tt.want)
		})
	}
}

func TestCreateMachineDuplicateName(t *testing.T) {
	app := newTestApp(t)
	alice, bob := app.login("alice"), app.login("bob")

	app.createMachine(alice, "box")
	rr := app.post(alice, "/machines", url.Values{"name": {"box"}, "location": {"closet"}})
	assertStatus(t, rr, http.StatusUnprocessableEntity)
	assertContains(t, rr, "You already have a machine named", `value="closet"`)

	// Names are per owner: Bob can have a "box" too.
	app.createMachine(bob, "box")
}

func TestDeleteMachine(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")
	id := app.createMachine(alice, "doomed")

	rr := app.post(alice, machinePath(id, "/delete"), nil)
	assertStatus(t, rr, http.StatusSeeOther)
	if loc := rr.Header().Get("Location"); loc != "/machines" {
		t.Fatalf("expected redirect to /machines, got %q", loc)
	}
	if _, err := app.queries.GetMachine(t.Context(), id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected machine %d to be deleted, got err=%v", id, err)
	}
	if slots := app.slots(id); len(slots) != 0 {
		t.Fatalf("expected 0 slots after machine delete, got %d", len(slots))
	}
}

func TestAddPart(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")
	machineID := app.createMachine(alice, "test")
	slotID := app.slots(machineID)[0].ID

	rr := app.postHX(alice, slotPath(slotID, "/part"), url.Values{
		"name":         {"B550 Tomahawk"},
		"manufacturer": {"MSI"},
		"model":        {"B550"},
		"price":        {"$149.99"},
	})
	assertStatus(t, rr, http.StatusOK)
	assertContains(t, rr, "B550 Tomahawk", "$149.99", "slot-own", ">Remove<")
	assertNotContains(t, rr, "not bought yet")

	slot := app.slots(machineID)[0]
	if !slot.PartID.Valid || slot.PartName.String != "B550 Tomahawk" || slot.PartPriceCents.Int64 != 14999 {
		t.Fatalf("slot = %+v, want B550 Tomahawk at 14999 cents", slot)
	}
	if p, _ := app.part(slot.PartID.Int64); p.OwnerID != app.user("alice").ID || p.Status != "owned" {
		t.Errorf("part = %+v, want alice's, owned", p)
	}
}

func TestAddUnboughtPart(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")
	machineID := app.createMachine(alice, "plan")
	slotID := app.slots(machineID)[0].ID

	rr := app.postHX(alice, slotPath(slotID, "/part"), url.Values{"name": {"9800X3D"}, "wanted": {"on"}})
	assertStatus(t, rr, http.StatusOK)
	assertContains(t, rr, "slot-unpurchased", "not bought yet")
	if p, _ := app.part(app.slots(machineID)[0].PartID.Int64); p.Status != "wanted" {
		t.Errorf("status = %q, want wanted", p.Status)
	}
}

func TestDoubleFillSlotRejected(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")
	machineID := app.createMachine(alice, "test")
	path := slotPath(app.slots(machineID)[0].ID, "/part")

	assertStatus(t, app.postHX(alice, path, url.Values{"name": {"Part A"}}), http.StatusOK)

	// Second fill: 200 with the error embedded in the fragment (htmx swaps on 200).
	rr := app.postHX(alice, path, url.Values{"name": {"Part B"}})
	assertStatus(t, rr, http.StatusOK)
	assertContains(t, rr, "Slot already filled.", "Part A")
}

func TestRemoveOwnPartDeletesIt(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")
	machineID := app.createMachine(alice, "test")
	slotID := app.slots(machineID)[0].ID
	assertStatus(t, app.postHX(alice, slotPath(slotID, "/part"), url.Values{"name": {"Part A"}}), http.StatusOK)
	partID := app.slots(machineID)[0].PartID.Int64

	rr := app.postHX(alice, slotPath(slotID, "/part/delete"), nil)
	assertStatus(t, rr, http.StatusOK)
	assertContains(t, rr, `placeholder="Part name"`)
	if _, ok := app.part(partID); ok {
		t.Error("part still exists")
	}
}

func TestBorrowedParts(t *testing.T) {
	tests := []struct {
		name       string
		actor      string
		wantStatus int
		wantBody   string
		returned   bool
	}{
		{
			name: "borrower returns it", actor: "alice",
			wantStatus: http.StatusOK, wantBody: `placeholder="Part name"`, returned: true,
		},
		{name: "lender takes it back", actor: "bob", wantStatus: http.StatusOK, wantBody: "Empty", returned: true},
		{name: "outsider can't touch it", actor: "carol", wantStatus: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newTestApp(t)
			cookies := map[string]*http.Cookie{
				"alice": app.login("alice"), "bob": app.login("bob"), "carol": app.login("carol"),
			}
			machineID := app.createMachine(cookies["alice"], "alice-pc")
			gpuSlot := app.slots(machineID)[6]
			gpu := app.sparePart(app.user("bob"), "RTX 3080")
			app.borrow(app.user("alice"), gpu, gpuSlot.ID)

			page := app.get(cookies["alice"], machinePath(machineID, ""))
			assertContains(t, page, "slot-borrowed", "from bob", ">Return<")
			page = app.get(cookies["bob"], machinePath(machineID, ""))
			assertContains(t, page, ">Take back<")

			rr := app.postHX(cookies[tt.actor], slotPath(gpuSlot.ID, "/part/delete"), nil)
			assertStatus(t, rr, tt.wantStatus)
			if tt.wantBody != "" {
				assertContains(t, rr, tt.wantBody)
			}

			p, ok := app.part(gpu.ID)
			if !ok {
				t.Fatal("Bob's GPU was deleted")
			}
			if p.OwnerID != app.user("bob").ID {
				t.Errorf("GPU owner = %d, want Bob", p.OwnerID)
			}
			if returned := !p.SlotID.Valid; returned != tt.returned {
				t.Errorf("GPU back in Bob's inventory = %v, want %v", returned, tt.returned)
			}
		})
	}
}

func TestFormerMemberPartLabel(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")
	machineID := app.createMachine(alice, "alice-pc")
	gpu := app.sparePart(app.user("bob"), "RTX 3080")
	app.borrow(app.user("alice"), gpu, app.slots(machineID)[6].ID)
	if _, err := app.queries.SoftDeleteUser(t.Context(), app.user("bob").ID); err != nil {
		t.Fatal(err)
	}

	rr := app.get(alice, machinePath(machineID, ""))
	assertContains(t, rr, "slot-former_member", "from a former member", "RTX 3080")
	assertNotContains(t, rr, "from bob")
}

func TestCreateSlot(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")
	machineID := app.createMachine(alice, "test")
	path := machinePath(machineID, "/slots")

	rr := app.postHX(alice, path, url.Values{"category": {"NIC"}})
	assertStatus(t, rr, http.StatusOK)
	assertContains(t, rr, "NIC", `placeholder="Part name"`)

	slots := app.slots(machineID)
	if last := slots[len(slots)-1]; len(slots) != len(defaultSlots)+1 || last.Category != "NIC" || last.PartID.Valid {
		t.Fatalf("slots = %d, last %+v; want an empty NIC slot appended", len(slots), last)
	}

	assertStatus(t, app.postHX(alice, path, url.Values{"category": {""}}), http.StatusBadRequest)
}

func TestDeleteSlotKeepsPart(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")
	machineID := app.createMachine(alice, "test")
	slotID := app.slots(machineID)[0].ID
	assertStatus(t, app.postHX(alice, slotPath(slotID, "/part"), url.Values{"name": {"Part A"}}), http.StatusOK)
	partID := app.slots(machineID)[0].PartID.Int64

	assertStatus(t, app.postHX(alice, slotPath(slotID, "/delete"), nil), http.StatusOK)

	if slots := app.slots(machineID); len(slots) != len(defaultSlots)-1 {
		t.Fatalf("expected %d slots after delete, got %d", len(defaultSlots)-1, len(slots))
	}
	p, ok := app.part(partID)
	if !ok || p.SlotID.Valid {
		t.Fatalf("part = %+v (exists %v), want it back in inventory", p, ok)
	}
}

// htmx slot actions must swap fragments in place: an Hx-Redirect header would
// make htmx navigate instead and discard the fragment.
func TestSlotHXResponsesHaveNoRedirect(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")
	machineID := app.createMachine(alice, "test")
	slotID := app.slots(machineID)[0].ID

	steps := []struct {
		path string
		data url.Values
	}{
		{machinePath(machineID, "/slots"), url.Values{"category": {"NIC"}}},
		{slotPath(slotID, "/part"), url.Values{"name": {"Part A"}}},
		{slotPath(slotID, "/part/delete"), nil},
		{slotPath(slotID, "/delete"), nil},
	}
	for _, s := range steps {
		rr := app.postHX(alice, s.path, s.data)
		assertStatus(t, rr, http.StatusOK)
		if loc := rr.Header().Get("Hx-Redirect"); loc != "" {
			t.Errorf("%s: unexpected Hx-Redirect %q", s.path, loc)
		}
	}
}

func TestFriendsCanViewButNotChange(t *testing.T) {
	app := newTestApp(t)
	alice, bob := app.login("alice"), app.login("bob")
	machineID := app.createMachine(alice, "alice-pc")
	slots := app.slots(machineID)
	assertStatus(t, app.postHX(alice, slotPath(slots[0].ID, "/part"), url.Values{"name": {"B550"}}), http.StatusOK)

	list := app.get(bob, "/machines")
	assertStatus(t, list, http.StatusOK)
	assertContains(t, list, "alice-pc", "alice's desktop")
	assertNotContains(t, list, "Delete</button>")

	page := app.get(bob, machinePath(machineID, ""))
	assertStatus(t, page, http.StatusOK)
	assertContains(t, page, "B550", "alice's desktop")
	assertNotContains(t, page, "Delete machine", "Add slot", `placeholder="Part name"`, "Remove slot", ">Remove<")

	for _, attempt := range []struct {
		path string
		form url.Values
	}{
		{machinePath(machineID, "/delete"), nil},
		{machinePath(machineID, "/slots"), url.Values{"category": {"NIC"}}},
		{slotPath(slots[1].ID, "/part"), url.Values{"name": {"sneaky"}}},
		{slotPath(slots[1].ID, "/delete"), nil},
		{slotPath(slots[0].ID, "/part/delete"), nil},
	} {
		rr := app.postHX(bob, attempt.path, attempt.form)
		if rr.Code != http.StatusForbidden {
			t.Errorf("Bob POST %s: status %d, want 403", attempt.path, rr.Code)
		}
	}

	after := app.slots(machineID)
	if len(after) != len(slots) || !after[0].PartID.Valid || after[1].PartID.Valid {
		t.Errorf("Alice's machine changed: %+v", after)
	}
}

func TestFormerMembersMachinesAreHidden(t *testing.T) {
	app := newTestApp(t)
	alice, bob := app.login("alice"), app.login("bob")
	machineID := app.createMachine(bob, "bob-pc")
	if _, err := app.queries.SoftDeleteUser(t.Context(), app.user("bob").ID); err != nil {
		t.Fatal(err)
	}

	assertStatus(t, app.get(alice, machinePath(machineID, "")), http.StatusNotFound)
	assertNotContains(t, app.get(alice, "/machines"), "bob-pc")
	// Bob's session ended when he left.
	assertStatus(t, app.get(bob, "/machines"), http.StatusSeeOther)
}

// Internal errors are logged, not shown: the page must not leak driver text.
func TestServerErrorHidesDetails(t *testing.T) {
	sqlDB := newTestDB(t)
	h := New(sqlDB, db.New(sqlDB), newTestWeb(t), Auth{})
	sqlDB.Close() // force every query to fail with "sql: database is closed"

	req := httptest.NewRequest(http.MethodGet, "/machines", nil)
	req = req.WithContext(auth.WithUser(req.Context(), db.User{ID: 1, Username: "alice"}))
	rr := httptest.NewRecorder()
	h.MachinesPage(rr, req)
	assertStatus(t, rr, http.StatusInternalServerError)
	body := rr.Body.String()
	if strings.Contains(body, "sql:") || strings.Contains(body, "closed") {
		t.Fatalf("error page leaked internal error text: %s", body)
	}
	assertContains(t, rr, "Something went wrong")
}
