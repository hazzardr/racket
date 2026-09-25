# htmx Migration Plan (racket)

This plan replaces the Vue 3 SPA with stdlib `html/template` + htmx. It also
deletes the JSON API and the entire frontend toolchain.

The locked decisions:

- Stdlib templates only
- Vendored and embedded htmx
- No Alpine
- JSON API deleted
- Import in one transaction
- Dark CSS in one static file
- Templates parsed once at startup

---

## Phase 1 — Template/static foundation (the binary compiles and serves)

**New files**
- `templates/layout.html` — `<!doctype html>`, `<head>` with `/static/app.css` + `/static/htmx.min.js` (defer), nav (Racket logo → `/`, links: Machines, New Machine, footer import link), `{{block "content" .}}{{end}}`.
- `templates/pages/.gitkeep` (the real pages arrive in Phase 2)
- `internal/web/web.go` — one file:
  - `//go:embed all:templates` → `templateFS` (move `templates/` under `internal/web/` so the embed is local. Adjust the paths to `internal/web/templates/...`).
  - `//go:embed all:static` → `StaticFS` (`internal/web/static/`).
  - `func New() (*Web, error)` — call `template.ParseFS` once. Walk and parse all `templates/**/*.html`. Return an error if the parse fails.
  - `func (w *Web) Render(rw http.ResponseWriter, status int, name string, data any)` — call `ExecuteTemplate(rw, name, data)`. Set `Content-Type: text/html; charset=utf-8`.
  - `func (w *Web) Fragment(rw, name, data)` — same, but no layout (partials).
  - `// ponytail: one template set, named blocks; no per-page render struct`
- `internal/web/static/htmx.min.js` — vendored htmx 2.x, committed.
- `internal/web/static/app.css` — the dark theme extracted from the Vue `<style>` blocks (bg `#1a1a1a`, text `#e0e0e0`, accent `#4a9eff`, slot card and drag-over styles, landing page styles).

**Changed**
- `cmd/server/main.go`:
  - Delete `spaHandler`, `newSPAHandler`, `serveIndex` (the whole bottom block).
  - Delete the import of the root `racket` package.
  - Add `web, err := web.New()`. Fail startup on error.
  - `r.Handle("/static/*", http.FileServerFS(web.Static()))` — or an `fs.Sub` wrapper if the route paths do not match.
  - Pass `web` into `handlers.New(q, web)`.

**Deleted**
- `frontend_embed.go`

**Check:**

- `mise run build` compiles.
- `go run ./cmd/server` starts the server.
- `curl -s localhost:8080/static/htmx.min.js | head -c 60` returns htmx code.
- `curl -s localhost:8080/static/app.css | head -c 60` returns CSS.
- `curl -si localhost:8080/ | head -5` (the route can return 404 until Phase 2).

## Phase 2 — Pages and page handlers

**New templates** (all under `internal/web/templates/`)
- `pages/landing.html` — the landing content from `LandingView.vue` (RACK / ET art, tagline, Enter button → `/machines`, a small `/import` link).
- `pages/machines.html` — the machine list: name link, `{{if .PurchaseDate}}`, price total, slot-fill count, delete form. Data: `[]db.ListMachinesRow` (reuse the existing query) + the computed fill counts.
- `pages/new_machine.html` — one form matching `MachineInput` and the current Vue fields: `name` (required), `description`, `location`, `built_on` (date), `purchased_on` (date).
- `pages/machine_detail.html` — the header (name, edit/delete) + the slot grid. The slot loop:
  ```html
  {{range .Slots}}{{template "slot_card" .}}{{end}}
  {{template "category_chips" .}}
  ```
  The data struct:
  ```go
  type MachinePage struct {
      Machine db.Machine
      Slots   []SlotVM   // db.ListSlotsByMachineRow + Part fields flattened
      Cats    []db.Category // for chips
  }
  ```
- `pages/import.html` — two sections: (a) the PCPartPicker URL form, (b) the CSV file form. Both have an optional `machine_name` field.

