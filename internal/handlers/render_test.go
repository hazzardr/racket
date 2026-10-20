package handlers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"racket/internal/db"
	"racket/internal/web"
)

// Every page renders with its real data type and a signed-in user.
func TestRenderTemplates(t *testing.T) {
	w, err := web.New()
	if err != nil {
		t.Fatal(err)
	}
	user := &db.User{ID: 1, Username: "alice"}
	board := []slotCard{{CanEdit: true, State: stateEmpty}}

	pages := []struct {
		name string
		data any
	}{
		{"error.html", errorPage{Message: "test error"}},
		{"import.html", importPage{}},
		{"landing.html", landingPage{}},
		{"machines.html", machinesPage{Mine: []machineRow{{Mine: true}}, Others: []machineRow{{}}}},
		{"new_machine.html", newMachinePage{Kinds: machineKinds, Statuses: machineStatuses}},
		{"people.html", peoplePage{People: []db.ListMembersRow{{User: *user}}}},
		{"profile.html", profilePage{
			Member: *user, IsMe: true, Machines: []machineRow{{Mine: true}}, Lending: []inventoryPart{{}},
		}},
		{"parts.html", partsPage{View: viewFriends, Friends: friendsView{
			Groups: []friendGroup{{Category: "GPU", Spares: []friendSpare{
				{Targets: []borrowTarget{{Instead: true}}, Matches: []borrowTarget{{}}}, {},
			}}},
			Filters:  []categoryFilter{{Key: "gpu", Selected: true}},
			Borrowed: &borrowTarget{}, Error: "x",
		}}},
		{"parts.html", partsPage{
			Spares:   []inventoryPart{{Wanters: []wanter{{UserName: "bob"}}, Friends: 1}},
			Lent:     []loanRow{{Friend: "bob"}},
			Borrowed: []loanRow{{}},
		}},
		{"machine_detail.html", MachinePage{
			IsOwner: true, Slots: board, Left: board, Spares: []sparePart{{Mine: true}}, HasMine: true,
			Cats: []legendEntry{{Name: "CPU"}}, Counts: map[string]int{},
		}},
	}

	for _, p := range pages {
		t.Run(p.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			w.Render(rr, http.StatusOK, p.name, web.Page{User: user, Data: p.data})
			assertStatus(t, rr, http.StatusOK)
			assertContains(t, rr, "<!doctype html>", "</html>", `class="nav-user">alice</span>`)
		})
	}
}

// Each page names itself in the browser tab.
func TestPageTitles(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")
	desk := app.createMachine(alice, "Desk PC")

	tests := []struct {
		path string
		want string
	}{
		{"/", "Racket — Hardware Tracker"},
		{"/machines", "Machines · Racket"},
		{"/machines/new", "New machine · Racket"},
		{"/machines/" + strconv.FormatInt(desk, 10), "Desk PC · Racket"},
		{"/parts", "Parts · Racket"},
		{"/people", "People · Racket"},
		{"/users/" + strconv.FormatInt(app.user("alice").ID, 10), "alice · Racket"},
		{"/import", "Import from PCPartPicker · Racket"},
		{"/machines/999", "Something went wrong · Racket"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			assertContains(t, app.get(alice, tt.path), "<title>"+tt.want+"</title>")
		})
	}
}
