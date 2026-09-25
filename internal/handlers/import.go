package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"racket/internal/db"
)

var (
	priceRe = regexp.MustCompile(`[^\d.]`)

	// pcppClient is used to fetch PCPartPicker export CSVs and pages.
	pcppClient = &http.Client{Timeout: pcppTimeout, CheckRedirect: checkPCPPRedirect}
)

const (
	// maxImportBody bounds the import request body (form fields + CSV upload).
	maxImportBody = 10 << 20

	// pcppTimeout bounds a single PCPartPicker fetch.
	pcppTimeout = 30 * time.Second

	// maxPCPPBody caps a PCPartPicker response; list pages are a few hundred KB.
	maxPCPPBody = 5 << 20

	// pcppHost is the only host import fetches from (plus its www. alias).
	pcppHost = "pcpartpicker.com"

	// maxPCPPRedirects matches net/http's default redirect limit.
	maxPCPPRedirects = 10

	// importWriteTimeout covers the CSV export fetch plus the HTML fallback
	// fetch, with headroom for the database writes.
	importWriteTimeout = 2*pcppTimeout + 10*time.Second
)

// importPage is the import form state (also used for failure re-renders).
type importPage struct {
	Error       string
	MachineName string
	PCPPURL     string
}

// ImportPage renders GET /import.
func (h *Handler) ImportPage(w http.ResponseWriter, _ *http.Request) {
	h.web.Render(w, http.StatusOK, "import.html", importPage{})
}

