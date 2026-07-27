package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/charmbracelet/log"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/pressly/goose/v3"

	"racket"
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

	r.Route("/api", func(r chi.Router) {
		r.Get("/dashboard", h.Dashboard)

		r.Route("/machines", func(r chi.Router) {
			r.Get("/", h.ListMachines)
			r.Post("/", h.CreateMachine)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", h.GetMachine)
				r.Put("/", h.UpdateMachine)
				r.Delete("/", h.DeleteMachine)
				r.Post("/slots", h.CreateSlot)
			})
		})

		r.Route("/parts", func(r chi.Router) {
			r.Get("/", h.ListParts)
			r.Post("/", h.CreatePart)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", h.GetPart)
				r.Put("/", h.UpdatePart)
				r.Delete("/", h.DeletePart)
			})
		})

		r.Route("/slots", func(r chi.Router) {
			r.Delete("/{id}", h.DeleteSlot)
		})
	})

	spa, err := newSPAHandler(racket.FrontendDist)
	if err != nil {
		return fmt.Errorf("create spa handler: %w", err)
	}
	r.Get("/*", spa.ServeHTTP)

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

type spaHandler struct {
	static fs.FS
	index  []byte
}

func newSPAHandler(embedded embed.FS) (*spaHandler, error) {
	dist, err := fs.Sub(embedded, "frontend/dist")
	if err != nil {
		return nil, fmt.Errorf("open frontend/dist: %w", err)
	}

	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return nil, fmt.Errorf("read index.html: %w", err)
	}

	return &spaHandler{static: dist, index: index}, nil
}

func (s *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" {
		s.serveIndex(w)
		return
	}

	file, err := s.static.Open(path[1:])
	if err != nil {
		s.serveIndex(w)
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil || stat.IsDir() {
		s.serveIndex(w)
		return
	}

	rs, ok := file.(io.ReadSeeker)
	if !ok {
		s.serveIndex(w)
		return
	}

	http.ServeContent(w, r, path, stat.ModTime(), rs)
}

func (s *spaHandler) serveIndex(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.index)
}
