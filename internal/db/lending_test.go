package db_test

// Tests for lending: which parts friends may borrow, who wants a spare, and
// what's on loan.

import (
	"database/sql"
	"testing"

	"racket/internal/db"
)

// The parts that were spare or lent out before lending was opt-in stay
// borrowable; the rest don't become borrowable.
func TestLendingMigrationBackfill(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	p := newProvider(t, sqlDB)
	if _, err := p.UpTo(t.Context(), 20261002000000); err != nil {
		t.Fatal(err)
	}
	exec(t, sqlDB, `
		INSERT INTO users (id, discord_id, username) VALUES (1, '100', 'alice'), (2, '200', 'bob');
		INSERT INTO machines (id, owner_id, name) VALUES (1, 1, 'alice-pc');
		INSERT INTO machine_slots (id, machine_id, category) VALUES (1, 1, 'CPU'), (2, 1, 'GPU');
		INSERT INTO parts (id, owner_id, slot_id, status, name) VALUES
		    (1, 1, NULL, 'owned',   'spare'),
		    (2, 1, NULL, 'wanted',  'not bought'),
		    (3, 1, NULL, 'retired', 'retired'),
		    (4, 1, 1,    'owned',   'installed'),
		    (5, 2, 2,    'owned',   'lent to alice');
	`)
	if _, err := p.Up(t.Context()); err != nil {
		t.Fatal(err)
	}

	want := map[int64]bool{1: true, 2: false, 3: false, 4: false, 5: true}
	for id, lendable := range want {
		if got := count(t, sqlDB, `SELECT lendable FROM parts WHERE id = ?`, id) == 1; got != lendable {
			t.Errorf("part %d lendable = %v, want %v", id, got, lendable)
		}
	}
}

func TestSetPartLendable(t *testing.T) {
	tests := []struct {
		name string
		user int64
		part int64
		want bool
	}{
		{name: "owner lends a bought part", user: alice, part: aliceCPU, want: true},
		{name: "a part nobody bought can't be lent", user: alice, part: aliceWantedPSU},
		{name: "only the owner can lend it", user: bob, part: aliceCPU},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB, q := newSeededDB(t)
			n, err := q.SetPartLendable(t.Context(), db.SetPartLendableParams{Lendable: true, ID: tt.part, UserID: tt.user})
			if err != nil {
				t.Fatal(err)
			}
			if got := n == 1; got != tt.want {
				t.Fatalf("allowed = %v, want %v", got, tt.want)
			}
			lendable := count(t, sqlDB, `SELECT lendable FROM parts WHERE id = ?`, tt.part) == 1
			if lendable != tt.want {
				t.Fatalf("lendable = %v, want %v", lendable, tt.want)
			}
		})
	}
}

func TestDeleteSparePart(t *testing.T) {
	tests := []struct {
		name string
		user int64
		part int64
		want bool
	}{
		{name: "owner deletes a spare", user: alice, part: aliceCPU, want: true},
		{name: "a lent part stays in the friend's machine", user: bob, part: bobSpareGPU},
		{name: "an installed part isn't a spare", user: bob, part: bobInstalledCPU},
		{name: "only the owner can delete it", user: bob, part: aliceCPU},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB, q := newSeededDB(t)
			exec(t, sqlDB, lendBobsGPUToAlice)
			n, err := q.DeleteSparePart(t.Context(), db.DeleteSparePartParams{ID: tt.part, OwnerID: tt.user})
			if err != nil {
				t.Fatal(err)
			}
			if got := n == 1; got != tt.want {
				t.Fatalf("deleted = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestListSpareDemand(t *testing.T) {
	sqlDB, q := newSeededDB(t)
	// Alice plans two of Bob's spare GPU (names match ignoring case and
	// outer spaces), Carol one, and Bob one for himself.
	exec(t, sqlDB, `INSERT INTO parts (owner_id, slot_id, status, name) VALUES
		(1, 1, 'wanted', ' rtx 3080 '),
		(1, 3, 'wanted', 'RTX 3080'),
		(3, 7, 'wanted', 'RTX 3080'),
		(2, 6, 'wanted', 'RTX 3080')`)

	type wanter struct {
		user    string
		machine string
	}
	demand := func() []wanter {
		t.Helper()
		rows, err := q.ListSpareDemand(t.Context(), bob)
		if err != nil {
			t.Fatal(err)
		}
		var got []wanter
		for _, r := range rows {
			if r.PartID != bobSpareGPU {
				t.Errorf("demand for part %d, want only Bob's spare GPU", r.PartID)
			}
			got = append(got, wanter{r.Username, r.MachineName})
		}
		return got
	}

	got := demand()
	want := []wanter{{"alice", "alice-pc"}, {"carol", "carol-pc"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("demand = %v, want %v", got, want)
	}

	exec(t, sqlDB, `UPDATE users SET deleted_at = CURRENT_TIMESTAMP WHERE id = 3`)
	if got := demand(); len(got) != 1 || got[0] != want[0] {
		t.Errorf("after Carol left, demand = %v, want only Alice", got)
	}

	// A spare on loan isn't spare anymore.
	exec(t, sqlDB, `UPDATE parts SET slot_id = 2 WHERE id = 20`)
	if got := demand(); len(got) != 0 {
		t.Errorf("demand for a lent part = %v, want none", got)
	}
}

func TestLentAndBorrowedParts(t *testing.T) {
	sqlDB, q := newSeededDB(t)
	exec(t, sqlDB, lendBobsGPUToAlice)
	exec(t, sqlDB, installAliceCPU)

	lent, err := q.ListLentParts(t.Context(), bob)
	if err != nil {
		t.Fatal(err)
	}
	if len(lent) != 1 || lent[0].ID != bobSpareGPU ||
		lent[0].BorrowerName != "alice" || lent[0].MachineName != "alice-pc" {
		t.Fatalf("Bob's lent parts = %+v, want his GPU in alice-pc", lent)
	}

	bobLeaves(t, q)
	borrowed, err := q.ListBorrowedParts(t.Context(), alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(borrowed) != 1 || borrowed[0].ID != bobSpareGPU || !borrowed[0].LenderLeftAt.Valid {
		t.Fatalf("Alice's borrowed parts = %+v, want Bob's GPU, from a former member", borrowed)
	}
	if lent, _ := q.ListLentParts(t.Context(), alice); len(lent) != 0 {
		t.Errorf("Alice's lent parts = %+v, want none (her CPU is in her own machine)", lent)
	}
}