// Import handles POST /import: accepts PCPartPicker URL or CSV file,
// attaches to (or creates) the named machine if given, and imports parts in a
// single transaction.
func (h *Handler) Import(w http.ResponseWriter, r *http.Request) {
	// PCPartPicker fetches can outlast the server-wide WriteTimeout. Not every
	// ResponseWriter supports deadlines (e.g. httptest), so the error is ignored.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(importWriteTimeout))

	r.Body = http.MaxBytesReader(w, r.Body, maxImportBody)
	// The URL form posts urlencoded; ParseMultipartForm has already parsed
	// those fields by the time it reports ErrNotMultipart.
	err := r.ParseMultipartForm(maxImportBody) //nolint:gosec // G120: body bounded by MaxBytesReader
	if err != nil && !errors.Is(err, http.ErrNotMultipart) {
		h.renderImportError(w, "", "", "failed to parse form")
		return
	}

	machineName := strings.TrimSpace(r.FormValue("machine_name"))
	rawURL := strings.TrimSpace(r.FormValue("pcpartpicker_url"))
	file, _, fileErr := r.FormFile("file")

	matches, msg := validateImportInput(rawURL, file, fileErr)
	if msg != "" {
		h.renderImportError(w, machineName, rawURL, msg)
		return
	}

	// Fetch and parse before opening the transaction so no SQLite write lock
	// is held during network I/O.
	var rows []csvRow
	if rawURL != "" {
		rows, err = h.importFromURL(r.Context(), rawURL, matches)
		if err != nil {
			// The error can carry network and parser internals; log it, not show it.
			slog.WarnContext(r.Context(), "PCPartPicker import failed", "url", rawURL, "error", err)
			h.renderImportError(w, machineName, rawURL,
				"Couldn't import that list from PCPartPicker. Check that the list is public and try again.")
			return
		}
	} else {
		defer file.Close()
		rows, err = parseCSVToRows(csv.NewReader(file))
		if err != nil {
			h.renderImportError(w, machineName, "", fmt.Sprintf("Failed to parse CSV: %v", err))
			return
		}
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	q := h.queries.WithTx(tx)

	var machineID int64

	// Attach to the named machine, creating it if it doesn't exist.
	if machineName != "" {
		machineID, err = h.findOrCreateMachine(r.Context(), q, machineName)
		if err != nil {
			_ = tx.Rollback()
			h.serverError(w, r, err)
			return
		}
	}

	// Create parts from rows.
	for _, row := range rows {
		params := rowToCreateParams(row, sql.NullInt64{Int64: machineID, Valid: machineID != 0})
		if _, err := q.CreatePart(r.Context(), params); err != nil {
			_ = tx.Rollback()
			h.serverError(w, r, fmt.Errorf("create part %q: %w", row.name, err))
			return
		}
	}

	if err := tx.Commit(); err != nil {
		h.serverError(w, r, err)
		return
	}

	// Redirect to the machine page if one was named, else to the machines list.
	redirect := machinesPath
	if machineID != 0 {
		redirect = fmt.Sprintf("%s/%d", machinesPath, machineID)
	}

	redirectNativeOrHX(w, r, redirect)
}

// findOrCreateMachine returns the ID of the machine with the given name. A new
// machine gets the default slots; an existing one is left as is.
func (h *Handler) findOrCreateMachine(ctx context.Context, q *db.Queries, name string) (int64, error) {
	machine, err := q.GetMachineByName(ctx, name)
	if err == nil {
		return machine.ID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("look up machine %q: %w", name, err)
	}
	machine, err = q.CreateMachine(ctx, db.CreateMachineParams{Name: name})
	if err != nil {
		return 0, fmt.Errorf("create machine %q: %w", name, err)
	}
	h.seedDefaultSlots(ctx, q, machine.ID)
	return machine.ID, nil
}

// renderImportError re-renders the import page (400) with the error and the
// user's prior input so they can retry.
func (h *Handler) renderImportError(w http.ResponseWriter, machineName, pcppURL, msg string) {
	h.web.Render(w, http.StatusBadRequest, "import.html", importPage{
		Error:       msg,
		MachineName: machineName,
		PCPPURL:     pcppURL,
	})
}

// validateImportInput enforces exactly one of a PCPartPicker URL or a CSV
// file, and that the URL is a PCPartPicker list/build URL. It returns the
// URL regex matches for importFromURL and a user-facing message ("" if valid).
func validateImportInput(rawURL string, file multipart.File, fileErr error) ([]string, string) {
	hasURL := rawURL != ""
	hasFile := file != nil && fileErr == nil
	switch {
	case !hasURL && !hasFile:
		return nil, "Provide a PCPartPicker URL or upload a CSV file."
	case hasURL && hasFile:
		return nil, "Provide only one: a URL or a CSV file, not both."
	case hasURL:
		if matches := pcppListRe.FindStringSubmatch(rawURL); matches != nil {
			return matches, ""
		}
		return nil, "Not a valid PCPartPicker list URL."
	}
	return nil, ""
}

// importFromURL fetches parts from a PCPartPicker URL (CSV export first, then HTML fallback).
func (h *Handler) importFromURL(ctx context.Context, rawURL string, matches []string) ([]csvRow, error) {
	pathType := matches[1]
	listID := matches[2]

	// Try the CSV export first.
	exportURL := "https://" + pcppHost + "/" + pathType + "/" + listID + "/export"
	exportData, exportStatus, err := fetchPCPP(ctx, exportURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch PCPartPicker: %w", err)
	}

	if exportStatus == http.StatusOK {
		rows, err := parseCSVToRows(csv.NewReader(bytes.NewReader(exportData)))
		if err != nil {
			return nil, fmt.Errorf("failed to parse CSV: %w", err)
		}
		return rows, nil
	}

	// CSV export unavailable (e.g. build guides) — parse the page HTML.
	rows, err := fetchAndParseHTML(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse page: %w", err)
	}
	return rows, nil
}

// parsePrice strips currency symbols and commas, then parses a float.
// Returns 0 and false if the string is empty or unparseable.
func parsePrice(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	s = priceRe.ReplaceAllString(s, "")
	if s == "" || s == "." {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// csvRow holds the parsed fields from a PCPartPicker-style CSV row.
type csvRow struct {
	category string
	name     string
	price    float64
	hasPrice bool
	merchant string
}

// parseCSVToRows parses a PCPartPicker-style CSV reader into structured rows.
// Expected columns: Type, Item/Name, Price, Merchant.
func parseCSVToRows(r *csv.Reader) ([]csvRow, error) {
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse CSV: %w", err)
	}
	if len(records) < 2 {
		return nil, nil
	}

	// Map header indices (case-insensitive).
	header := records[0]
	idx := map[string]int{}
	for i, col := range header {
		idx[strings.ToLower(strings.TrimSpace(col))] = i
	}

	nameIdx, hasName := idx["item"]
	if !hasName {
		nameIdx, hasName = idx["name"]
	}
	if !hasName {
		return nil, errors.New("CSV missing Item/Name column")
	}

	typeIdx, hasType := idx["type"]
	priceIdx, hasPrice := idx["price"]
	merchantIdx, hasMerchant := idx["merchant"]

	var rows []csvRow
	for _, rec := range records[1:] {
		if nameIdx >= len(rec) {
			continue
		}
		name := strings.TrimSpace(rec[nameIdx])
		if name == "" {
			continue
		}

		row := csvRow{name: name}

		if hasType && typeIdx < len(rec) {
			row.category = strings.TrimSpace(rec[typeIdx])
		}
		if hasPrice && priceIdx < len(rec) {
			row.price, row.hasPrice = parsePrice(rec[priceIdx])
		}
		if hasMerchant && merchantIdx < len(rec) {
			row.merchant = strings.TrimSpace(rec[merchantIdx])
		}

		rows = append(rows, row)
	}
	return rows, nil
}

// parsePartlistHTML extracts parts from a PCPartPicker page's partlist table.
// ponytail: walks the DOM tree; O(n) on page size, fine for a single parts list.
func parsePartlistHTML(data []byte) ([]csvRow, error) {
	root, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parse HTML: %w", err)
	}

	// Find the partlist table.
	table := findPartlistTable(root)
	if table == nil {
		return nil, nil
	}

	// Collect all <tr> descendants (they may be nested under <tbody>).
	var trs []*html.Node
	collectTRs(table, &trs)

	return processRows(trs), nil
}

// processRows walks table rows, tracking categories and extracting parts.
func processRows(trs []*html.Node) []csvRow {
	var rows []csvRow
	currentCategory := ""

	for _, tr := range trs {
		// Check for category row.
		if td := childByClass(tr, "td__component"); td != nil {
			if h4 := childByTag(td, "h4"); h4 != nil {
				currentCategory = extractText(h4)
			}
			continue
		}

		// Check for product row (has td__name).
		row := extractProductRow(tr, currentCategory)
		if row != nil {
			rows = append(rows, *row)
		}
	}
	return rows
}

// extractProductRow extracts a product row from a <tr> if it has td__name.
func extractProductRow(tr *html.Node, category string) *csvRow {
	td := childByClass(tr, "td__name")
	if td == nil {
		return nil
	}

	var name, priceStr string
	if a := childByTag(td, "a"); a != nil {
		name = extractText(a)
	}
	if p := childByClass(td, "td__price"); p != nil {
		priceStr = extractText(p)
	}
	if name == "" {
		return nil
	}

	row := &csvRow{
		name:     name,
		category: category,
	}
	if p, ok := parsePrice(priceStr); ok {
		row.price = p
		row.hasPrice = true
	}
	return row
}

// collectTRs recursively collects all <tr> descendants of n.
func collectTRs(n *html.Node, out *[]*html.Node) {
	if n.Type == html.ElementNode && n.Data == "tr" {
		*out = append(*out, n)
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectTRs(c, out)
	}
}

// findPartlistTable finds the first <table class="partlist ..."> node.
func findPartlistTable(n *html.Node) *html.Node {
	if n.Type == html.ElementNode && n.Data == "table" {
		for _, attr := range n.Attr {
			if attr.Key == "class" && strings.Contains(attr.Val, "partlist") {
				return n
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findPartlistTable(c); found != nil {
			return found
		}
	}
	return nil
}

// childByTag returns the first child of n with the given tag name.
func childByTag(n *html.Node, tag string) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == tag {
			return c
		}
	}
	return nil
}

// childByClass returns the first child of n whose class attribute contains cls.
func childByClass(n *html.Node, cls string) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			for _, attr := range c.Attr {
				if attr.Key == "class" && strings.Contains(attr.Val, cls) {
					return c
				}
			}
		}
	}
	return nil
}

