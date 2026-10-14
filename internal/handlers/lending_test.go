package handlers

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"racket/internal/db"
)

func TestTakeOutAndLend(t *testing.T) {
	app := newTestApp(t)
	alice, bob := app.login("alice"), app.login("bob")
	alicePC := app.createMachine(alice, "alice-pc")
	bobPC := app.createMachine(bob, "bob-pc")
	gpu, psu := app.slots(alicePC)[6].ID, app.slots(alicePC)[7].ID
	assertStatus(t, app.post(alice, slotPath(gpu, "/part"), url.Values{"name": {"RTX 3080"}}), http.StatusSeeOther)
	assertStatus(t, app.post(alice, slotPath(psu, "/part"), url.Values{"name": {"RM850x"}, "wanted": {"on"}}),
		http.StatusSeeOther)

	page := app.get(alice, machinePath(alicePC, ""))
	if n := countOf(page, "Take out &amp; lend"); n != 1 {
		t.Errorf(`"Take out & lend" offered %d times, want once (not for the part not bought yet)`, n)
	}

	gpuID := app.slots(alicePC)[6].PartID.Int64
	assertStatus(t, app.postHX(alice, slotPath(gpu, "/part/takeout"), url.Values{"lend": {"1"}}), http.StatusOK)
	if p, _ := app.part(gpuID); p.SlotID.Valid {
		t.Fatalf("GPU still in slot %v", p.SlotID)
	}
	assertContains(t, app.get(bob, machinePath(bobPC, "")), "RTX 3080", "<b>alice's</b>")

	// A part nobody bought can't be lent, so it stays put.
	rr := app.postHX(alice, slotPath(psu, "/part/takeout"), url.Values{"lend": {"1"}})
	assertContains(t, rr, "Only parts you&#39;ve bought can be lent.")
	if slot := app.slots(alicePC)[7]; !slot.PartID.Valid {
		t.Error("the PSU not bought yet came out of its slot")
	}
}

func TestBorrowInsteadOfBuying(t *testing.T) {
	app := newTestApp(t)
	alice, bob := app.login("alice"), app.login("bob")
	app.login("carol")
	alicePC := app.createMachine(alice, "alice-pc")
	gpuSlot := app.slots(alicePC)[6].ID
	assertStatus(t, app.post(alice, slotPath(gpuSlot, "/part"), url.Values{"name": {"rtx 3080"}, "wanted": {"on"}}),
		http.StatusSeeOther)
	planned := app.slots(alicePC)[6].PartID.Int64
	bobs := app.sparePart(app.user("bob"), "RTX 3080")
	// Carol has one too, but doesn't lend it.
	if _, err := app.queries.CreatePart(t.Context(), db.CreatePartParams{
		OwnerID: app.user("carol").ID, Status: partOwned, Name: "RTX 3080",
	}); err != nil {
		t.Fatal(err)
	}

	page := app.get(alice, machinePath(alicePC, ""))
	assertContains(t, page, `<span class="match">bob has one</span>`, "Instead of buying", "Borrow it")
	assertNotContains(t, page, "carol")
	// Bob sees Alice's machine, but nothing to swap.
	assertNotContains(t, app.get(bob, machinePath(alicePC, "")), "Instead of buying")

	form := url.Values{"part_id": {strconv.FormatInt(bobs.ID, 10)}, "replace": {"1"}}
	assertStatus(t, app.postHX(alice, slotPath(gpuSlot, "/part/place"), form), http.StatusOK)
	if slot := app.slots(alicePC)[6]; slot.PartID.Int64 != bobs.ID || slot.PartSource != sourceBorrowed {
		t.Fatalf("GPU slot = %+v, want Bob's GPU, borrowed", slot)
	}
	if _, ok := app.part(planned); ok {
		t.Error("the GPU Alice planned to buy is still around")
	}
}

func TestOwnSpareComesFirst(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")
	app.login("bob")
	alicePC := app.createMachine(alice, "alice-pc")
	gpuSlot := app.slots(alicePC)[6].ID
	assertStatus(t, app.post(alice, slotPath(gpuSlot, "/part"), url.Values{"name": {"RTX 3080"}, "wanted": {"on"}}),
		http.StatusSeeOther)
	app.sparePart(app.user("bob"), "RTX 3080")
	// Alice's own spare isn't lendable, but she can still use it.
	if _, err := app.queries.CreatePart(t.Context(), db.CreatePartParams{
		OwnerID: app.user("alice").ID, Status: partOwned, Name: "RTX 3080",
	}); err != nil {
		t.Fatal(err)
	}

	page := app.get(alice, machinePath(alicePC, ""))
	assertContains(t, page, `<span class="match">you have one</span>`, "Your spare", "Use it", "bob's spare")
}
