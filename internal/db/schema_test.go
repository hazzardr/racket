package db_test

// Tests for the schema's rules and the permission-bearing queries: per-owner
// names, one part per slot, borrowing, slot labels, and what leaving or
// deleting leaves behind.

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"racket/internal/db"
	"racket/migrations"

	_ "modernc.org/sqlite"
)

// Fixture IDs. Alice and Bob each have a machine; Carol has one too, so there
// is a third party for borrowing cases.
const (
	alice int64 = 1
	bob   int64 = 2
	carol int64 = 3

	aliceMachine int64 = 1

	aliceGPUSlot int64 = 1
	aliceCPUSlot int64 = 2
	alicePSUSlot int64 = 4
	bobGPUSlot   int64 = 6
	carolGPUSlot int64 = 7

	aliceCPU        int64 = 10 // owned, not in a machine, not lent
	aliceWantedPSU  int64 = 11 // not purchased
	bobSpareGPU     int64 = 20 // owned, not in a machine, lendable
	bobWantedCPU    int64 = 21 // not purchased
	bobInstalledCPU int64 = 22 // owned, in Bob's machine
)

const seedSQL = `
INSERT INTO users (id, discord_id, username) VALUES
    (1, '100', 'alice'), (2, '200', 'bob'), (3, '300', 'carol');
INSERT INTO machines (id, owner_id, name) VALUES
    (1, 1, 'alice-pc'), (2, 2, 'bob-pc'), (3, 3, 'carol-pc');
INSERT INTO machine_slots (id, machine_id, category, position) VALUES
    (1, 1, 'GPU', 0), (2, 1, 'CPU', 1), (3, 1, 'RAM', 2), (4, 1, 'PSU', 3),
    (5, 2, 'CPU', 0), (6, 2, 'GPU', 1),
    (7, 3, 'GPU', 0);
INSERT INTO parts (id, owner_id, slot_id, status, name, lendable) VALUES
    (10, 1, NULL, 'owned',  'Ryzen 7 5700X',   FALSE),
    (11, 1, NULL, 'wanted', 'Corsair RM850x',  FALSE),
    (20, 2, NULL, 'owned',  'RTX 3080',        TRUE),
    (21, 2, NULL, 'wanted', 'Ryzen 7 9800X3D', FALSE),
    (22, 2, 5,    'owned',  'Ryzen 5 7600',    FALSE);
`

const (
	// lendBobsGPUToAlice puts Bob's spare GPU in Alice's machine.
	lendBobsGPUToAlice = `UPDATE parts SET slot_id = 1 WHERE id = 20`
	// installAliceCPU puts Alice's CPU in her own machine.
	installAliceCPU = `UPDATE parts SET slot_id = 2 WHERE id = 10`
)

