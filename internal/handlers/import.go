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
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"racket/internal/db"
)

var (
	priceRe = regexp.MustCompile(`[^\d.]`)

	// pcppClient is used to fetch PCPartPicker export CSVs.
	pcppClient = &http.Client{Timeout: 30 * time.Second}
)

// importPage is the import form state (also used for failure re-renders).
type importPage struct {
	Error       string
	MachineName string
	URL         string
}

// ImportPage renders GET /import.
func (h *Handler) ImportPage(w http.ResponseWriter, _ *http.Request) {
	h.web.Render(w, http.StatusOK, "import.html", importPage{})
}

// Import handles POST /import.
// Accepts multipart/form-data or application/x-www-form-urlencoded with fields:
//   - machine_name (optional) — create a new machine for the imported parts
//   - pcpartpicker_url (optional) — PCPartPicker list/build URL
//   - file (optional) — CSV file upload
//
// Exactly one of pcpartpicker_url or file is required.
// All operations run in a single transaction.
func (h *Handler) Import(w http.ResponseWriter, r *http.Request) {
	// Limit body to 10MB.
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		// Fall back to urlencoded.
		if err := r.ParseForm(); err != nil {
			h.renderError(w, r, http.StatusBadRequest, "failed to parse form")
			return
		}
	}

	url := strings.TrimSpace(r.FormValue("pcpartpicker_url"))
	machineName := strings.TrimSpace(r.FormValue("machine_name"))
	file, _, fileErr := r.FormFile("file")

	if url == "" && fileErr != nil {
		h.web.Render(w, http.StatusUnprocessableEntity, "import.html", importPage{
			Error: "Provide a PCPartPicker URL or a CSV file.",
		})
		return
	}
	if fileErr == nil {
		defer file.Close()
	}

	base := importPage{MachineName: machineName, URL: url}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()

	txQ := h.queries.WithTx(tx)
	var machineID int64

	if machineName != "" {
		machine, err := txQ.CreateMachine(ctx, db.CreateMachineParams{Name: machineName})
		if err != nil {
			h.web.Render(w, http.StatusUnprocessableEntity, "import.html", importPage{Error: err.Error(), MachineName: base.MachineName, URL: base.URL})
			return
		}
		machineID = machine.ID
		// Seed default slots.
		for _, cat := range defaultSlots {
			if _, err := txQ.CreateSlot(ctx, db.CreateSlotParams{MachineID: machine.ID, Category: cat}); err != nil {
				slog.WarnContext(ctx, "failed to seed slot", "machine", machine.ID, "category", cat, "error", err)
			}
		}
	}

	var rows []csvRow
	if url != "" {
		matches := pcppListRe.FindStringSubmatch(url)
		if matches == nil {
			h.web.Render(w, http.StatusUnprocessableEntity, "import.html", importPage{Error: "Not a valid PCPartPicker list URL.", MachineName: base.MachineName, URL: base.URL})
			return
		}
		pathType, listID := matches[1], matches[2]

		// Try CSV export first.
		exportURL := "https://pcpartpicker.com/" + pathType + "/" + listID + "/export"
		exportData, exportStatus, err := fetchPCPP(ctx, exportURL)
		if err != nil {
			h.web.Render(w, http.StatusUnprocessableEntity, "import.html", importPage{Error: "Failed to fetch PCPartPicker: " + err.Error(), MachineName: base.MachineName, URL: base.URL})
			return
		}
		if exportStatus == http.StatusOK {
			rows, err = parseCSVToRows(csv.NewReader(bytes.NewReader(exportData)))
		} else {
			rows, err = fetchAndParseHTML(ctx, url)
		}
		if err != nil {
			h.web.Render(w, http.StatusUnprocessableEntity, "import.html", importPage{Error: err.Error(), MachineName: base.MachineName, URL: base.URL})
			return
		}
	} else {
		var err error
		rows, err = parseCSVToRows(csv.NewReader(file))
		if err != nil {
			h.web.Render(w, http.StatusUnprocessableEntity, "import.html", importPage{Error: err.Error(), MachineName: base.MachineName, URL: base.URL})
			return
		}
	}

	// Create parts from rows.
	for _, row := range rows {
		params := rowToCreateParams(row, sql.NullInt64{Int64: machineID, Valid: machineID != 0})
		if _, err := txQ.CreatePart(ctx, params); err != nil {
			h.web.Render(w, http.StatusUnprocessableEntity, "import.html", importPage{Error: fmt.Sprintf("create part %q: %v", row.name, err), MachineName: base.MachineName, URL: base.URL})
			return
		}
	}

	if err := tx.Commit(); err != nil {
		h.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}

	redirectURL := fmt.Sprintf("/machines/%d", machineID)
	if machineID == 0 {
		redirectURL = "/machines"
	}
	if r.Header.Get("Hx-Request") == "" {
		http.Redirect(w, r, redirectURL, http.StatusSeeOther)
	} else {
		w.Header().Set("Hx-Redirect", redirectURL)
	}
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

// fetchPCPP fetches a URL and returns the response body bytes and status code.
func fetchPCPP(ctx context.Context, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Racket/1.0)")
	resp, err := pcppClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, resp.Body); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	return buf.Bytes(), resp.StatusCode, nil
}

// fetchAndParseHTML fetches a PCPartPicker page and parses its partlist table.
func fetchAndParseHTML(ctx context.Context, url string) ([]csvRow, error) {
	pageData, pageStatus, err := fetchPCPP(ctx, url)
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
