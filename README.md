# Racket

Racket (Rack-it) is a small web app for tracking hardware and planning builds
with friends. A machine is a server, a desktop, a NAS box, or any other device
with parts inside.

## Features

- Friends sign in with Discord. Only members of one Discord server can sign in.
- Everyone signed in can see every machine; only the owner can change it.
- The app groups hardware into machines, each with slots (CPU, RAM, GPU...).
- Machines can be planned, active, or retired. Parts can be not bought yet,
  so a planned build shows what it still needs and what it costs.
- You can put a friend's spare part in your machine. It stays theirs, and
  either of you can take it out again. Slots show whether a part is yours,
  borrowed, or not bought yet.
- PCPartPicker lists and CSVs import into a machine, one part per slot.
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

Then start the server with dev login, which lets you sign in as any username
without Discord:

```bash
mise run dev
```

Then open <http://localhost:8080>. Sign in as two different users in two
browsers (or a private window) to see sharing: each of you can view the
other's machines but only change your own.

### Discord sign-in

Copy `.env.example` to `.env` and fill in the Discord settings; the file
explains each one. mise loads `.env` for every task. Then build and run the
server without dev login:

```bash
mise run start
```

The server refuses to start without a way to sign in, and refuses dev login
when `RACKET_BASE_URL` is https.

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