func newProvider(t *testing.T, sqlDB *sql.DB) *goose.Provider {
	t.Helper()
	p, err := goose.NewProvider(goose.DialectSQLite3, sqlDB, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// newTestDB returns an in-memory database with the migrations applied.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	// FK enforcement matches the production DSN (see sqliteDSN in cmd/server).
	sqlDB, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	// Each pooled connection would get its own empty :memory: database.
	sqlDB.SetMaxOpenConns(1)
	if _, err := newProvider(t, sqlDB).Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	return sqlDB
}

// newSeededDB returns a migrated database loaded with the fixture above.
func newSeededDB(t *testing.T) (*sql.DB, *db.Queries) {
	t.Helper()
	sqlDB := newTestDB(t)
	exec(t, sqlDB, seedSQL)
	return sqlDB, db.New(sqlDB)
}

func exec(t *testing.T, sqlDB *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := sqlDB.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func count(t *testing.T, sqlDB *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := sqlDB.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func TestMigrationsUpDown(t *testing.T) {
	sqlDB := newTestDB(t)
	p := newProvider(t, sqlDB)

	if _, err := p.DownTo(t.Context(), 0); err != nil {
		t.Fatalf("down: %v", err)
	}
	const leftovers = `SELECT count(*) FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%' AND name <> 'goose_db_version'`
	if n := count(t, sqlDB, leftovers); n != 0 {
		t.Errorf("down left %d schema objects behind", n)
	}
	if _, err := p.Up(t.Context()); err != nil {
		t.Fatalf("up after down: %v", err)
	}
}

func TestSchemaConstraints(t *testing.T) {
	const (
		unique = "UNIQUE constraint failed"
		check  = "CHECK constraint failed"
		fk     = "FOREIGN KEY constraint failed"
	)
	tests := []struct {
		name    string
		stmt    string
		wantErr string // "" means the statement must succeed
	}{
		{
			name:    "machine names are unique per owner",
			stmt:    `INSERT INTO machines (owner_id, name) VALUES (1, 'alice-pc')`,
			wantErr: unique,
		},
		{
			name: "two owners can reuse a machine name",
			stmt: `INSERT INTO machines (owner_id, name) VALUES (2, 'alice-pc')`,
		},
		{
			name:    "a slot holds one part",
			stmt:    `INSERT INTO parts (owner_id, slot_id, name) VALUES (2, 5, 'second CPU')`,
			wantErr: unique,
		},
		{
			name:    "part status is an enum",
			stmt:    `INSERT INTO parts (owner_id, status, name) VALUES (1, 'lost', 'x')`,
			wantErr: check,
		},
		{
			name:    "machine kind is an enum",
			stmt:    `INSERT INTO machines (owner_id, name, kind) VALUES (1, 'x', 'toaster')`,
			wantErr: check,
		},
		{
			name:    "prices are not negative",
			stmt:    `INSERT INTO parts (owner_id, name, price_cents) VALUES (1, 'x', -1)`,
			wantErr: check,
		},
		{
			name:    "an installed part can't be retired",
			stmt:    `UPDATE parts SET status = 'retired' WHERE id = 22`,
			wantErr: check,
		},
		{
			name: "a part outside a machine can be retired",
			stmt: `UPDATE parts SET status = 'retired' WHERE id = 20`,
		},
		{
			name:    "parts need a real owner",
			stmt:    `INSERT INTO parts (owner_id, name) VALUES (99, 'x')`,
			wantErr: fk,
		},
		{
			name:    "racks are at most 60U",
			stmt:    `INSERT INTO racks (owner_id, name, height_u) VALUES (1, 'x', 61)`,
			wantErr: check,
		},
		{
			name:    "a mount needs a machine or a label",
			stmt:    `INSERT INTO rack_mounts (rack_id, position_u) VALUES (1, 1)`,
			wantErr: check,
		},
		{
			name:    "a mount can't be both a machine and a label",
			stmt:    `INSERT INTO rack_mounts (rack_id, machine_id, label, position_u) VALUES (1, 2, 'x', 1)`,
			wantErr: check,
		},
		{
			name:    "a machine is mounted at most once",
			stmt:    `INSERT INTO rack_mounts (rack_id, machine_id, position_u) VALUES (1, 1, 20)`,
			wantErr: unique,
		},
		{
			name:    "mounts start at U1",
			stmt:    `INSERT INTO rack_mounts (rack_id, label, position_u) VALUES (1, 'shelf', 0)`,
			wantErr: check,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB, _ := newSeededDB(t)
			exec(t, sqlDB, `INSERT INTO racks (id, owner_id, name) VALUES (1, 1, 'lab')`)
			exec(t, sqlDB, `INSERT INTO rack_mounts (rack_id, machine_id, position_u, height_u)
				VALUES (1, 1, 10, 2)`)

			_, err := sqlDB.ExecContext(t.Context(), tt.stmt)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("want success, got %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("want %q, got success", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("want %q, got %v", tt.wantErr, err)
			}
		})
	}
}

// bobLeaves soft-deletes Bob.
func bobLeaves(t *testing.T, q *db.Queries) {
	t.Helper()
	if _, err := q.SoftDeleteUser(t.Context(), bob); err != nil {
		t.Fatal(err)
	}
}

func TestAssignPartToSlot(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *sql.DB, *db.Queries)
		user  int64
		part  int64
		slot  int64
		want  bool
	}{
		{name: "own part into own machine", user: alice, part: aliceCPU, slot: aliceCPUSlot, want: true},
		{name: "own unpurchased part into own machine", user: alice, part: aliceWantedPSU, slot: alicePSUSlot, want: true},
		{name: "borrow a friend's spare part", user: alice, part: bobSpareGPU, slot: aliceGPUSlot, want: true},
		{name: "can't borrow a part nobody bought", user: alice, part: bobWantedCPU, slot: aliceCPUSlot},
		{name: "can't borrow a spare its owner doesn't lend", user: bob, part: aliceCPU, slot: bobGPUSlot},
		{name: "can't take a part out of a friend's machine", user: alice, part: bobInstalledCPU, slot: aliceCPUSlot},
		{name: "can't put parts in a friend's machine", user: bob, part: bobSpareGPU, slot: aliceGPUSlot},
		{
			name:  "can't take a part already lent to someone else",
			setup: func(t *testing.T, sqlDB *sql.DB, _ *db.Queries) { exec(t, sqlDB, lendBobsGPUToAlice) },
			user:  carol, part: bobSpareGPU, slot: carolGPUSlot,
		},
		{
			name:  "can't borrow from a former member",
			setup: func(t *testing.T, _ *sql.DB, q *db.Queries) { bobLeaves(t, q) },
			user:  alice, part: bobSpareGPU, slot: aliceGPUSlot,
		},
		{
			name:  "owner can move a lent part back into their own machine",
			setup: func(t *testing.T, sqlDB *sql.DB, _ *db.Queries) { exec(t, sqlDB, lendBobsGPUToAlice) },
			user:  bob, part: bobSpareGPU, slot: bobGPUSlot, want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB, q := newSeededDB(t)
			if tt.setup != nil {
				tt.setup(t, sqlDB, q)
			}

			n, err := q.AssignPartToSlot(t.Context(), db.AssignPartToSlotParams{
				SlotID: sql.NullInt64{Int64: tt.slot, Valid: true},
				PartID: tt.part,
				UserID: tt.user,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := n == 1; got != tt.want {
				t.Fatalf("allowed = %v, want %v", got, tt.want)
			}
			inSlot := count(t, sqlDB, `SELECT count(*) FROM parts WHERE id = ? AND slot_id = ?`, tt.part, tt.slot)
			if (inSlot == 1) != tt.want {
				t.Fatalf("part in slot = %v, want %v", inSlot == 1, tt.want)
			}
		})
	}
}

func TestUnassignPart(t *testing.T) {
	tests := []struct {
		name string
		user int64
		part int64
		want bool
	}{
		{name: "lender takes their part back", user: bob, part: bobSpareGPU, want: true},
		{name: "builder removes a borrowed part", user: alice, part: bobSpareGPU, want: true},
		{name: "builder removes their own part", user: alice, part: aliceCPU, want: true},
		{name: "outsider can't remove a lent part", user: carol, part: bobSpareGPU},
		{name: "lender can't remove the builder's own parts", user: bob, part: aliceCPU},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB, q := newSeededDB(t)
			exec(t, sqlDB, lendBobsGPUToAlice)
			exec(t, sqlDB, installAliceCPU)

			n, err := q.UnassignPart(t.Context(), db.UnassignPartParams{PartID: tt.part, UserID: tt.user})
			if err != nil {
				t.Fatal(err)
			}
			if got := n == 1; got != tt.want {
				t.Fatalf("allowed = %v, want %v", got, tt.want)
			}
			unassigned := count(t, sqlDB, `SELECT count(*) FROM parts WHERE id = ? AND slot_id IS NULL`, tt.part)
			if (unassigned == 1) != tt.want {
				t.Fatalf("part unassigned = %v, want %v", unassigned == 1, tt.want)
			}
		})
	}
}