**Changed handlers** (`internal/handlers/`)
- `handlers.go`:
  - `New(q *db.Queries, w *web.Web) *Handler` — store both.
  - Delete `writeJSON`, `writeError`, `decodeJSON`, `MachineResponse`/`PartResponse`/`SlotResponse` + `new*Response` (all the JSON shapes).
  - Add `renderError(w, r, status, msg)` — it renders a tiny `error.html` partial (or a plain `<p class="error">` fragment). Use it everywhere validation fails.
  - `Dashboard` — delete it (a Vue-only aggregate. The machines page shows the same info).
- New `pages.go` (or fold it into `machines.go` — ponytail: fold it in. Do not create a file unless it gets long):
  - `Landing`: `GET /` → render `pages/landing.html` (a full page, no data).
  - `MachinesPage`: `GET /machines` → the `ListMachines` query → a full page.
  - `NewMachinePage`: `GET /machines/new` → a full page.
  - `MachineDetailPage`: `GET /machines/{id}` → the machine + `ListSlotsByMachine` + the categories → a full page. A 404 renders the `renderError` 404 page.
  - `ImportPage`: `GET /import` → a full page.

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
Rationale: htmx cannot send PUT or DELETE without `hx-put` or `hx-delete`.
Plain POST forms keep the no-JS fallback working. `hx-boost` is optional.
ponytail: POST-with-suffix over method-override middleware.

**Check:**

- `mise run build` compiles.
- `curl -s localhost:8080/ | grep -i rack` returns a match.
- `curl -s localhost:8080/machines | grep -i machine` returns a match.
- `curl -si localhost:8080/machines/9999 | head -1` returns `404`.
- `curl -s localhost:8080/import | grep form` returns a match.

## Phase 3 — Machine CRUD over htmx

**machines.go**
- `CreateMachine` (POST `/machines`): `r.ParseForm` → validate that the name is not empty → the `CreateMachine` query (keeps slot seeding) → `w.Header().Set("HX-Redirect", "/machines/{id}")` + 200. If validation fails, re-render `pages/new_machine.html` with `Error` + the prior values. Use status 422. The form works without JS via a standard POST + 303 redirect. If the `HX-Request` header is absent, use `http.Redirect`:
  ```go
  if r.Header.Get("HX-Request") == "" { http.Redirect(w, r, url, 303); return }
  w.Header().Set("HX-Redirect", url)
  ```
  ponytail: this two-liner everywhere redirects happen — no helper until it repeats 3×.
- `DeleteMachine` (POST `/machines/{id}/delete`): delete → redirect to `/machines` (the same pattern).
- Delete `UpdateMachine`, `GetMachine`, `ListMachines`, `ListParts`, `GetPart`. The pages absorbed them. The edit-machine form is out of scope: the name and model are read-only. Add the form back if the user asks.

**Template attrs**
- `pages/machines.html` delete form:
  ```html
  <form hx-post="/machines/{{.ID}}/delete" hx-confirm="Delete {{.Name}}?"
        hx-target="closest li" hx-swap="outerHTML">
  ```
  The handler returns `204` + an empty body. The row is removed. The no-JS
  fallback is a form `action` + redirect. Skip the fallback on destructive
  buttons. ponytail: the app already requires a JS-era browser. htmx is the JS.
- `pages/new_machine.html`: use a plain `<form action="/machines" method="post">`. `hx-boost` is not needed. Keep the form native: the native form is the laziest option and it works.

**Check:**

- `curl -s -X POST localhost:8080/machines -d 'name=Test&model='` returns the `HX-Redirect` header.
- `curl -s -X POST localhost:8080/machines -d 'name='` returns 422 + the error text.
- `curl -s -X POST localhost:8080/machines/1/delete` returns 204 or a redirect.

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
The fields mirror the current Vue slot form: name, price, link, per `PartInput` in `parts.go`.

