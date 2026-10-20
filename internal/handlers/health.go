package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// healthTimeout bounds the database check, so a stuck database fails the
// health check instead of hanging it.
const healthTimeout = 2 * time.Second

// Healthz answers 200 "ok" when the server is up and its database answers,
// and 503 otherwise. It needs no sign-in, for deploy checks and monitoring.
func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
	defer cancel()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.db.PingContext(ctx); err != nil {
		slog.ErrorContext(ctx, "health check: database", "error", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("database unavailable\n"))
		return
	}
	_, _ = w.Write([]byte("ok\n"))
}
