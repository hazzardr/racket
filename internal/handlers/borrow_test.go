package handlers

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"racket/internal/db"
)

// friendsTab sets up the friends' tab: Bob lends a GPU and a CPU, Carol has
// a GPU she doesn't lend, and Alice has an empty machine and a plan with
// Bob's GPU not bought yet. It returns Alice's cookie and Bob's GPU.
func friendsTab(t *testing.T) (*testApp, *http.Cookie, db.Part) {
	t.Helper()
	app := newTestApp(t)
	alice := app.login("alice")
	app.login("bob")
	app.login("carol")
	part := func(owner, name, category string, lend bool) db.Part {
		p, err := app.queries.CreatePart(t.Context(), db.CreatePartParams{
			OwnerID: app.user(owner).ID, Status: partOwned, Name: name, Lendable: lend,
			Category: sql.NullString{String: category, Valid: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	gpu := part("bob", "NVIDIA GeForce RTX 3080", "Video Card", true)
	part("bob", "AMD Ryzen 5 7600", "CPU", true)
	part("carol", "Radeon RX 6600", "Video Card", false)

	app.createMachine(alice, "alice-pc")
	plan := app.createMachine(alice, "alice-plan")
	form := url.Values{"name": {"nvidia geforce rtx 3080"}, "wanted": {"on"}}
	assertStatus(t, app.post(alice, slotPath(app.slots(plan)[6].ID, "/part"), form), http.StatusSeeOther)
	return app, alice, gpu
}

func TestFriendsTab(t *testing.T) {
	app, alice, _ := friendsTab(t)

	rr := app.get(alice, "/parts?view=friends")
	assertStatus(t, rr, http.StatusOK)
	assertContains(t, rr,
		`aria-current="page">Friends'</a>`,
		"NVIDIA GeForce RTX 3080", "AMD Ryzen 5 7600", ">bob</a>'s",
		"matches your plan", "Matches your <a href=\"/machines/2\">alice-plan</a> GPU",
		// The plan's slot comes first, then the empty GPU slots.
		`<option value="16">alice-plan · GPU, instead of buying</option><option value="7">alice-pc · GPU (empty)</option>`,
		`>CPU <span>1</span></a>`, `>GPU <span>1</span></a>`)
	assertNotContains(t, rr, "Radeon RX 6600")

	// Video cards are GPUs here, as in slots.
	rr = app.get(alice, "/parts?view=friends&cat=gpu")
	assertContains(t, rr, "NVIDIA GeForce RTX 3080", `aria-current="true"><i></i>GPU`)
	assertNotContains(t, rr, "AMD Ryzen 5 7600")

	// Your own tab is the default.
	assertContains(t, app.get(alice, partsPath), `aria-current="page">Yours</a>`)
}

func TestBorrowFromFriendsTab(t *testing.T) {
	app, alice, gpu := friendsTab(t)
	planGPU := app.slots(2)[6]
	borrow := func(slotID int64, htmx bool) *httptest.ResponseRecorder {
		form := url.Values{"slot_id": {strconv.FormatInt(slotID, 10)}, "cat": {"gpu"}}
		return app.do(alice, http.MethodPost, partPath(gpu.ID, "/borrow"), form, htmx)
	}

	// Slots the tab doesn't offer: Bob's machine, and a CPU slot.
	bobPC := app.createMachine(app.login("bob"), "bob-pc")
	assertStatus(t, borrow(app.slots(bobPC)[6].ID, false), http.StatusUnprocessableEntity)
	assertStatus(t, borrow(app.slots(1)[1].ID, false), http.StatusConflict)
	// htmx gets the error with a 200, so it swaps in.
	assertContains(t, borrow(app.slots(1)[1].ID, true), `role="alert">That part can&#39;t go there anymore.`)
	if p, _ := app.part(gpu.ID); p.SlotID.Valid {
		t.Fatalf("Bob's GPU moved to slot %v", p.SlotID)
	}

	rr := borrow(planGPU.ID, true)
	assertStatus(t, rr, http.StatusOK)
	assertContains(t, rr,
		`NVIDIA GeForce RTX 3080 is in your <a href="/machines/2">alice-plan</a> now, instead of buying one.`)
	if slot := app.slots(2)[6]; slot.PartID.Int64 != gpu.ID || slot.PartSource != sourceBorrowed {
		t.Fatalf("plan's GPU slot = %+v, want Bob's GPU, borrowed", slot)
	}
	if _, ok := app.part(planGPU.PartID.Int64); ok {
		t.Error("the GPU Alice planned to buy is still around")
	}

	// It's taken now.
	rr = borrow(app.slots(1)[6].ID, false)
	assertStatus(t, rr, http.StatusConflict)
	assertContains(t, rr, "That part can&#39;t go there anymore.")
}

func TestBorrowFromFriendsTabNative(t *testing.T) {
	app, alice, gpu := friendsTab(t)
	form := url.Values{"slot_id": {strconv.FormatInt(app.slots(1)[6].ID, 10)}, "cat": {"gpu"}}
	rr := app.post(alice, partPath(gpu.ID, "/borrow"), form)
	assertStatus(t, rr, http.StatusSeeOther)
	if loc := rr.Header().Get("Location"); loc != "/parts?cat=gpu&view=friends" {
		t.Errorf("redirected to %q, want the friends' tab, still on GPUs", loc)
	}
}
