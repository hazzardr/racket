package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/pressly/goose/v3"

	"racket/internal/auth"
	"racket/internal/db"
	"racket/internal/handlers"
	"racket/internal/web"
	"racket/migrations"

	_ "modernc.org/sqlite"
)

func init() {
	if err := goose.SetDialect("sqlite3"); err != nil {
		panic(err)
	}
	goose.SetBaseFS(migrations.FS)
}

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

// Environment variables for Discord sign-in.
const (
	envDiscordClientID     = "RACKET_DISCORD_CLIENT_ID"
	envDiscordClientSecret = "RACKET_DISCORD_CLIENT_SECRET"
	envDiscordGuildID      = "RACKET_DISCORD_GUILD_ID"
)

const schemeHTTPS = "https"

// config is the server configuration, read from the environment.
type config struct {
	dbPath string
	// host is the address to listen on; empty means every interface.
	host    string
	port    string
	baseURL *url.URL
	// clientIPHeader names the request header that holds the client's
	// address, set by a reverse proxy; empty means use the connection's.
	clientIPHeader string
	// discord is nil when Discord sign-in isn't configured.
	discord  *auth.DiscordConfig
	devLogin bool
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// loadConfig reads the configuration. At least one way to sign in must be
// set up: Discord, or dev login for local development.
func loadConfig() (config, error) {
	cfg := config{
		dbPath:         getenv("RACKET_DB", "racket.db"),
		host:           os.Getenv("RACKET_HOST"),
		port:           getenv("PORT", "8080"),
		clientIPHeader: os.Getenv("RACKET_CLIENT_IP_HEADER"),
		devLogin:       os.Getenv("RACKET_DEV_LOGIN") == "1",
	}

	rawBase := strings.TrimSuffix(getenv("RACKET_BASE_URL", "http://localhost:"+cfg.port), "/")
	base, err := url.Parse(rawBase)
	if err != nil || (base.Scheme != "http" && base.Scheme != schemeHTTPS) || base.Host == "" {
		return config{}, fmt.Errorf("RACKET_BASE_URL must be an http(s) URL like https://racket.example.com, got %q", rawBase)
	}
	cfg.baseURL = base

	discordVars := []string{envDiscordClientID, envDiscordClientSecret, envDiscordGuildID}
	var missing []string
	for _, key := range discordVars {
		if os.Getenv(key) == "" {
			missing = append(missing, key)
		}
	}
	switch len(missing) {
	case 0:
		cfg.discord = &auth.DiscordConfig{
			ClientID:     os.Getenv(envDiscordClientID),
			ClientSecret: os.Getenv(envDiscordClientSecret),
			GuildID:      os.Getenv(envDiscordGuildID),
			RedirectURL:  rawBase + "/auth/discord/callback",
		}
	case len(discordVars):
	default:
		return config{}, fmt.Errorf("partly configured Discord sign-in: also set %s", strings.Join(missing, ", "))
	}

	if cfg.devLogin && base.Scheme == schemeHTTPS {
		return config{}, errors.New("RACKET_DEV_LOGIN lets anyone sign in as anyone, " +
			"so it's refused when RACKET_BASE_URL is https (a real deployment)")
	}
	if cfg.discord == nil && !cfg.devLogin {
		return config{}, errors.New("no way to sign in: set RACKET_DISCORD_CLIENT_ID, " +
			"RACKET_DISCORD_CLIENT_SECRET, and RACKET_DISCORD_GUILD_ID, " +
			"or RACKET_DEV_LOGIN=1 for local development")
	}
	return cfg, nil
}

// sqliteDSN turns a plain path into a file: URI with foreign key enforcement
// and a busy timeout. SQLite defaults PRAGMA foreign_keys to off, which would
// silently disable the schema's ON DELETE CASCADE/SET NULL clauses. Without
// busy_timeout, a write that hits another connection's lock fails immediately
// with "database is locked" instead of waiting.
func sqliteDSN(path string) string {
	if !strings.HasPrefix(path, "file:") {
		path = "file:" + path
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	sqlDB, err := sql.Open("sqlite", sqliteDSN(cfg.dbPath))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer sqlDB.Close()

	if err := sqlDB.PingContext(context.Background()); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	if err := goose.Up(sqlDB, "."); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	webUI, err := web.New()
	if err != nil {
		return fmt.Errorf("init web: %w", err)
	}
	h := newHandler(sqlDB, webUI, cfg)
	if cfg.devLogin {
		slog.Warn("dev login is on: anyone can sign in as anyone. Never expose this server.")
	}
	if cfg.discord != nil {
		slog.Info("Discord sign-in is on", "redirect_url", cfg.discord.RedirectURL)
	}

	server := &http.Server{
		Addr:         net.JoinHostPort(cfg.host, cfg.port),
		Handler:      clientIP(cfg.clientIPHeader, middleware.Logger(h.Routes())),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	slog.Info("racket listening", "addr", server.Addr, "base_url", cfg.baseURL.String())
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("server error: %w", err)
	}
	return nil
}

// clientIP sets r.RemoteAddr from the header a reverse proxy puts the
// client's address in (Cloudflare's Cf-Connecting-IP), so the request log
// shows the client instead of the proxy. Trust only a header the proxy always
// overwrites, on a server only the proxy can reach: anyone else can send it.
// Without a header name, or with a value that isn't an IP, RemoteAddr stays.
func clientIP(header string, next http.Handler) http.Handler {
	if header == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get(header))); err == nil {
			r.RemoteAddr = ip.Unmap().String()
		}
		next.ServeHTTP(w, r)
	})
}

// newHandler wires the handlers to the database, templates, and sign-in.
func newHandler(sqlDB *sql.DB, webUI *web.Web, cfg config) *handlers.Handler {
	queries := db.New(sqlDB)
	secure := cfg.baseURL.Scheme == schemeHTTPS
	a := handlers.Auth{
		Sessions:      auth.NewSessions(queries, secure),
		DevLogin:      cfg.devLogin,
		SecureCookies: secure,
	}
	if cfg.discord != nil {
		a.Discord = auth.NewDiscord(*cfg.discord)
	}
	return handlers.New(sqlDB, queries, webUI, a)
}
