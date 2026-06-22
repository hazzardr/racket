package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/charmbracelet/log"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/pressly/goose/v3"

	"racket/internal/db"
	"racket/internal/handlers"

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

func run() error {
	dsn := os.Getenv("RACKET_DB")
	if dsn == "" {
		dsn = "racket.db"
	}

	sqlDB, err := sql.Open("sqlite", dsn)
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
	h := handlers.New(queries)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	r.Get("/", h.Home)

	r.Route("/machines", func(r chi.Router) {
		r.Get("/", h.ListMachines)
		r.Get("/new", h.NewMachineForm)
		r.Post("/", h.CreateMachine)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", h.GetMachine)
			r.Get("/edit", h.EditMachineForm)
			r.Put("/", h.UpdateMachine)
			r.Delete("/", h.DeleteMachine)
		})
	})

	r.Route("/parts", func(r chi.Router) {
		r.Get("/", h.ListParts)
		r.Get("/new", h.NewPartForm)
		r.Post("/", h.CreatePart)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", h.GetPart)
			r.Get("/edit", h.EditPartForm)
			r.Put("/", h.UpdatePart)
			r.Delete("/", h.DeletePart)
		})
	})

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
