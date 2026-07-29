# htmx Migration Plan (racket)

Goal: replace the Vue 3 SPA with stdlib `html/template` + htmx; delete the JSON API; delete the entire frontend toolchain.

Decisions (locked): stdlib templates only, htmx vendored + embedded, no Alpine, JSON API deleted, import becomes one transaction, dark CSS extracted to one static file, templates parsed once at startup.

---

## Phase 1 — Template/static foundation (binary compiles & serves)

**New files**
- `templates/layout.html` — `<!doctype html>`, `<head>` with `/static/app.css` + `/static/htmx.min.js` (defer), nav (Racket logo → `/`, links: Machines, New Machine, footer import link), `{{block "content" .}}{{end}}`.
- `templates/pages/.gitkeep` (real pages in Phase 2)
- `internal/web/web.go` — one file:
  - `//go:embed all:templates` → `templateFS` (move `templates/` under `internal/web/` so embed is local; adjust paths accordingly: `internal/web/templates/...`).
  - `//go:embed all:static` → `StaticFS` (`internal/web/static/`).
  - `func New() (*Web, error)` — `template.ParseFS` once, walk/parse all `templates/**/*.html`; return error on parse failure.
  - `func (w *Web) Render(rw http.ResponseWriter, status int, name string, data any)` — `ExecuteTemplate(rw, name, data)`, `Content-Type: text/html; charset=utf-8`.
  - `func (w *Web) Fragment(rw, name, data)` — same but no layout (partials).
  - `// ponytail: one template set, named blocks; no per-page render struct`
- `internal/web/static/htmx.min.js` — vendored htmx 2.x, committed.
- `internal/web/static/app.css` — dark theme extracted from Vue `<style>` blocks (bg `#1a1a1a`, text `#e0e0e0`, accent `#4a9eff`, slot card/drag-over styles, landing page styles).

**Changed**
- `cmd/server/main.go`:
  - Delete `spaHandler`, `newSPAHandler`, `serveIndex` (the whole bottom block).
  - Delete import of root `racket` package.
  - Add `web, err := web.New()`; fail startup on error.
  - `r.Handle("/static/*", http.FileServerFS(web.Static()))` — or `fs.Sub` wrapper if needed.
  - Pass `web` into `handlers.New(q, web)`.

**Deleted**
- `frontend_embed.go`

**Verify**: `mise run build` compiles; `go run ./cmd/server`; `curl -s localhost:8080/static/htmx.min.js | head -c 60`; `curl -s localhost:8080/static/app.css | head -c 60`; `curl -si localhost:8080/ | head -5` (may 404 until Phase 2).

## Phase 2 — Pages + page handlers

**New templates** (all under `internal/web/templates/`)
- `pages/landing.html` — landing content ported from `LandingView.vue` (RACK / ET art, tagline, Enter button → `/machines`, discreet `/import` link).
- `pages/machines.html` — machine list: name link, `{{if .PurchaseDate}}`, price total, slot-fill count, delete form. Data: `[]db.ListMachinesRow` (reuse existing query) + computed fill counts.
- `pages/new_machine.html` — single form matching actual `MachineInput`/current Vue fields: `name` (required), `description`, `location`, `built_on` (date), `purchased_on` (date).
- `pages/machine_detail.html` — header (name, edit/delete), slot grid. Slot loop:
  ```html
  {{range .Slots}}{{template "slot_card" .}}{{end}}
  {{template "category_chips" .}}
  ```
  Data struct:
  ```go
  type MachinePage struct {
      Machine db.Machine
      Slots   []SlotVM   // db.ListSlotsByMachineRow + Part fields flattened
      Cats    []db.Category // for chips
  }
  ```
- `pages/import.html` — two sections: (a) PCPartPicker URL form, (b) CSV file form; both with optional `machine_name` field.

**Changed handlers** (`internal/handlers/`)
- `handlers.go`:
  - `New(q *db.Queries, w *web.Web) *Handler` — store both.
  - Delete `writeJSON`, `writeError`, `decodeJSON`, `MachineResponse`/`PartResponse`/`SlotResponse` + `new*Response` (all JSON shapes).
  - Add `renderError(w, r, status, msg)` — renders a tiny `error.html` partial (or plain `<p class="error">` fragment); used everywhere validation fails.
  - `Dashboard` — deleted (Vue-only aggregate; machines page shows same info).
