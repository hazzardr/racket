package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func partPath(partID int64, suffix string) string {
	return fmt.Sprintf("%s/%d%s", partsPath, partID, suffix)
}

func TestAddSparePart(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")

	rr := app.post(alice, partsPath, url.Values{
		"name": {"RTX 3080"}, "category": {"GPU"}, "price": {"399.99"}, "lend": {"on"},
	})
	assertStatus(t, rr, http.StatusSeeOther)
	rr = app.post(alice, partsPath, url.Values{"name": {"RM850x"}, "wanted": {"on"}, "lend": {"on"}})
	assertStatus(t, rr, http.StatusSeeOther)

	parts, err := app.queries.ListSparesByOwner(t.Context(), app.user("alice").ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 {
		t.Fatalf("got %d spares, want 2", len(parts))
	}
	for _, p := range parts {
		switch p.Name {
		case "RTX 3080":
			if p.Status != partOwned || !p.Lendable || p.PriceCents.Int64 != 39999 || p.Category.String != "GPU" {
				t.Errorf("GPU = %+v, want a bought, lendable GPU at 39999", p)
			}
		case "RM850x":
			// Nobody can borrow a part that isn't bought yet.
			if p.Status != partWanted || p.Lendable {
				t.Errorf("PSU = %+v, want not bought and not lendable", p)
			}
		}
	}

	rr = app.get(alice, partsPath)
	assertStatus(t, rr, http.StatusOK)
	assertContains(t, rr, "RTX 3080", "$399.99", "friends can borrow", "Stop lending", "RM850x", "not bought")
}

func TestAddSparePartValidation(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")

	rr := app.post(alice, partsPath, url.Values{"name": {""}, "model": {"5700X"}})
	assertStatus(t, rr, http.StatusUnprocessableEntity)
	assertContains(t, rr, "Part name is required.", `value="5700X"`, "<details class=\"panel add-spare\" open>")

	rr = app.postHX(alice, partsPath, url.Values{"name": {"x"}, "price": {"cheap"}})
	assertStatus(t, rr, http.StatusOK)
	assertContains(t, rr, "Price should be a number", `id="parts-root"`)
	assertNotContains(t, rr, "<!doctype html>")
}

func TestLendingASpare(t *testing.T) {
	app := newTestApp(t)
	alice, bob := app.login("alice"), app.login("bob")
	bobPC := app.createMachine(bob, "bob-pc")
	assertStatus(t, app.post(alice, partsPath, url.Values{"name": {"RTX 3080"}, "category": {"GPU"}}), http.StatusSeeOther)
	gpu, _ := app.queries.ListSparesByOwner(t.Context(), app.user("alice").ID)
	gpuID := gpu[0].ID
	gpuSlot := slotPath(app.slots(bobPC)[6].ID, "/part/place")
	place := url.Values{"part_id": {strconv.FormatInt(gpuID, 10)}}

	// Not lent yet: Bob doesn't see it and can't take it.
	assertNotContains(t, app.get(bob, machinePath(bobPC, "")), "RTX 3080")
	assertContains(t, app.postHX(bob, gpuSlot, place), "That part can&#39;t go in this slot.")

	// Only Alice can lend it.
	assertStatus(t, app.postHX(bob, partPath(gpuID, "/lend"), url.Values{"lendable": {"1"}}), http.StatusForbidden)
	rr := app.postHX(alice, partPath(gpuID, "/lend"), url.Values{"lendable": {"1"}})
	assertStatus(t, rr, http.StatusOK)
	assertContains(t, rr, "Stop lending")

	assertContains(t, app.get(bob, machinePath(bobPC, "")), "RTX 3080", "<b>alice's</b>")
	assertStatus(t, app.postHX(bob, gpuSlot, place), http.StatusOK)
	if slot := app.slots(bobPC)[6]; slot.PartID.Int64 != gpuID || slot.PartSource != sourceBorrowed {
		t.Fatalf("Bob's GPU slot = %+v, want Alice's GPU, borrowed", slot)
	}

	// It shows as lent out for Alice and borrowed for Bob.
	rr = app.get(alice, partsPath)
	assertContains(t, rr, "Lent out", "in bob's <a href=\"/machines/", "Take back")
	rr = app.get(bob, partsPath)
	assertContains(t, rr, "from alice, in your <a href=\"/machines/", "Give back")

	// Stopping lending leaves it where it is.
	assertStatus(t, app.post(alice, partPath(gpuID, "/lend"), url.Values{"lendable": {"0"}}), http.StatusSeeOther)
	if p, _ := app.part(gpuID); p.SlotID.Int64 != app.slots(bobPC)[6].ID {
		t.Errorf("GPU moved to slot %v after Alice stopped lending it", p.SlotID)
	}
}

func TestReturnPart(t *testing.T) {
	for _, tt := range []struct {
		name  string
		actor string
		want  int
	}{
		{name: "borrower gives it back", actor: "bob", want: http.StatusSeeOther},
		{name: "lender takes it back", actor: "alice", want: http.StatusSeeOther},
		{name: "outsider can't", actor: "carol", want: http.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app := newTestApp(t)
			cookies := map[string]*http.Cookie{"alice": app.login("alice"), "bob": app.login("bob"), "carol": app.login("carol")}
			bobPC := app.createMachine(cookies["bob"], "bob-pc")
			gpu := app.sparePart(app.user("alice"), "RTX 3080")
			app.borrow(app.user("bob"), gpu, app.slots(bobPC)[6].ID)

			assertStatus(t, app.post(cookies[tt.actor], partPath(gpu.ID, "/return"), nil), tt.want)
			p, _ := app.part(gpu.ID)
			if returned, want := !p.SlotID.Valid, tt.want == http.StatusSeeOther; returned != want {
				t.Errorf("returned = %v, want %v", returned, want)
			}
		})
	}
}

