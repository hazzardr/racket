package db_test

// Tests for the draft schema and permission queries in docs/. They load the
// draft files directly so the rules are pinned down before the schema replaces
// migrations/. Once it does, point newDraftDB at migrations.FS and call the
// sqlc-generated queries instead of loadDraftQueries.

import (
	"bufio"
	"bytes"
	"database/sql"
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"

	_ "modernc.org/sqlite"
)

const (
	draftSchemaPath  = "../../docs/schema-draft.sql"
	draftQueriesPath = "../../docs/queries-draft.sql"
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
	aliceRAMSlot int64 = 3
	alicePSUSlot int64 = 4
	bobCPUSlot   int64 = 5
	bobGPUSlot   int64 = 6
	carolGPUSlot int64 = 7

	aliceCPU        int64 = 10 // owned, not in a machine
	aliceWantedPSU  int64 = 11 // not purchased
	bobSpareGPU     int64 = 20 // owned, not in a machine
	bobWantedCPU    int64 = 21 // not purchased
	bobInstalledCPU int64 = 22 // owned, in Bob's machine
)

const seedSQL = `
INSERT INTO users (id, discord_id, username) VALUES
    (1, '100', 'alice'), (2, '200', 'bob'), (3, '300', 'carol');
INSERT INTO machines (id, owner_id, name) VALUES
    (1, 1, 'alice-pc'), (2, 2, 'bob-pc'), (3, 3, 'carol-pc');
INSERT INTO machine_slots (id, machine_id, category) VALUES
    (1, 1, 'GPU'), (2, 1, 'CPU'), (3, 1, 'RAM'), (4, 1, 'PSU'),
    (5, 2, 'CPU'), (6, 2, 'GPU'),
    (7, 3, 'GPU');
INSERT INTO parts (id, owner_id, slot_id, status, name) VALUES
    (10, 1, NULL, 'owned',  'Ryzen 7 5700X'),
    (11, 1, NULL, 'wanted', 'Corsair RM850x'),
    (20, 2, NULL, 'owned',  'RTX 3080'),
    (21, 2, NULL, 'wanted', 'Ryzen 7 9800X3D'),
    (22, 2, 5,    'owned',  'Ryzen 5 7600');
`