**Handlers**
- `AssignPart` (POST `/slots/{id}/part`): parse the form. Create the part row and assign it to the slot in one query path. Reuse the `CreatePart` params + the slot-assign SQL that the `CreateSlot`-adjacent code already uses. Check `db/queries.sql` for an `AssignPartToSlot`-style query. If the query is missing, add it to `queries.sql`. Then run `sqlc generate`. Return the `slot_card.html` fragment. Re-query `ListSlotsByMachine` filtered to this slot, or build the VM directly. The unique constraint rejects a double fill. On conflict, return the fragment with `<p class="error">slot filled</p>` + 422. Note: `hx-swap` applies to 422 only if configured. Simpler: return 200 with the error in the fragment. ponytail: 200 + error-in-fragment, no htmx response-targets extension.
- `UnassignPart` (POST `/slots/{id}/part/delete`): clear the part in the slot. Delete the orphan part row only if the current Vue code does so. Return the updated `slot_card.html` fragment.
- `CreateSlot` (POST `/machines/{id}/slots`): the body is `category_id` (from the chip). Return the `slot_card.html` fragment for the new slot. The chip partial:
  ```html
  <div id="slot-chips">
    {{range .Cats}}<button hx-post="/machines/{{$.Machine.ID}}/slots"
      hx-vals='{"category_id":"{{.ID}}"}' hx-target="#slot-grid"
      hx-swap="beforeend">{{.Name}}</button>{{end}}
  </div>
  ```
- `DeleteSlot` (POST `/slots/{id}/delete`): unassign the part first (the existing behavior in `slots.go`). Return an empty `204`. The chip button uses `hx-target="#slot-{{.ID}}"`. htmx swaps nothing on 204, so the element stays. Use `hx-swap="delete"` on the button instead. Then the target `#slot-{{.ID}}` is removed.

**Check:**

- `curl -s -X POST localhost:8080/slots/1/part -d 'name=SSD&price=80'` returns HTML containing `SSD`.
- `curl -s -X POST localhost:8080/slots/1/part/delete` returns HTML containing `placeholder="Part name"`.
- `curl -s -X POST localhost:8080/machines/1/slots -d 'category_id=2'` returns the new slot card.
- `curl -si -X POST localhost:8080/slots/9/delete` returns 200 or 204.

## Phase 5 — Import as one transaction

**import.go changes**
- New `Import` (POST `/import`): accept `multipart/form-data` or `application/x-www-form-urlencoded`. The fields: `machine_name` (optional), `pcpartpicker_url` (optional), `file` (optional CSV). The request requires exactly one of url or file.
  ```go
  // ponytail: one handler, url wins if both sent — form makes both-set impossible
  tx, _ := sqlDB.BeginTx(...) // needs *sql.DB — pass into handlers.New
  if machine_name != "" { create machine (seeds slots) in tx }
  rows := parseCSV | fetchAndParseHTML(url)
  createPartsFromRows(tx-q, rows, machineID)
  tx.Commit()
  ```
  `createPartsFromRows` already takes `*db.Queries`. Build the tx queries via `q.WithTx(tx)` (the sqlc standard).
- Delete `ImportJSON` + `readJSONImportItems` + `itemToCreateParams`. They existed only for the SPA two-step.
- Keep `parseMachineID` only if it is still used. The import now takes `machine_name`. Delete the function if it is orphaned.
- Response: if the app created a machine, set `HX-Redirect` to `/machines/{newID}`. Otherwise set it to `/machines/{id}`. Use the native-form 303 fallback from Phase 3. On failure (a bad URL, a parse error, a PCPP fetch error), re-render `pages/import.html` with the error message + 422.

**Check:**

- `curl -s -X POST localhost:8080/import -d 'machine_name=Imp&pcpartpicker_url=bad'` returns 422 + the error text.
- `curl -s -X POST localhost:8080/import -F 'machine_name=Imp2' -F 'file=@test.csv'` returns `HX-Redirect`.
- `sqlite3 racket.db 'select count(*) from parts'` returns the new row count.

## Phase 6 — Tests

