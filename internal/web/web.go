// Package web serves HTML templates and static assets for the htmx frontend.
package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"racket/internal/db"
)

//go:embed all:templates
var templateFS embed.FS

//go:embed all:static
var staticFS embed.FS

// ponytail: one template set, named blocks; no per-page render struct
type Web struct {
	tmpl *template.Template
}

// Page is the data every full page renders with: the signed-in user (nil
// when signed out) and the request path for the header, and the page's own
// data.
type Page struct {
	User *db.User
	Path string
	Data any
}

var funcs = template.FuncMap{
	// money formats cents as a decimal amount: 123456 -> "1,234.56".
	"money": func(cents int64) string {
		sign := ""
		if cents < 0 {
			sign, cents = "-", -cents
		}
		whole := strconv.FormatInt(cents/100, 10)
		for i := len(whole) - 3; i > 0; i -= 3 {
			whole = whole[:i] + "," + whole[i:]
		}
		return fmt.Sprintf("%s%s.%02d", sign, whole, cents%100)
	},
	"hasPrefix": strings.HasPrefix,
	"ago":       ago,
	// percent is part/total as a whole percentage, 0 when total is 0.
	"percent": func(part, total int64) int64 {
		if total == 0 {
			return 0
		}
		return part * 100 / total
	},
}

// agoToday is what ago says for anything in the last day.
const agoToday = "today"

// ago says roughly how long ago t was, in whole days: "today",
// "yesterday", "3 days ago".
func ago(t time.Time) string {
	switch days := int(time.Since(t).Hours() / 24); {
	case days < 1: // also a little in the future, from clock skew
		return agoToday
	case days == 1:
		return "yesterday"
	default:
		return strconv.Itoa(days) + " days ago"
	}
}

func New() (*Web, error) {
	var files []string
	err := fs.WalkDir(templateFS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".html") {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk templates: %w", err)
	}

	// Template names default to the base filename (layout.html, landing.html, ...).
	tmpl, err := template.New("").Funcs(funcs).ParseFS(templateFS, files...)
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return &Web{tmpl: tmpl}, nil
}

// Render executes a full page template (layout + content block). The template
// is executed into a buffer first so a failure yields a 500 instead of a
// truncated page with the caller's status.
func (w *Web) Render(rw http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := w.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		slog.Error("render template", "template", name, "error", err)
		http.Error(rw, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.WriteHeader(status)
	_, _ = buf.WriteTo(rw)
}

// Fragment executes a partial template (no layout) for htmx swaps.
func (w *Web) Fragment(rw http.ResponseWriter, name string, data any) {
	w.Render(rw, http.StatusOK, name, data)
}

// Static returns the embedded static asset subtree (css, js, images).
func (w *Web) Static() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // embed layout is fixed at compile time
	}
	return sub
}