// newDraftDB returns an in-memory database migrated with the draft schema.
func newDraftDB(t *testing.T) *sql.DB {
	t.Helper()
	schema, err := os.ReadFile(draftSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	// FK enforcement matches the production DSN (see sqliteDSN in cmd/server).
	sqlDB, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	// Each pooled connection would get its own empty :memory: database.
	sqlDB.SetMaxOpenConns(1)

	p, err := goose.NewProvider(goose.DialectSQLite3, sqlDB,
		fstest.MapFS{"00001_init.sql": {Data: schema}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	return sqlDB
}

// newSeededDB returns a draft database loaded with the fixture above.
func newSeededDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB := newDraftDB(t)
	exec(t, sqlDB, seedSQL)
	return sqlDB
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

var sqlcArgRe = regexp.MustCompile(`sqlc\.arg\((\w+)\)`)

// loadDraftQueries splits the draft queries file on sqlc's "-- name:" markers
// and rewrites sqlc.arg(x) as :x, so the driver binds them with sql.Named.
func loadDraftQueries(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(draftQueriesPath)
	if err != nil {
		t.Fatal(err)
	}
	queries := map[string]string{}
	var name string
	var body strings.Builder
	flush := func() {
		if name != "" {
			queries[name] = sqlcArgRe.ReplaceAllString(body.String(), ":$1")
		}
		body.Reset()
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := sc.Text()
		if rest, ok := strings.CutPrefix(line, "-- name: "); ok {
			flush()
			name = strings.Fields(rest)[0]
			continue
		}
		body.WriteString(line + "\n")
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	flush()
	return queries
}

func draftQuery(t *testing.T, name string) string {
	t.Helper()
	q, ok := loadDraftQueries(t)[name]
	if !ok {
		t.Fatalf("query %q not found in %s", name, draftQueriesPath)
	}
	return q
}

func TestDraftSchemaUpDown(t *testing.T) {
	schema, err := os.ReadFile(draftSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB := newDraftDB(t)
	p, err := goose.NewProvider(goose.DialectSQLite3, sqlDB,
		fstest.MapFS{"00001_init.sql": {Data: schema}})
	if err != nil {
		t.Fatal(err)
	}

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

func TestDraftSchemaConstraints(t *testing.T) {
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
			sqlDB := newSeededDB(t)
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

// lendBobsGPUToAlice puts Bob's spare GPU in Alice's machine.
const lendBobsGPUToAlice = `UPDATE parts SET slot_id = 1 WHERE id = 20`

func TestAssignPartToSlot(t *testing.T) {
	tests := []struct {
		name  string
		setup string
		user  int64
		part  int64
		slot  int64
		want  bool
	}{
		{name: "own part into own machine", user: alice, part: aliceCPU, slot: aliceCPUSlot, want: true},
		{name: "own unpurchased part into own machine", user: alice, part: aliceWantedPSU, slot: alicePSUSlot, want: true},
		{name: "borrow a friend's spare part", user: alice, part: bobSpareGPU, slot: aliceGPUSlot, want: true},
		{name: "can't borrow a part nobody bought", user: alice, part: bobWantedCPU, slot: aliceCPUSlot},
		{name: "can't take a part out of a friend's machine", user: alice, part: bobInstalledCPU, slot: aliceCPUSlot},
		{name: "can't put parts in a friend's machine", user: bob, part: bobSpareGPU, slot: aliceGPUSlot},
		{
			name:  "can't take a part already lent to someone else",
			setup: lendBobsGPUToAlice,
			user:  carol, part: bobSpareGPU, slot: carolGPUSlot,
		},
		{
			name:  "owner can move a lent part back into their own machine",
			setup: lendBobsGPUToAlice,
			user:  bob, part: bobSpareGPU, slot: bobGPUSlot, want: true,
		},
	}
	query := draftQuery(t, "AssignPartToSlot")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB := newSeededDB(t)
			if tt.setup != "" {
				exec(t, sqlDB, tt.setup)
			}

			res, err := sqlDB.ExecContext(t.Context(), query,
				sql.Named("slot_id", tt.slot), sql.Named("part_id", tt.part), sql.Named("user_id", tt.user))
			if err != nil {
				t.Fatal(err)
			}
			n, err := res.RowsAffected()
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
	query := draftQuery(t, "UnassignPart")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB := newSeededDB(t)
			exec(t, sqlDB, lendBobsGPUToAlice)
			exec(t, sqlDB, `UPDATE parts SET slot_id = 2 WHERE id = 10`)

			res, err := sqlDB.ExecContext(t.Context(), query,
				sql.Named("part_id", tt.part), sql.Named("user_id", tt.user))
			if err != nil {
				t.Fatal(err)
			}
			n, err := res.RowsAffected()
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

func TestSlotPartSourceLabels(t *testing.T) {
	sqlDB := newSeededDB(t)
	exec(t, sqlDB, lendBobsGPUToAlice)
	exec(t, sqlDB, `UPDATE parts SET slot_id = 2 WHERE id = 10`)
	exec(t, sqlDB, `UPDATE parts SET slot_id = 4 WHERE id = 11`)

	rows, err := sqlDB.QueryContext(t.Context(), draftQuery(t, "ListSlotsByMachine"), aliceMachine)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	type slot struct{ category, owner, source string }
	var got []slot
	for rows.Next() {
		var (
			id                        int64
			category, source          string
			partID                    sql.NullInt64
			partName, status, ownerNm sql.NullString
		)
		if err := rows.Scan(&id, &category, &partID, &partName, &status, &ownerNm, &source); err != nil {
			t.Fatal(err)
		}
		got = append(got, slot{category, ownerNm.String, source})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	want := []slot{
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

func TestDeleteMachineReturnsPartsToInventory(t *testing.T) {
	sqlDB := newSeededDB(t)
	exec(t, sqlDB, lendBobsGPUToAlice)
	exec(t, sqlDB, `UPDATE parts SET slot_id = 2 WHERE id = 10`)
	exec(t, sqlDB, `INSERT INTO racks (id, owner_id, name) VALUES (1, 1, 'lab')`)
	exec(t, sqlDB, `INSERT INTO rack_mounts (rack_id, machine_id, position_u) VALUES (1, 1, 10)`)
	exec(t, sqlDB, `INSERT INTO rack_mounts (rack_id, label, position_u) VALUES (1, 'patch panel', 42)`)

	exec(t, sqlDB, `DELETE FROM machines WHERE id = 1`)

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

func TestDeleteUserRemovesTheirData(t *testing.T) {
	sqlDB := newSeededDB(t)
	exec(t, sqlDB, lendBobsGPUToAlice)
	exec(t, sqlDB, `UPDATE parts SET slot_id = 2 WHERE id = 10`)
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
