package db_test

// Tests for the write queries' filters on the acting user and on where a
// part is: a write nobody is allowed to make changes 0 rows.

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"racket/internal/db"
)

// putAlicesPSUInSlot puts Alice's PSU, not bought yet, in her PSU slot.
const putAlicesPSUInSlot = `UPDATE parts SET slot_id = 4, price_cents = 10000 WHERE id = 11`

func TestPartWritesAreOwnerOnly(t *testing.T) {
	checked := sql.NullTime{Time: time.Now().UTC(), Valid: true}
	inPSUSlot := sql.NullInt64{Int64: alicePSUSlot, Valid: true}
	tests := []struct {
		name string
		// write runs the query as user and returns the rows it changed.
		write func(q *db.Queries, t *testing.T, user int64) (int64, error)
		// changed counts 1 once the write took effect on Alice's PSU.
		changed string
	}{
		{
			name: "DeletePart",
			write: func(q *db.Queries, t *testing.T, user int64) (int64, error) {
				return q.DeletePart(t.Context(), db.DeletePartParams{ID: aliceWantedPSU, OwnerID: user, SlotID: inPSUSlot})
			},
			changed: `SELECT count(*) = 0 FROM parts WHERE id = 11`,
		},
		{
			name: "MarkPartBought",
			write: func(q *db.Queries, t *testing.T, user int64) (int64, error) {
				return q.MarkPartBought(t.Context(), db.MarkPartBoughtParams{
					PriceCents: sql.NullInt64{Int64: 1, Valid: true}, ID: aliceWantedPSU, UserID: user,
				})
			},
			changed: `SELECT status = 'owned' FROM parts WHERE id = 11`,
		},
		{
			name: "RefreshPartPrice",
			write: func(q *db.Queries, t *testing.T, user int64) (int64, error) {
				return q.RefreshPartPrice(t.Context(), db.RefreshPartPriceParams{
					PriceCents: sql.NullInt64{Int64: 1, Valid: true}, CheckedAt: checked, ID: aliceWantedPSU, UserID: user,
				})
			},
			changed: `SELECT price_cents = 1 FROM parts WHERE id = 11`,
		},
		{
			name: "MarkPartMissingFromSource",
			write: func(q *db.Queries, t *testing.T, user int64) (int64, error) {
				return q.MarkPartMissingFromSource(t.Context(), db.MarkPartMissingFromSourceParams{
					CheckedAt: checked, ID: aliceWantedPSU, UserID: user,
				})
			},
			changed: `SELECT missing_from_source_at IS NOT NULL FROM parts WHERE id = 11`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB, q := newSeededDB(t)
			exec(t, sqlDB, putAlicesPSUInSlot)

			for _, user := range []int64{bob, alice} {
				n, err := tt.write(q, t, user)
				if err != nil {
					t.Fatal(err)
				}
				want := user == alice
				if got := n == 1; got != want {
					t.Fatalf("user %d: changed %d rows, want allowed = %v", user, n, want)
				}
				if got := count(t, sqlDB, tt.changed) == 1; got != want {
					t.Fatalf("user %d: part changed = %v, want %v", user, got, want)
				}
			}
		})
	}
}

// A write that names the part's slot doesn't touch the part once it has
// moved to another slot.
func TestPartWritesFollowTheSlot(t *testing.T) {
	sqlDB, q := newSeededDB(t)
	exec(t, sqlDB, putAlicesPSUInSlot)
	exec(t, sqlDB, lendBobsGPUToAlice)
	otherSlot := sql.NullInt64{Int64: 3, Valid: true}

	n, err := q.DeletePart(t.Context(), db.DeletePartParams{ID: aliceWantedPSU, OwnerID: alice, SlotID: otherSlot})
	if err != nil || n != 0 {
		t.Errorf("DeletePart from the wrong slot: rows %d, err %v; want 0", n, err)
	}
	n, err = q.UnassignPart(t.Context(), db.UnassignPartParams{PartID: bobSpareGPU, SlotID: otherSlot, UserID: bob})
	if err != nil || n != 0 {
		t.Errorf("UnassignPart from the wrong slot: rows %d, err %v; want 0", n, err)
	}
	if count(t, sqlDB, `SELECT count(*) FROM parts WHERE id IN (11, 20) AND slot_id IS NOT NULL`) != 2 {
		t.Fatal("a part left its slot")
	}

	n, err = q.UnassignPart(t.Context(), db.UnassignPartParams{
		PartID: bobSpareGPU, SlotID: sql.NullInt64{Int64: aliceGPUSlot, Valid: true}, UserID: bob,
	})
	if err != nil || n != 1 {
		t.Errorf("UnassignPart from its slot: rows %d, err %v; want 1", n, err)
	}
}