type slotLabel struct{ category, owner, source string }

// slotLabels lists a machine's slots as the UI would label them.
func slotLabels(t *testing.T, q *db.Queries, machineID int64) []slotLabel {
	t.Helper()
	rows, err := q.ListSlotsByMachine(t.Context(), machineID)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]slotLabel, 0, len(rows))
	for _, r := range rows {
		got = append(got, slotLabel{r.Category, r.PartOwnerName.String, r.PartSource})
	}
	return got
}

func TestSlotPartSourceLabels(t *testing.T) {
	sqlDB, q := newSeededDB(t)
	exec(t, sqlDB, lendBobsGPUToAlice)
	exec(t, sqlDB, installAliceCPU)
	exec(t, sqlDB, `UPDATE parts SET slot_id = 4 WHERE id = 11`)

	got := slotLabels(t, q, aliceMachine)
	want := []slotLabel{
		{"GPU", "bob", "borrowed"},
		{"CPU", "alice", "own"},
		{"RAM", "", "empty"},
		{"PSU", "alice", "unpurchased"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d slots, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("slot %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestCreateSlotAppends(t *testing.T) {
	_, q := newSeededDB(t)
	slot, err := q.CreateSlot(t.Context(), db.CreateSlotParams{MachineID: aliceMachine, Category: "NIC", UserID: alice})
	if err != nil {
		t.Fatal(err)
	}
	if slot.Position != 4 {
		t.Errorf("position = %d, want 4 (after the 4 existing slots)", slot.Position)
	}
	if got := slotLabels(t, q, aliceMachine); got[len(got)-1].category != "NIC" {
		t.Errorf("new slot isn't last: %v", got)
	}
}

func TestListMachinesTotals(t *testing.T) {
	sqlDB, q := newSeededDB(t)
	exec(t, sqlDB, `UPDATE parts SET price_cents = 25000 WHERE id = 10`)
	exec(t, sqlDB, `UPDATE parts SET price_cents = 40000 WHERE id = 20`)
	exec(t, sqlDB, lendBobsGPUToAlice)
	exec(t, sqlDB, installAliceCPU)

	machines, err := q.ListMachines(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range machines {
		if m.Machine.ID != aliceMachine {
			continue
		}
		if m.OwnerName != "alice" || m.TotalSlots != 4 || m.FilledSlots != 2 || m.PartsTotalCents != 65000 {
			t.Errorf("alice-pc = %+v, want owner alice, 2/4 slots, 65000 cents", m)
		}
		return
	}
	t.Fatal("alice-pc not listed")
}

func TestSessions(t *testing.T) {
	_, q := newSeededDB(t)
	now := time.Now().UTC()
	for _, s := range []db.CreateSessionParams{
		{TokenHash: []byte("live"), UserID: alice, ExpiresAt: now.Add(time.Hour)},
		{TokenHash: []byte("expired"), UserID: alice, ExpiresAt: now.Add(-time.Second)},
		{TokenHash: []byte("bob"), UserID: bob, ExpiresAt: now.Add(time.Hour)},
	} {
		if err := q.CreateSession(t.Context(), s); err != nil {
			t.Fatal(err)
		}
	}
	lookup := func(token string) (db.User, error) {
		return q.GetSessionUser(t.Context(), db.GetSessionUserParams{TokenHash: []byte(token), Now: now})
	}

	if u, err := lookup("live"); err != nil || u.ID != alice {
		t.Errorf("live session: got user %d, err %v", u.ID, err)
	}
	if _, err := lookup("expired"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expired session: err = %v, want sql.ErrNoRows", err)
	}
	if _, err := lookup("unknown"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("unknown token: err = %v, want sql.ErrNoRows", err)
	}
	bobLeaves(t, q)
	if _, err := lookup("bob"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("session of a user who left: err = %v, want sql.ErrNoRows", err)
	}
}

func TestSoftDeletedUser(t *testing.T) {
	sqlDB, q := newSeededDB(t)
	exec(t, sqlDB, lendBobsGPUToAlice)
	exec(t, sqlDB, `INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES (x'00', 2, CURRENT_TIMESTAMP)`)
	machineCount := func() int {
		t.Helper()
		machines, err := q.ListMachines(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return len(machines)
	}

	bobLeaves(t, q)
	if err := q.DeleteSessionsByUser(t.Context(), bob); err != nil {
		t.Fatal(err)
	}

	t.Run("lent parts stay, labeled as from a former member", func(t *testing.T) {
		got := slotLabels(t, q, aliceMachine)
		if got[0] != (slotLabel{"GPU", "bob", "former_member"}) {
			t.Errorf("GPU slot = %+v", got[0])
		}
	})
	t.Run("their machines drop out of listings", func(t *testing.T) {
		if n := machineCount(); n != 2 {
			t.Errorf("listed %d machines, want 2 (alice, carol)", n)
		}
		if _, err := q.GetMachine(t.Context(), 2); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("GetMachine(bob-pc): err = %v, want sql.ErrNoRows", err)
		}
	})
	t.Run("their data and sessions", func(t *testing.T) {
		if n := count(t, sqlDB, `SELECT count(*) FROM parts WHERE owner_id = 2`); n != 3 {
			t.Errorf("%d of Bob's 3 parts kept", n)
		}
		if n := count(t, sqlDB, `SELECT count(*) FROM sessions WHERE user_id = 2`); n != 0 {
			t.Errorf("%d sessions left", n)
		}
	})
	t.Run("logging in again restores them", func(t *testing.T) {
		u, err := q.UpsertDiscordUser(t.Context(), db.UpsertDiscordUserParams{DiscordID: "200", Username: "bob"})
		if err != nil {
			t.Fatal(err)
		}
		if u.ID != bob || u.DeletedAt.Valid {
			t.Fatalf("upsert returned %+v, want Bob restored", u)
		}
		if n := machineCount(); n != 3 {
			t.Errorf("listed %d machines, want 3", n)
		}
		if got := slotLabels(t, q, aliceMachine)[0].source; got != "borrowed" {
			t.Errorf("GPU slot source = %q, want borrowed", got)
		}
	})
}

func TestDeleteMachineReturnsPartsToInventory(t *testing.T) {
	sqlDB, q := newSeededDB(t)
	exec(t, sqlDB, lendBobsGPUToAlice)
	exec(t, sqlDB, installAliceCPU)
	exec(t, sqlDB, `INSERT INTO racks (id, owner_id, name) VALUES (1, 1, 'lab')`)
	exec(t, sqlDB, `INSERT INTO rack_mounts (rack_id, machine_id, position_u) VALUES (1, 1, 10)`)
	exec(t, sqlDB, `INSERT INTO rack_mounts (rack_id, label, position_u) VALUES (1, 'patch panel', 42)`)

	n, err := q.DeleteMachine(t.Context(), db.DeleteMachineParams{ID: aliceMachine, OwnerID: bob})
	if err != nil || n != 0 {
		t.Fatalf("Bob deleting Alice's machine: rows = %d, err = %v; want 0 rows", n, err)
	}
	n, err = q.DeleteMachine(t.Context(), db.DeleteMachineParams{ID: aliceMachine, OwnerID: alice})
	if err != nil || n != 1 {
		t.Fatalf("Alice deleting her machine: rows = %d, err = %v; want 1 row", n, err)
	}

	if n := count(t, sqlDB, `SELECT count(*) FROM parts WHERE id IN (10, 20) AND slot_id IS NULL`); n != 2 {
		t.Errorf("%d of 2 parts back in inventory", n)
	}
	if n := count(t, sqlDB, `SELECT count(*) FROM machine_slots WHERE machine_id = 1`); n != 0 {
		t.Errorf("%d slots left on the deleted machine", n)
	}
	if n := count(t, sqlDB, `SELECT count(*) FROM rack_mounts WHERE machine_id IS NOT NULL`); n != 0 {
		t.Errorf("deleted machine is still mounted")
	}
	if n := count(t, sqlDB, `SELECT count(*) FROM rack_mounts WHERE label = 'patch panel'`); n != 1 {
		t.Errorf("labeled mount was removed with the machine")
	}
}

func TestDeleteSlotReturnsPartToInventory(t *testing.T) {
	sqlDB, q := newSeededDB(t)
	exec(t, sqlDB, lendBobsGPUToAlice)

	n, err := q.DeleteSlot(t.Context(), db.DeleteSlotParams{SlotID: aliceGPUSlot, UserID: bob})
	if err != nil || n != 0 {
		t.Fatalf("Bob deleting Alice's slot: rows = %d, err = %v; want 0 rows", n, err)
	}
	n, err = q.DeleteSlot(t.Context(), db.DeleteSlotParams{SlotID: aliceGPUSlot, UserID: alice})
	if err != nil || n != 1 {
		t.Fatalf("Alice deleting her slot: rows = %d, err = %v; want 1 row", n, err)
	}
	if n := count(t, sqlDB, `SELECT count(*) FROM parts WHERE id = 20 AND owner_id = 2 AND slot_id IS NULL`); n != 1 {
		t.Error("Bob's GPU didn't go back to his inventory")
	}
}

// TestPurgeUserRemovesTheirData covers the hard delete (see TestSoftDeletedUser
// for the normal way a user leaves).
func TestPurgeUserRemovesTheirData(t *testing.T) {
	sqlDB, _ := newSeededDB(t)
	exec(t, sqlDB, lendBobsGPUToAlice)
	exec(t, sqlDB, installAliceCPU)
	exec(t, sqlDB, `INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES (x'00', 2, CURRENT_TIMESTAMP)`)

	exec(t, sqlDB, `DELETE FROM users WHERE id = 2`)

	for _, query := range []string{
		`SELECT count(*) FROM machines WHERE owner_id = 2`,
		`SELECT count(*) FROM parts WHERE owner_id = 2`,
		`SELECT count(*) FROM sessions WHERE user_id = 2`,
	} {
		if n := count(t, sqlDB, query); n != 0 {
			t.Errorf("%q = %d, want 0", query, n)
		}
	}
	// The part Bob lent is gone from Alice's machine; her own parts stay.
	if n := count(t, sqlDB, `SELECT count(*) FROM parts WHERE slot_id = 1`); n != 0 {
		t.Errorf("deleted user's part is still in Alice's GPU slot")
	}
	if n := count(t, sqlDB, `SELECT count(*) FROM parts WHERE owner_id = 1 AND slot_id = 2`); n != 1 {
		t.Errorf("Alice's own CPU was removed")
	}
}