func TestDeleteSparePart(t *testing.T) {
	app := newTestApp(t)
	alice, bob := app.login("alice"), app.login("bob")
	bobPC := app.createMachine(bob, "bob-pc")
	spare := app.sparePart(app.user("alice"), "RX 6600")
	lent := app.sparePart(app.user("alice"), "RTX 3080")
	app.borrow(app.user("bob"), lent, app.slots(bobPC)[6].ID)

	assertStatus(t, app.post(bob, partPath(spare.ID, "/delete"), nil), http.StatusForbidden)
	// A lent part stays in Bob's machine.
	assertStatus(t, app.post(alice, partPath(lent.ID, "/delete"), nil), http.StatusForbidden)
	if _, ok := app.part(lent.ID); !ok {
		t.Fatal("Alice's lent GPU was deleted out of Bob's machine")
	}

	assertStatus(t, app.postHX(alice, partPath(spare.ID, "/delete"), nil), http.StatusOK)
	if _, ok := app.part(spare.ID); ok {
		t.Error("spare RX 6600 wasn't deleted")
	}
}

func TestSpareDemand(t *testing.T) {
	app := newTestApp(t)
	alice, bob, carol := app.login("alice"), app.login("bob"), app.login("carol")
	app.sparePart(app.user("alice"), "AMD Ryzen 7 5700X 3.4 GHz 8-Core Processor")
	app.sparePart(app.user("alice"), "Nobody wants this")
	for _, plan := range []struct {
		cookie  *http.Cookie
		machine string
	}{{bob, "bob-plan"}, {bob, "bob-nas"}, {carol, "carol-plan"}} {
		id := app.createMachine(plan.cookie, plan.machine)
		cpu := slotPath(app.slots(id)[1].ID, "/part")
		form := url.Values{"name": {"amd ryzen 7 5700x 3.4 ghz 8-core processor"}, "wanted": {"on"}}
		assertStatus(t, app.post(plan.cookie, cpu, form), http.StatusSeeOther)
	}

	rr := app.get(alice, partsPath)
	assertContains(t, rr,
		"2 friends want this",
		`bob's <a href="/machines/2">bob-nas</a>, bob's <a href="/machines/1">bob-plan</a>`,
		`carol's <a href="/machines/3">carol-plan</a>`)
	if n := strings.Count(rr.Body.String(), "want this"); n != 1 {
		t.Errorf(`"want this" shown %d times, want once (only the CPU is wanted)`, n)
	}
}