// extractText returns the concatenated text content of a node.
func extractText(n *html.Node) string {
	var buf strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			buf.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(buf.String())
}

// rowToCreateParams converts a csvRow to db.CreatePartParams.
func rowToCreateParams(r csvRow, machineID sql.NullInt64) db.CreatePartParams {
	p := db.CreatePartParams{
		Name:      r.name,
		MachineID: machineID,
	}
	if r.category != "" {
		p.Category = sql.NullString{String: r.category, Valid: true}
	}
	if r.hasPrice {
		p.PurchasePrice = sql.NullFloat64{Float64: r.price, Valid: true}
	}
	if r.merchant != "" {
		p.PurchasedFrom = sql.NullString{String: r.merchant, Valid: true}
	}
	return p
}

// pcppListRe matches pcpartpicker.com list and build URLs (/list/ and /b/).
// Group 1 = path type ("list" or "b"), group 2 = list/build ID.
var pcppListRe = regexp.MustCompile(`^https?://(?:www\.)?pcpartpicker\.com/(list|b)/([a-zA-Z0-9_-]+)`)

// isPCPPHost reports whether u is an http(s) URL on pcpartpicker.com.
func isPCPPHost(u *url.URL) bool {
	return (u.Scheme == "https" || u.Scheme == "http") &&
		(u.Host == pcppHost || u.Host == "www."+pcppHost)
}