- `machines_test.go`: keep `newTestDB`. Rewrite `newTestRouter` to register the new HTML routes. It needs `web.New()` pointed at the real embedded templates. The tests run from the module root, so the paths are fine. Keep the logic tests that still apply: `TestCreateMachineSeedsSlots`, `TestDoubleFillSlotRejected` (via the new endpoint), `TestCreateSlot`, `TestDeleteSlotUnassignsPart`. Change the assertions from a JSON decode to status code + `strings.Contains(body, "expected text")`. Delete the pure-JSON tests.
- `import_test.go`: keep all parse-level tests unchanged: `TestParsePrice`, `TestParseCSVToRows*`, `TestParsePartlistHTML*`, `TestPCPPListRegex`, `TestRowToCreateParams`. Delete `TestItemToCreateParams`. Rewrite the endpoint tests `TestImportCSV_Endpoint` and `TestImportURL_*` against `POST /import`. Delete `TestImportJSON_*`.
- Add one render test: render each page template with zero-value data. Expect no error. This catches template typos at test time.

**Check:**

- `mise run test` passes.
- `mise run lint` is clean.

## Phase 7 — Delete the toolchain

**Deleted**
- `frontend/` (the entire directory: src, package.json, vite config, tsconfig, node_modules, pnpm-lock.yaml)
- `mise.toml`: remove the `node` and `pnpm` tools. Remove the tasks `frontend/install`, `frontend/build`, `frontend/dev`. Set `start` to `go run ./cmd/server` (no depends). Set `build` to plain `go build -ldflags='-s' -o=./bin/racket ./cmd/server` (no depends). Set `dev` to `go run ./cmd/server` (or delete the task and keep the alias).
- `.goreleaser.yml` and the CI workflows (`.forgejo/workflows/`): if the pnpm and vite steps are present, remove them. Run `grep` for `pnpm` across the repo to find them.
- The root `package.json` and `pnpm-lock.yaml` (if they exist).
- README.md and AGENTS.md: update the stack section. Remove the Vite, Vue, and pnpm bullets. Add "htmx + html/template, assets embedded from internal/web".

**Check:**

- `grep -ri pnpm --include='*.yml' --include='*.toml' .` returns nothing.
- `mise run build && ./bin/racket` starts the server.
- `git status` shows no remaining frontend references.

---

## Error rendering

| Case | Response |
|---|---|
| Validation (form) | Re-render the same page or fragment with `.Error` + 422 (pages) or the error-in-fragment 200 (slot ops) |
| Not found (machine) | 404 + `pages/error.html` ("machine not found", a link home) |
| Import failure | Re-render the import page with the specific message (fetch error, parse error, invalid URL) + 422 |
| Constraint (double-fill) | The slot_card fragment containing the error text, 200 |
| 500 | chi `Recoverer` (already present). The templates never panic after the startup parse |

## Risks

1. **Template parse at startup** — the fail-fast `web.New()` error + the Phase 6 render test cover this.
2. **HTML escaping** — the stdlib `html/template` auto-escapes output. Do not use `template.HTML` for user data. The part names from a PCPP scrape are untrusted.
3. **CSV upload size** — add `r.Body = http.MaxBytesReader(w, r.Body, 10<<20)` in `Import`.
4. **Tx wiring** — `handlers.New` currently takes only `*db.Queries`. The in-tx import needs `*sql.DB` too. Use the signature `New(sqlDB *sql.DB, q *db.Queries, w *web.Web)`. All other handlers stay non-tx (single statements).
5. **htmx 204 behavior** — htmx swaps nothing on 204. Use `hx-swap="delete"` for removals (noted in Phase 4).
6. **Vue `<style>` extraction** — the Vue styles are scoped. The global CSS can collide. Use one file with no prefixes. Fix collisions visually at the smoke test (the app is about 700 LOC, so the risk is low).
7. **CI and release** — the goreleaser and forgejo workflows can invoke pnpm. Edit them in Phase 7, or the release breaks.
8. **`sqlc generate`** — run it after the `queries.sql` changes (the assign-part query). Run it before the build.
