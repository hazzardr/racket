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
- PCPartPicker lists import into a machine, one part per slot: paste the
  list's Markup > Text (PCPartPicker shows lists only to signed-in users, so
  Racket can't fetch them). CSV files import too. Pasting the list again
  updates the prices of parts not bought yet; marking a part bought keeps
  the price you paid.
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

## Deploying

Racket is one static binary that embeds its assets. Deployments install it
from a release, and can rely on the following.

- **Releases:** each tag publishes `racket_Linux_x86_64.tar.gz` (also
  `arm64` and `i386`), with the `racket` binary at the archive root, and
  `racket_<version>_checksums.txt` (the tag without its `v`) holding their
  SHA-256 sums.
- **Configuration** is environment variables only (`.env.example` explains
  each one):

  | Variable | In production |
  |---|---|
  | `RACKET_BASE_URL` | The public `https://` URL. Discord's callback is under it, and https makes cookies HTTPS-only |
  | `RACKET_DISCORD_CLIENT_ID`, `_CLIENT_SECRET`, `_GUILD_ID` | All three |
  | `RACKET_HOST` | `127.0.0.1` behind a reverse proxy or tunnel |
  | `PORT` | Default `8080` |
  | `RACKET_DB` | An absolute path to the database file |
  | `RACKET_CLIENT_IP_HEADER` | `Cf-Connecting-IP` behind Cloudflare, only when nothing else can reach the app |
  | `RACKET_DEV_LOGIN` | Never set. It's refused when the base URL is https |

- **Serving:** plain HTTP on `RACKET_HOST:PORT`; TLS ends in front of it.
- **Health:** `GET /healthz` answers `200 ok` when the database answers,
  `503` otherwise. It needs no sign-in.
- **Data:** one SQLite file at `RACKET_DB`. SQLite writes a journal file
  beside it, so the service user needs write access to the directory. Back it
  up with `sqlite3 <db> ".backup <file>"`, which is safe while the server
  runs; copying the file isn't.
- **Migrations** run on startup and only go forward. An older release isn't
  guaranteed to work on a newer schema, so roll back by restoring a backup
  taken before the upgrade, not by installing the old binary alone.
- **Logs** go to stderr: one line per request, plus app events.
- **Stopping:** there's no graceful shutdown, so a stop or restart cuts off
  requests in flight. Each SQLite transaction is atomic, so the database
  can't be left half-written.

## Future Goals

The next goals:

- PCPartPicker import
- Maintenance reminders for tasks like thermal paste and dusting
