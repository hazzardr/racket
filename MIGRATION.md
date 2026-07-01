# Migration: Templ/HTMX to Vue SPA

## Status: Complete

The server-rendered Templ/HTMX UI has been replaced with a Vue 3 Composition API SPA that consumes a Go JSON API. The Go binary embeds the built frontend and serves it as static assets.

## Final Architecture

- `frontend/src/` holds Vue SFCs, API modules, types, and router config.
- `internal/handlers/` exposes a JSON API under `/api/*`.
- `cmd/server/main.go` serves `/api/*` and falls back to the embedded SPA for all other paths.
- `internal/components/`, Templ, HTMX, and Tailwind have been removed.

## API

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/dashboard` | Dashboard stats |
| GET | `/api/machines` | List all machines |
| POST | `/api/machines` | Create a machine |
| GET | `/api/machines/:id` | Get a machine and its parts |
| PUT | `/api/machines/:id` | Update a machine |
| DELETE | `/api/machines/:id` | Delete a machine |
| GET | `/api/parts` | List all parts |
| POST | `/api/parts` | Create a part |
| GET | `/api/parts/:id` | Get a part and its machine |
| PUT | `/api/parts/:id` | Update a part |
| DELETE | `/api/parts/:id` | Delete a part |

## JSON Conventions

- Nullable values are represented as omitted fields or pointers in Go and `null` in JSON.
- Dates are ISO strings (`YYYY-MM-DD`).
- Timestamps are RFC 3339 strings.
- Errors are returned as `{"error": "..."}`.

## Removed

- `internal/components/`
- `assets/css/`
- `github.com/a-h/templ`
- `github.com/templui/templui`
- `templ` CLI tool in `mise.toml`
