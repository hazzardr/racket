package main

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"racket/internal/db"
)

func TestRemoveUser(t *testing.T) {
	sqlDB := newTestDB(t)
	q := db.New(sqlDB)
	ctx := t.Context()
	users := map[string]db.User{}
	for _, u := range []struct{ id, name string }{{"1", "alice"}, {"2", "bob"}, {"3", "sam"}, {"4", "sam"}} {
		row, err := q.UpsertDiscordUser(ctx, db.UpsertDiscordUserParams{DiscordID: u.id, Username: u.name})
		if err != nil {
			t.Fatal(err)
		}
		users[u.id] = row
	}
	session := []byte("bob's session")
	if err := q.CreateSession(ctx, db.CreateSessionParams{
		TokenHash: session, UserID: users["2"].ID, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	current := func() int {
		t.Helper()
		var n int
		if err := sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE deleted_at IS NULL`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if u, err := removeUser(ctx, sqlDB, "bob"); err != nil || u.ID != users["2"].ID {
		t.Fatalf("remove bob: %+v, %v", u, err)
	}
	_, err := q.GetSessionUser(ctx, db.GetSessionUserParams{TokenHash: session, Now: time.Now()})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("bob's session: err = %v, want it gone", err)
	}
	if _, err = removeUser(ctx, sqlDB, "bob"); err == nil || !strings.Contains(err.Error(), "no current member") {
		t.Errorf("removing bob twice: err = %v", err)
	}

	// Two members named sam: ask for a Discord ID, and remove no one.
	_, err = removeUser(ctx, sqlDB, "sam")
	if err == nil || !strings.Contains(err.Error(), "3, 4") {
		t.Errorf("ambiguous name: err = %v, want both Discord IDs listed", err)
	}
	if n := current(); n != 3 {
		t.Errorf("%d current members after an ambiguous remove, want 3", n)
	}
	if u, err := removeUser(ctx, sqlDB, "4"); err != nil || u.ID != users["4"].ID {
		t.Fatalf("remove by Discord ID: %+v, %v", u, err)
	}
	if n := current(); n != 2 {
		t.Errorf("%d current members, want alice and one sam", n)
	}
}

func TestRunCommandUsage(t *testing.T) {
	for _, args := range [][]string{{"bogus"}, {cmdRemoveUser}, {cmdRemoveUser, "a", "b"}} {
		if err := runCommand(t.Context(), args); !errors.Is(err, errUsage) {
			t.Errorf("runCommand(%q) = %v, want the usage", args, err)
		}
	}
}
