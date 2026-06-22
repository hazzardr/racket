# Agent Notes

## Build & Run

This project uses [mise](https://mise.jdx.dev/) for tool and task management.
Run `mise install` first to set up Go and development tools.

- `mise run run` — run the server on port 8080.
- `mise run dev` — run `templ generate --watch` with the server for development.
- `mise run generate` — regenerate sqlc and templ code.
- `mise run build` — build binaries for local and linux/amd64.
- `mise run test` — run Go tests.
- `mise run lint` — run golangci-lint.
- `mise run fmt` — format the project.
- `mise run db/migrate` — run database migrations with goose.

See `mise run help` for the full task list.

## Stack

- Go 1.26 with [chi](https://github.com/go-chi/chi) routing.
- Structured logging via [charmbracelet/log](https://github.com/charmbracelet/log) as an `slog` handler.
- Templ for HTML components; HTMX for interactivity.
- SQLite via `modernc.org/sqlite` (pure Go, no CGO).
- sqlc for type-safe SQL queries.
- goose for database migrations.

## Code Generation

- `sqlc.yaml` configures sqlc to read `migrations/` and
  `internal/db/queries.sql` and generate `internal/db/*.go`.
- Templ files live in `internal/components/` and generate
  `internal/components/*_templ.go`.
- Do not edit generated files by hand.

## Database

- Migration files live in `migrations/` and are applied on startup using goose.
- `RACKET_DB` env var sets the database file path (default: `racket.db`).

## Conventions

- Handlers live in `internal/handlers/` and operate on `*db.Queries`.
- Components live in `internal/components/`.
- Helper functions for parsing form values and formatting are in
  `internal/components/helpers.go`.

## CI & Releases

- CI runs on Codeberg's Woodpecker CI via `.woodpecker.yml`.
- Releases are published to Codeberg (Forgejo/Gitea) using GoReleaser.
- Container images for CI are pulled from Docker Hub instead of ghcr.