- New `pages.go` (or fold into `machines.go` — ponytail: fold in, don't create a file unless it gets long):
  - `Landing`: `GET /` → render `pages/landing.html` (full page, no data).
  - `MachinesPage`: `GET /machines` → `ListMachines` query → full page.
  - `NewMachinePage`: `GET /machines/new` → full page.
  - `MachineDetailPage`: `GET /machines/{id}` → machine + `ListSlotsByMachine` + categories → full page; 404 → `renderError` 404 page.
  - `ImportPage`: `GET /import` → full page.

**Routes** (`cmd/server/main.go`) — replace the whole `/api` block:
```go
r.Get("/", h.Landing)
r.Get("/machines", h.MachinesPage)
r.Get("/machines/new", h.NewMachinePage)
r.Post("/machines", h.CreateMachine)
r.Get("/machines/{id}", h.MachineDetailPage)
r.Post("/machines/{id}/delete", h.DeleteMachine)
r.Post("/machines/{id}/slots", h.CreateSlot)
r.Post("/slots/{id}/delete", h.DeleteSlot)
r.Post("/slots/{id}/part", h.AssignPart)   // was fill-slot
r.Post("/parts/{id}/delete", h.DeletePart)
r.Get("/import", h.ImportPage)
r.Post("/import", h.Import)                // merged csv+url
```
Rationale: htmx can't send PUT/DELETE natively without `hx-put`/`hx-delete` — those work fine actually, but plain POST forms keep no-JS fallback working (`hx-boost` optional). ponytail: POST-with-suffix over method-override middleware.

**Verify**: build; `curl -s localhost:8080/ | grep -i rack`; `curl -s localhost:8080/machines | grep -i machine`; `curl -si localhost:8080/machines/9999 | head -1` → `404`; `curl -s localhost:8080/import | grep form`.

## Phase 3 — Machine CRUD over htmx

**machines.go**
- `CreateMachine` (POST `/machines`): `r.ParseForm` → validate name non-empty → `CreateMachine` query (keeps slot seeding) → `w.Header().Set("HX-Redirect", "/machines/{id}")` + 200. On validation error: re-render `pages/new_machine.html` with `Error` + prior values, status 422. Works without JS via standard form POST + 303 redirect — use `http.Redirect` when `HX-Request` header absent:
  ```go
  if r.Header.Get("HX-Request") == "" { http.Redirect(w, r, url, 303); return }
  w.Header().Set("HX-Redirect", url)
  ```
  ponytail: this two-liner everywhere redirects happen — no helper until it repeats 3×.
- `DeleteMachine` (POST `/machines/{id}/delete`): delete → redirect to `/machines` (same pattern).
- `UpdateMachine`, `GetMachine`, `ListMachines`, `ListParts`, `GetPart` — delete (pages absorbed them; edit-machine form is out of scope — name/model shown read-only; add back if user asks).

**Template attrs**
- `pages/machines.html` delete form:
  ```html
  <form hx-post="/machines/{{.ID}}/delete" hx-confirm="Delete {{.Name}}?"
        hx-target="closest li" hx-swap="outerHTML">
  ```
  Handler returns `204` + empty body → row removed. (No-JS fallback: form `action` + redirect; fine to skip fallback on destructive buttons — ponytail: app already requires JS-era browser, htmx is the JS.)
- `pages/new_machine.html`: plain `<form action="/machines" method="post">` + `hx-boost` not needed; add `hx-post="/machines"` `hx-target="body"` for consistency, or leave as native form — pick native, it's the laziest and works.

**Verify**: create via `curl -s -X POST localhost:8080/machines -d 'name=Test&model='` → HX-Redirect header; `curl -s -X POST localhost:8080/machines -d 'name='` → 422 with error text; `curl -s -X POST localhost:8080/machines/1/delete` → 204 or redirect.

## Phase 4 — Slot-card interactions (the core UX)

**Partial**: `templates/partials/slot_card.html` — one slot card:
```html
<div class="slot-card" id="slot-{{.ID}}">
  <h4>{{.CategoryName}}</h4>
  {{if .PartID.Valid}}
    <div class="part">
      {{.PartName}} {{if .PartPrice.Valid}}${{.}}{{end}}
      <button hx-post="/slots/{{.ID}}/part/delete" hx-target="#slot-{{.ID}}" hx-swap="outerHTML">✕</button>
    </div>
  {{else}}
    <form hx-post="/slots/{{.ID}}/part" hx-target="#slot-{{.ID}}" hx-swap="outerHTML">
      <input name="name" placeholder="Part name" required>
      <input name="price" placeholder="Price" inputmode="decimal">
      <button>Add</button>
    </form>
  {{end}}
</div>
```
(Exact fields mirror current Vue slot form — name/price/link per `PartInput` in `parts.go`.)

**Handlers**
- `AssignPart` (POST `/slots/{id}/part`): parse form → create part row + assign to slot in one query path (reuse `CreatePart` params + slot assign SQL already used by `CreateSlot`-adjacent code — check `db/queries.sql` for `AssignPartToSlot`-style query; if missing add to `queries.sql` + `sqlc generate`). Returns `slot_card.html` fragment (re-query `ListSlotsByMachine` filtered to this slot, or build VM directly). Double-fill: unique constraint already enforced — on conflict return fragment with `<p class="error">slot filled</p>` + 422 (`hx-swap` still applies on 422 only if configured; simpler: return 200 with error embedded in fragment. ponytail: 200 + error-in-fragment, no htmx response-targets extension).
- `UnassignPart` (POST `/slots/{id}/part/delete`): clear slot's part (and optionally delete orphan part row — match current Vue behavior: current UI removes assignment; keep semantics). Returns updated `slot_card.html` fragment.
- `CreateSlot` (POST `/machines/{id}/slots`): body `category_id` (from chip). Returns `slot_card.html` fragment for the new slot. Chips partial:
  ```html
  <div id="slot-chips">
    {{range .Cats}}<button hx-post="/machines/{{$.Machine.ID}}/slots"
      hx-vals='{"category_id":"{{.ID}}"}' hx-target="#slot-grid"
      hx-swap="beforeend">{{.Name}}</button>{{end}}
  </div>
  ```
- `DeleteSlot` (POST `/slots/{id}/delete`): unassigns part first (existing behavior in `slots.go`) → return `204` empty; chip button uses `hx-target="#slot-{{.ID}}" hx-swap="outerHTML"` — deleting target with 204 removes it. (htmx swaps nothing on 204; element stays. Use `hx-swap="delete"` instead — cleaner: `hx-swap="delete"` on the button, target `#slot-{{.ID}}`.)

**Verify**: `curl -s -X POST localhost:8080/slots/1/part -d 'name=SSD&price=80'` → HTML containing `SSD`; `curl -s -X POST localhost:8080/slots/1/part/delete` → HTML containing `placeholder="Part name"`; `curl -s -X POST localhost:8080/machines/1/slots -d 'category_id=2'` → new slot card; `curl -si -X POST localhost:8080/slots/9/delete` → 200/204.

## Phase 5 — Import as one transaction

**import.go changes**
- New `Import` (POST `/import`): accepts `multipart/form-data` OR `application/x-www-form-urlencoded` with fields: `machine_name` (optional), `pcpartpicker_url` (optional), `file` (optional CSV). Exactly one of url/file required.
  ```go
  // ponytail: one handler, url wins if both sent — form makes both-set impossible
  tx, _ := sqlDB.BeginTx(...) // needs *sql.DB — pass into handlers.New
  if machine_name != "" { create machine (seeds slots) in tx }
  rows := parseCSV | fetchAndParseHTML(url)
  createPartsFromRows(tx-q, rows, machineID)
  tx.Commit()
  ```
  `createPartsFromRows` already takes `*db.Queries` — build tx queries via `q.WithTx(tx)` (sqlc standard).
- Delete `ImportJSON` + `readJSONImportItems` + `itemToCreateParams` (only existed for the SPA two-step).
- Keep `parseMachineID` only if still used; import now takes `machine_name`, delete it if orphaned.
- Response: `HX-Redirect` to `/machines/{newID}` if machine created, else to `/machines/{id}`; native-form fallback 303 as in Phase 3. Failure (bad URL, parse error, PCPP fetch error): re-render `pages/import.html` with error message + 422.

**Verify**: `curl -s -X POST localhost:8080/import -d 'machine_name=Imp&pcpartpicker_url=bad'` → 422 error text; CSV: `curl -s -X POST localhost:8080/import -F 'machine_name=Imp2' -F 'file=@test.csv'` → HX-Redirect; check DB: `sqlite3 racket.db 'select count(*) from parts'`.

## Phase 6 — Tests

- `machines_test.go`: keep `newTestDB`; rewrite `newTestRouter` to register the new HTML routes (needs `web.New()` pointed at real embedded templates — fine, tests run from module root). Keep the *logic* tests that still apply (`TestCreateMachineSeedsSlots`, `TestDoubleFillSlotRejected` via new endpoint, `TestCreateSlot`, `TestDeleteSlotUnassignsPart`) — change assertions from JSON decode to: status code + `strings.Contains(body, "expected text")`. Delete pure-JSON tests.
- `import_test.go`: all parse-level tests unchanged (`TestParsePrice`, `TestParseCSVToRows*`, `TestParsePartlistHTML*`, `TestPCPPListRegex`, `TestRowToCreateParams`, `TestItemToCreateParams`→delete). Endpoint tests: rewrite `TestImportCSV_Endpoint`/`TestImportURL_*` against `POST /import`; delete `TestImportJSON_*`.
- Add one render test: `web.New()` + render each page template with zero-value data → no error (catches template typos at test time).

**Verify**: `mise run test` green; `mise run lint` clean.

## Phase 7 — Delete the toolchain

**Deleted**
- `frontend/` (entire dir: src, package.json, vite config, tsconfig, node_modules, pnpm-lock.yaml)
- `mise.toml`: remove `node`, `pnpm` tools; remove tasks `frontend/install`, `frontend/build`, `frontend/dev`; `start` → `go run ./cmd/server` (no depends); `build` → plain `go build -ldflags='-s' -o=./bin/racket ./cmd/server` (no depends); `dev` → `go run ./cmd/server` (or delete task; keep alias).
- `.goreleaser.yml` / CI workflows (`.forgejo/workflows/`): remove pnpm/vite steps if present — grep `pnpm` repo-wide to find them.
- Root `package.json`/`pnpm-lock.yaml` if any.
- README/AGENTS.md: update stack section (drop Vite/Vue/pnpm bullets, add "htmx + html/template, assets embedded from internal/web").

**Verify**: `grep -ri pnpm --include='*.yml' --include='*.toml' .` → nothing; `mise run build && ./bin/racket` boots; `git status` shows no stray frontend refs.

---

## Error rendering

| Case | Response |
|---|---|
| Validation (form) | Re-render same page/fragment with `.Error` + 422 (pages) or error-in-fragment 200 (slot ops) |
| Not found (machine) | 404 + `pages/error.html` ("machine not found", link home) |
| Import failure | Re-render import page with specific message (fetch error, parse error, invalid URL) + 422 |
| Constraint (double-fill) | slot_card fragment containing error text, 200 |
| 500 | chi `Recoverer` (already present); templates never panic after startup parse |

## Risks

1. **Template parse at startup** — mitigated by fail-fast `web.New()` error + Phase 6 render test.
2. **HTML escaping** — stdlib `html/template` auto-escapes; never use `template.HTML` for user data (part names from PCPP scrape are untrusted).
3. **CSV upload size** — add `r.Body = http.MaxBytesReader(w, r.Body, 10<<20)` in `Import`.
4. **Tx wiring** — `handlers.New` currently takes only `*db.Queries`; import-in-tx needs `*sql.DB` too. Signature: `New(sqlDB *sql.DB, q *db.Queries, w *web.Web)`. All other handlers stay non-tx (single statements).
5. **htmx 204-no-swap gotcha** — remember `hx-swap="delete"` for removals (noted in Phase 4).
6. **Vue `<style>` extraction** — styles are scoped; global CSS may collide. One file, prefix nothing; fix visually at smoke test (700 LOC app, low risk).
7. **CI/release** — goreleaser/forgejo workflows may invoke pnpm; must be edited in Phase 7 or release breaks.
8. **`sqlc generate` after queries.sql changes** (assign-part query) — run before build.
