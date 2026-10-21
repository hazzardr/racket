# Agent Notes

## Build and Run

Use [mise](https://mise.jdx.dev/) for tool and task management. Run `mise install` first.
The command installs Go and the development tools.

- `mise run start` — Build and start the server on port 8080. Needs Discord
  settings in `.env` (see `.env.example`).
- `mise run dev` — Start the server with dev login (sign in as any username).
- `mise run build` — Build the local Go binary.
- `mise run test` — Run the Go tests.
- `mise run lint` — Run golangci-lint.
- `mise run fmt` — Format the project.

Run `sqlc generate` directly to regenerate the sqlc code. The server applies
migrations on startup. Scaffold a new migration with
`goose sqlite3 ./racket.db -dir migrations create NAME sql`.
Run `mise tasks` to list all tasks.

## Stack

- The server uses Go 1.26 with [chi](https://github.com/go-chi/chi) routing.
- [charmbracelet/log](https://github.com/charmbracelet/log) provides structured
  logging as an `slog` handler.
- The frontend uses htmx + stdlib `html/template`. The app embeds assets from
  `internal/web/`.
- The app uses SQLite via `modernc.org/sqlite` (pure Go, no CGO).
- sqlc generates type-safe SQL queries.
- goose applies database migrations.

## Code Generation

- `sqlc.yaml` configures sqlc. sqlc reads `migrations/` and
  `internal/db/queries.sql`, then generates `internal/db/*.go`.
- Do not edit the generated files by hand.

## Database

- The app stores migration files in `migrations/`. goose applies them on startup.
- The `RACKET_DB` environment variable sets the database file path (default: `racket.db`).

## Auth

- `internal/auth` holds sessions (the cookie token's SHA-256 is stored), the
  `RequireUser` middleware, and the Discord OAuth2 client.
- `internal/auth/authtest` fakes Discord for tests.
- `handlers.(*Handler).Routes` is the whole route table. Everything except
  `/healthz`, `/`, `/logout`, `/auth/*`, and `/static` needs a signed-in
  user. Signed-out visitors land on `/`, whose header has the sign-in buttons.
- Everyone signed in can read everything. Writes are owner-only: check
  ownership in the handler, and also filter the write query on the acting
  user (see the `:execrows` queries in `internal/db/queries.sql`).
- Friends can borrow only parts their owner marked `lendable`; the parts
  page (`/parts`) manages spares and loans.
- Users who leave are soft-deleted (`users.deleted_at`). Read queries hide
  their machines; parts they lent stay in friends' machines. The admin
  command `racket remove-user WHO` (`cmd/server/admin.go`) does this and
  ends their sessions; signing in again restores them.

## Conventions

- The app stores handlers in `internal/handlers/`. They operate on `*db.Queries` and `*web.Web`.
- Full pages render through `h.render`, which wraps the data in `web.Page`
  (templates read `.User`, `.SignIn`, and `.Data`). htmx fragments render bare.
- htmx doesn't swap in 4xx/5xx responses. For an expected failure (stale
  data, bad input), re-render the fragment with a note and a 200. Anything
  else shows as a toast (`internal/web/static/errors.js`).
- The app stores HTML templates and static assets in `internal/web/`.
- `internal/handlers/helpers.go` holds the helper functions that parse request values.

## CI and Releases

- CI runs on the local Forgejo instance (`code.brihome.me`) via `.forgejo/workflows/`:
  `ci.yml` is the one full check, on PRs only; `release.yml` (tag `v*`) checks
  the tagged commit's CI status, re-runs govulncheck and runs GoReleaser;
  `security.yml` is a nightly govulncheck. Tag only commits on main.
- Jobs run in `localhost/brihome-go-ci:1.27`, which has `GOTOOLCHAIN=local`:
  keep `go` in `mise.toml` on the image's Go minor, and golangci-lint on a
  release built with that Go (older builds can't read newer standard-library
  export data).
- The README's "Deploying" section is the deploy contract that the `hosted`
  repo's Ansible relies on: release asset names, environment variables, the
  listen address, `/healthz`, the data path, and migrations. Update it in the
  same change as any of those.
- The git repository is a push mirror to Codeberg for public access.
- CI pulls container images from Docker Hub, not ghcr.
