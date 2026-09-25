# Agent Notes

## Build and Run

Use [mise](https://mise.jdx.dev/) for tool and task management. Run `mise install` first.
The command installs Go and the development tools.

- `mise run start` — Start the server on port 8080.
- `mise run dev` — Start the server for development.
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

## Conventions

- The app stores handlers in `internal/handlers/`. They operate on `*db.Queries` and `*web.Web`.
- The app stores HTML templates and static assets in `internal/web/`.
- `internal/handlers/helpers.go` holds the helper functions that parse request values.

## CI and Releases

- CI runs on the local Forgejo instance (`code.brihome.me`) via `.forgejo/workflows/`.
- GoReleaser publishes releases to the local Forgejo instance.
- The git repository is a push mirror to Codeberg for public access.
- CI pulls container images from Docker Hub, not ghcr.
