package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"

	"racket/internal/auth"
	"racket/internal/auth/authtest"
	"racket/internal/db"
	"racket/internal/web"
	"racket/migrations"

	_ "modernc.org/sqlite"
)

// testApp is the real router over an in-memory database, with dev login on.
type testApp struct {
	t       *testing.T
	h       *Handler
	router  http.Handler
	db      *sql.DB
	queries *db.Queries
	discord *authtest.Discord // nil unless newTestAppWithDiscord
}

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
	p, err := goose.NewProvider(goose.DialectSQLite3, sqlDB, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	return sqlDB
}

func newTestWeb(t *testing.T) *web.Web {
	t.Helper()
	w, err := web.New()
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func buildTestApp(t *testing.T, a Auth) *testApp {
	t.Helper()
	sqlDB := newTestDB(t)
	q := db.New(sqlDB)
	a.Sessions = auth.NewSessions(q, false)
	h := New(sqlDB, q, newTestWeb(t), a)
	return &testApp{t: t, h: h, router: h.Routes(), db: sqlDB, queries: q}
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()
	return buildTestApp(t, Auth{DevLogin: true})
}

// newTestAppWithDiscord signs in through a fake Discord only.
func newTestAppWithDiscord(t *testing.T) *testApp {
	t.Helper()
	fake := authtest.NewDiscord(t)
	app := buildTestApp(t, Auth{
		Discord: auth.NewDiscord(fake.Config("http://racket.test" + discordCallbackPath)),
	})
	app.discord = fake
	return app
}

// do sends a request as the user behind cookie (nil: signed out). A non-nil
// form is posted urlencoded; htmx marks it as an htmx request.
func (app *testApp) do(
	cookie *http.Cookie, method, path string, form url.Values, htmx bool,
) *httptest.ResponseRecorder {
	app.t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if htmx {
		req.Header.Set("Hx-Request", "true")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	app.router.ServeHTTP(rr, req)
	return rr
}

func (app *testApp) get(cookie *http.Cookie, path string) *httptest.ResponseRecorder {
	app.t.Helper()
	return app.do(cookie, http.MethodGet, path, nil, false)
}

func (app *testApp) post(cookie *http.Cookie, path string, form url.Values) *httptest.ResponseRecorder {
	app.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	return app.do(cookie, http.MethodPost, path, form, false)
}

func (app *testApp) postHX(cookie *http.Cookie, path string, form url.Values) *httptest.ResponseRecorder {
	app.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	return app.do(cookie, http.MethodPost, path, form, true)
}

// responseCookie returns the named cookie a response set, or nil.
func responseCookie(rr *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rr.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// login signs in through dev login and returns the session cookie.
func (app *testApp) login(username string) *http.Cookie {
	app.t.Helper()
	rr := app.post(nil, devLoginPath, url.Values{"username": {username}})
	if rr.Code != http.StatusSeeOther {
		app.t.Fatalf("dev login as %s: status %d body: %s", username, rr.Code, rr.Body.String())
	}
	c := responseCookie(rr, auth.SessionCookie)
	if c == nil {
		app.t.Fatalf("dev login as %s set no session cookie", username)
	}
	return c
}

// user returns the dev-login user with the given name.
func (app *testApp) user(username string) db.User {
	app.t.Helper()
	u, err := app.queries.UpsertDiscordUser(app.t.Context(), db.UpsertDiscordUserParams{
		DiscordID: devUserPrefix + strings.ToLower(username),
		Username:  username,
	})
	if err != nil {
		app.t.Fatal(err)
	}
	return u
}

// createMachine posts the new-machine form as cookie's user and returns the
// machine ID.
func (app *testApp) createMachine(cookie *http.Cookie, name string) int64 {
	app.t.Helper()
	rr := app.post(cookie, "/machines", url.Values{"name": {name}})
	if rr.Code != http.StatusSeeOther {
		app.t.Fatalf("CreateMachine: expected 303, got %d body: %s", rr.Code, rr.Body.String())
	}
	loc := rr.Header().Get("Location")
	id, err := strconv.ParseInt(strings.TrimPrefix(loc, machinesPath+"/"), 10, 64)
	if err != nil {
		app.t.Fatalf("CreateMachine: bad Location %q: %v", loc, err)
	}
	return id
}

func (app *testApp) slots(machineID int64) []db.ListSlotsByMachineRow {
	app.t.Helper()
	slots, err := app.queries.ListSlotsByMachine(app.t.Context(), machineID)
	if err != nil {
		app.t.Fatal(err)
	}
	return slots
}

// part returns a part by ID, or ok=false if it's gone.
func (app *testApp) part(id int64) (db.Part, bool) {
	app.t.Helper()
	var p db.Part
	err := app.db.QueryRowContext(app.t.Context(),
		`SELECT id, owner_id, slot_id, status, name FROM parts WHERE id = ?`, id).
		Scan(&p.ID, &p.OwnerID, &p.SlotID, &p.Status, &p.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Part{}, false
	}
	if err != nil {
		app.t.Fatal(err)
	}
	return p, true
}

// sparePart creates a bought part in owner's inventory that friends may
// borrow.
func (app *testApp) sparePart(owner db.User, name string) db.Part {
	app.t.Helper()
	p, err := app.queries.CreatePart(app.t.Context(), db.CreatePartParams{
		OwnerID: owner.ID, Status: "owned", Name: name, Lendable: true,
	})
	if err != nil {
		app.t.Fatal(err)
	}
	return p
}

// borrow puts part in slot as the slot's owner, through the permission query.
func (app *testApp) borrow(borrower db.User, part db.Part, slotID int64) {
	app.t.Helper()
	n, err := app.queries.AssignPartToSlot(app.t.Context(), db.AssignPartToSlotParams{
		SlotID: sql.NullInt64{Int64: slotID, Valid: true},
		PartID: part.ID,
		UserID: borrower.ID,
	})
	if err != nil || n != 1 {
		app.t.Fatalf("borrow %q: rows %d, err %v", part.Name, n, err)
	}
}

func slotPath(slotID int64, suffix string) string {
	return fmt.Sprintf("/slots/%d%s", slotID, suffix)
}

func machinePath(machineID int64, suffix string) string {
	return fmt.Sprintf("%s/%d%s", machinesPath, machineID, suffix)
}

func assertStatus(t *testing.T, rr *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rr.Code != want {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, want, rr.Body.String())
	}
}

func assertContains(t *testing.T, rr *httptest.ResponseRecorder, want ...string) {
	t.Helper()
	body := rr.Body.String()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("body missing %q:\n%s", w, body)
		}
	}
}

func assertNotContains(t *testing.T, rr *httptest.ResponseRecorder, unwanted ...string) {
	t.Helper()
	body := rr.Body.String()
	for _, u := range unwanted {
		if strings.Contains(body, u) {
			t.Errorf("body contains %q:\n%s", u, body)
		}
	}
}