func TestCreateSlotOwnerOnly(t *testing.T) {
	sqlDB, q := newSeededDB(t)
	_, err := q.CreateSlot(t.Context(), db.CreateSlotParams{MachineID: aliceMachine, Category: "NIC", UserID: bob})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Bob adding a slot to Alice's machine: err %v, want sql.ErrNoRows", err)
	}
	if n := count(t, sqlDB, `SELECT count(*) FROM machine_slots WHERE machine_id = 1`); n != 4 {
		t.Errorf("Alice's machine has %d slots, want its 4", n)
	}
}

func TestCreatePartInSlotOwnerOnly(t *testing.T) {
	sqlDB, q := newSeededDB(t)
	ramSlot := sql.NullInt64{Int64: 3, Valid: true}
	part := func(user int64, slot sql.NullInt64) error {
		_, err := q.CreatePart(t.Context(), db.CreatePartParams{OwnerID: user, SlotID: slot, Status: "owned", Name: "DDR5"})
		return err
	}

	if err := part(bob, ramSlot); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Bob adding a part to Alice's slot: err %v, want sql.ErrNoRows", err)
	}
	if n := count(t, sqlDB, `SELECT count(*) FROM parts WHERE slot_id = 3`); n != 0 {
		t.Fatalf("Alice's RAM slot holds %d parts, want none", n)
	}
	if err := part(bob, sql.NullInt64{}); err != nil {
		t.Errorf("Bob adding a spare: %v", err)
	}
	if err := part(alice, ramSlot); err != nil {
		t.Errorf("Alice adding a part to her own slot: %v", err)
	}
}

// The canonical list URL migration rewrites every spelling of a list URL
// and leaves other values alone.
func TestCanonicalListURLsMigration(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	p := newProvider(t, sqlDB)
	if _, err = p.UpTo(t.Context(), 20261014000000); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"https://pcpartpicker.com/list/Kd3LPf":           "https://pcpartpicker.com/list/Kd3LPf",
		"https://uk.pcpartpicker.com/list/Kd3LPf":        "https://pcpartpicker.com/list/Kd3LPf",
		"http://pcpartpicker.com/list/Kd3LPf/":           "https://pcpartpicker.com/list/Kd3LPf",
		"https://PCPartPicker.com/list/Kd3LPf?x=1":       "https://pcpartpicker.com/list/Kd3LPf",
		"https://de.pcpartpicker.com/list/Kd3LPf/#parts": "https://pcpartpicker.com/list/Kd3LPf",
		"https://pcpartpicker.com/product/abc":           "https://pcpartpicker.com/product/abc",
		"https://pcpartpicker.com/user/bob/saved/AbC123": "https://pcpartpicker.com/user/bob/saved/AbC123",
	}
	exec(t, sqlDB, `INSERT INTO users (id, discord_id, username) VALUES (1, '100', 'alice')`)
	for from := range want {
		exec(t, sqlDB, `INSERT INTO parts (owner_id, name, source_url) VALUES (1, ?, ?)`, from, from)
	}
	exec(t, sqlDB, `INSERT INTO parts (owner_id, name) VALUES (1, 'by hand')`)
	if _, err = p.Up(t.Context()); err != nil {
		t.Fatal(err)
	}

	rows, err := sqlDB.QueryContext(t.Context(), `SELECT name, source_url FROM parts`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var got sql.NullString
		if err := rows.Scan(&name, &got); err != nil {
			t.Fatal(err)
		}
		if name == "by hand" {
			if got.Valid {
				t.Errorf("a part with no list got source %q", got.String)
			}
			continue
		}
		if got.String != want[name] {
			t.Errorf("%q became %q, want %q", name, got.String, want[name])
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// GetFriendSpare finds only what ListFriendSpares would list.
func TestGetFriendSpare(t *testing.T) {
	tests := []struct {
		name  string
		setup string
		user  int64
		part  int64
		want  bool
	}{
		{name: "a friend's lendable spare", user: alice, part: bobSpareGPU, want: true},
		{name: "not your own", user: bob, part: bobSpareGPU},
		{name: "not one its owner doesn't lend", user: bob, part: aliceCPU},
		{
			name: "not one nobody bought", setup: `UPDATE parts SET lendable = TRUE WHERE id = 21`,
			user: alice, part: bobWantedCPU,
		},
		{name: "not one on loan", setup: lendBobsGPUToAlice, user: carol, part: bobSpareGPU},
		{name: "not a former member's", setup: `UPDATE users SET deleted_at = CURRENT_TIMESTAMP WHERE id = 2`,
			user: alice, part: bobSpareGPU},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB, q := newSeededDB(t)
			if tt.setup != "" {
				exec(t, sqlDB, tt.setup)
			}
			row, err := q.GetFriendSpare(t.Context(), db.GetFriendSpareParams{ID: tt.part, UserID: tt.user})
			if found := err == nil; found != tt.want || (err != nil && !errors.Is(err, sql.ErrNoRows)) {
				t.Fatalf("found = %v (err %v), want %v", found, err, tt.want)
			}
			if tt.want && row.OwnerName != "bob" {
				t.Errorf("owner = %q, want bob", row.OwnerName)
			}
		})
	}
}