// checkPCPPRedirect keeps redirects on pcpartpicker.com, so the host allowlist
// in fetchPCPP can't be bypassed by a redirect elsewhere.
func checkPCPPRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxPCPPRedirects {
		return fmt.Errorf("stopped after %d redirects", maxPCPPRedirects)
	}
	if !isPCPPHost(req.URL) {
		return fmt.Errorf("redirect to %s not allowed", req.URL.Redacted())
	}
	return nil
}

// fetchPCPP fetches a PCPartPicker URL and returns the body bytes and status
// code. The host is allowlisted (including across redirects) as an SSRF
// guard, and the body is capped at maxPCPPBody.
func fetchPCPP(ctx context.Context, rawURL string) ([]byte, int, error) {
	u, err := url.Parse(rawURL)
	if err != nil || !isPCPPHost(u) {
		return nil, 0, errors.New("URL must point to " + pcppHost)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil) //nolint:gosec // G704: host allowlisted
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Racket/1.0)")
	resp, err := pcppClient.Do(req) //nolint:gosec // G704: request built from allowlisted URL
	if err != nil {
		return nil, 0, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	// Read one byte past the cap to tell "exactly at the limit" from "too big".
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPCPPBody+1))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	if len(body) > maxPCPPBody {
		return nil, resp.StatusCode, fmt.Errorf("response larger than %d bytes", maxPCPPBody)
	}
	return body, resp.StatusCode, nil
}

// fetchAndParseHTML fetches a PCPartPicker page and parses its partlist table.
func fetchAndParseHTML(ctx context.Context, rawURL string) ([]csvRow, error) {
	pageData, pageStatus, err := fetchPCPP(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("fetch PCPartPicker page: %w", err)
	}
	if pageStatus != http.StatusOK {
		return nil, fmt.Errorf("PCPartPicker returned %d", pageStatus)
	}
	rows, err := parsePartlistHTML(pageData)
	if err != nil {
		return nil, fmt.Errorf("parse parts list: %w", err)
	}
	return rows, nil
}
