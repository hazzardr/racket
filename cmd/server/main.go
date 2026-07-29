package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/pressly/goose/v3"

	"racket/internal/db"
	"racket/internal/handlers"
	"racket/internal/web"

	_ "modernc.org/sqlite"
)

func main() {
	logger := log.NewWithOptions(os.Stderr, log.Options{
		ReportCaller:    true,
		ReportTimestamp: true,
		TimeFormat:      time.Kitchen,
	})
	slog.SetDefault(slog.New(logger))

	if err := run(); err != nil {
		slog.Error("failed to start racket", "error", err)
		os.Exit(1)
	}
}

// sqliteDSN turns a plain path into a file: URI with foreign key enforcement
// enabled. SQLite defaults PRAGMA foreign_keys to off, which would silently
// disable the schema's ON DELETE CASCADE/SET NULL clauses.
func sqliteDSN(path string) string {
	if !strings.HasPrefix(path, "file:") {
		path = "file:" + path
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + "_pragma=foreign_keys(1)"
}

func run() error {
	dsn := os.Getenv("RACKET_DB")
	if dsn == "" {
		dsn = "racket.db"
	}

	sqlDB, err := sql.Open("sqlite", sqliteDSN(dsn))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer sqlDB.Close()

	if err := sqlDB.PingContext(context.Background()); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	if err := goose.SetDialect(string(goose.DialectSQLite3)); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	queries := db.New(sqlDB)

	webUI, err := web.New()
	if err != nil {
		return fmt.Errorf("init web: %w", err)
	}
	h := handlers.New(sqlDB, queries, webUI)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/", h.Landing)
	r.Get("/machines", h.MachinesPage)
	r.Get("/machines/new", h.NewMachinePage)
	r.Post("/machines", h.CreateMachine)
	r.Get("/machines/{id}", h.MachineDetailPage)
	r.Post("/machines/{id}/delete", h.DeleteMachine)
	r.Post("/machines/{id}/slots", h.CreateSlot)
	r.Post("/slots/{id}/part", h.AssignPart)
	r.Post("/slots/{id}/part/delete", h.UnassignPart)
	r.Post("/slots/{id}/delete", h.DeleteSlot)
	r.Post("/parts/{id}/delete", h.DeletePart)
	r.Get("/import", h.ImportPage)
	r.Post("/import", h.Import)

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServerFS(webUI.Static())))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	slog.Info("racket listening", "addr", "http://localhost:"+port)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("server error: %w", err)
	}
	return nil
}
