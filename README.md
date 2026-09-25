# Racket

Racket (Rack-it) is a small web app for managing and tracking hardware across
multiple machines. A machine is a server, a desktop, a NAS box, or any other
device with parts inside.

## Features

- The app groups hardware into machines.
- The app tracks machine stats: build date, purchase date, location.
- The app tracks part stats: category, manufacturer, model, serial number,
  warranty, purchase info, and guide URL.
- The frontend uses htmx + stdlib `html/template`.
- The app embeds all assets into the Go binary. The build is one executable.
- The database is SQLite with type-safe queries from sqlc. goose applies
  migrations on startup.

## Tech Stack

- Go 1.26
- [chi](https://github.com/go-chi/chi) router
- [charmbracelet/log](https://github.com/charmbracelet/log) for structured logging
- htmx (vendored) + stdlib `html/template`
- SQLite (via `modernc.org/sqlite`, no CGO)
- sqlc + goose

## Getting Started

Install [mise](https://mise.jdx.dev/). Then install the dependencies:

```bash
mise install
```

Then start the server:

```bash
mise run start
```

Then open <http://localhost:8080>.

List all tasks:

```bash
mise tasks
```

## Project Layout

```
cmd/server/         Application entrypoint
internal/
  db/               sqlc-generated code + queries
  handlers/         HTTP handlers
  web/              HTML templates + static assets (embedded)
migrations/         goose migration files
```

## CI and Releases

- The source repository is <https://code.brihome.me/brian/racket> on Forgejo.
- A push mirror on Codeberg gives the project public access.
- CI runs on Forgejo Actions (`.forgejo/workflows/`).
- GoReleaser publishes releases to Forgejo.

## Future Goals

The next goals:

- PCPartPicker import
- Maintenance reminders for tasks like thermal paste and dusting
