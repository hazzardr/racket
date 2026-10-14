package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"racket/internal/db"
)

func profilePath(userID int64) string {
	return fmt.Sprintf("/users/%d", userID)
}

func TestPeoplePage(t *testing.T) {
	app := newTestApp(t)
	alice, bob := app.login("alice"), app.login("bob")
	app.login("carol")
	app.login("dave")
	app.createMachine(bob, "bob-pc")
	app.sparePart(app.user("bob"), "RTX 3080")
	if _, err := app.queries.SoftDeleteUser(t.Context(), app.user("dave").ID); err != nil {
		t.Fatal(err)
	}

	rr := app.get(alice, "/people")
	assertStatus(t, rr, http.StatusOK)
	assertContains(t, rr,
		`href="/users/1"`, `alice <span class="chip">you</span>`,
		"@bob", "1 machine · <b>1 spare to lend</b>",
		"@carol", "0 machines · nothing to lend")
	assertNotContains(t, rr, "@dave")

	// Search matches usernames and display names, ignoring case.
	if _, err := app.queries.UpsertDiscordUser(t.Context(), db.UpsertDiscordUserParams{
		DiscordID: devUserPrefix + "carol", Username: "carol", DisplayName: sql.NullString{String: "Carol B", Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	rr = app.get(alice, "/people?"+url.Values{"q": {"BOB"}}.Encode())
	assertContains(t, rr, "@bob")
	assertNotContains(t, rr, "@carol", "@alice")
	rr = app.get(alice, "/people?q=carol+b")
	assertContains(t, rr, "Carol B", "@carol")
	rr = app.get(alice, "/people?q=zed")
	assertContains(t, rr, "Nobody matches “zed”.")
}

func TestProfilePage(t *testing.T) {
	app := newTestApp(t)
	alice, bob := app.login("alice"), app.login("bob")
	app.createMachine(bob, "bob-pc")
	app.createMachine(alice, "alice-pc")
	app.sparePart(app.user("bob"), "RTX 3080")
	if _, err := app.queries.CreatePart(t.Context(), db.CreatePartParams{
		OwnerID: app.user("bob").ID, Status: partOwned, Name: "Kept for himself",
	}); err != nil {
		t.Fatal(err)
	}
	bobID := app.user("bob").ID

	rr := app.get(alice, profilePath(bobID))
	assertStatus(t, rr, http.StatusOK)
	assertContains(t, rr, "@bob", "bob-pc", "Spares they lend", "RTX 3080", "spare-parts tray")
	assertNotContains(t, rr, "alice-pc", "Kept for himself", "Your parts")

	rr = app.get(bob, profilePath(bobID))
	assertContains(t, rr, "this is you", "Spares you lend", `href="/parts"`)

	assertStatus(t, app.get(alice, "/users/x"), http.StatusBadRequest)
	if _, err := app.queries.SoftDeleteUser(t.Context(), bobID); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, app.get(alice, profilePath(bobID)), http.StatusNotFound)
}
