package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"racket/internal/db"
)

// ImportResponse is returned by all import endpoints.
type ImportResponse struct {
	Imported int            `json:"imported"`
	Parts    []PartResponse `json:"parts"`
}

var (
	priceRe = regexp.MustCompile(`[^\d.]`)

	// pcppClient is used to fetch PCPartPicker export CSVs.
	pcppClient = &http.Client{Timeout: 30 * time.Second}
)

// importPage is the import form state (also used for failure re-renders).
type importPage struct {
	Error string
}

// ImportPage renders GET /import.
func (h *Handler) ImportPage(w http.ResponseWriter, _ *http.Request) {
	h.web.Render(w, http.StatusOK, "import.html", importPage{})
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

// parseMachineID parses an optional machine_id query parameter.
// Returns a valid NullInt64 if provided and the machine exists, zero value otherwise.
func (h *Handler) parseMachineID(r *http.Request) (sql.NullInt64, error) {
	idStr := r.URL.Query().Get("machine_id")
	if idStr == "" {
		return sql.NullInt64{}, nil
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return sql.NullInt64{}, fmt.Errorf("invalid machine_id: %s", idStr)
	}
	_, err = h.queries.GetMachine(r.Context(), id)
	if err != nil {
		return sql.NullInt64{}, fmt.Errorf("machine %d not found", id)
	}
	return sql.NullInt64{Int64: id, Valid: true}, nil
}

// ImportCSV handles POST /api/parts/import-csv.
// Expects multipart form with a file field.
func (h *Handler) ImportCSV(w http.ResponseWriter, r *http.Request) {
	//nolint:gosec // bounded to 10MB
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse form")
		return
	}

	machineID, err := h.parseMachineID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer file.Close()

	rows, err := parseCSVToRows(csv.NewReader(file))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	parts, err := createPartsFromRows(r.Context(), h.queries, rows, machineID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, ImportResponse{
		Imported: len(parts),
		Parts:    parts,
	})
}

// ImportJSON handles POST /api/parts/import-json.
// Accepts multipart form with a JSON file, or raw JSON body.
func (h *Handler) ImportJSON(w http.ResponseWriter, r *http.Request) {
	machineID, err := h.parseMachineID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	items, err := readJSONImportItems(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	parts, err := createPartsFromJSONItems(r.Context(), h.queries, items, machineID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, ImportResponse{
		Imported: len(parts),
		Parts:    parts,
	})
}

// readJSONImportItems reads import items from a multipart file or raw JSON body.
func readJSONImportItems(r *http.Request) ([]jsonPartItem, error) {
	var items []jsonPartItem

	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		//nolint:gosec // bounded to 10MB
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			return nil, errors.New("failed to parse form")
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			return nil, errors.New("missing file field")
		}
		defer file.Close()

		if err := json.NewDecoder(file).Decode(&items); err != nil {
			return nil, fmt.Errorf("invalid JSON in file: %w", err)
		}
		return items, nil
	}

	if err := decodeJSON(r, &items); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return items, nil
}

// jsonPartItem represents one item in the JSON import array.
type jsonPartItem struct {
	Name          string   `json:"name"`
	Category      string   `json:"category"`
	Manufacturer  string   `json:"manufacturer"`
	Model         string   `json:"model"`
	PurchasePrice *float64 `json:"purchase_price"`
	PurchasedFrom string   `json:"purchased_from"`
	GuideURL      string   `json:"guide_url"`
}

func itemToCreateParams(item jsonPartItem, machineID sql.NullInt64) db.CreatePartParams {
	p := db.CreatePartParams{
		Name:      strings.TrimSpace(item.Name),
		MachineID: machineID,
	}
	if item.Category != "" {
		p.Category = sql.NullString{String: item.Category, Valid: true}
	}
	if item.Manufacturer != "" {
		p.Manufacturer = sql.NullString{String: item.Manufacturer, Valid: true}
	}
	if item.Model != "" {
		p.Model = sql.NullString{String: item.Model, Valid: true}
	}
	if item.PurchasePrice != nil {
		p.PurchasePrice = sql.NullFloat64{Float64: *item.PurchasePrice, Valid: true}
	}
	if item.PurchasedFrom != "" {
		p.PurchasedFrom = sql.NullString{String: item.PurchasedFrom, Valid: true}
	}
	if item.GuideURL != "" {
		p.GuideUrl = sql.NullString{String: item.GuideURL, Valid: true}
	}
	return p
}

func createPartsFromRows(ctx context.Context, q *db.Queries, rows []csvRow,
	machineID sql.NullInt64,
) ([]PartResponse, error) {
	var parts []PartResponse
	for _, row := range rows {
		part, err := q.CreatePart(ctx, rowToCreateParams(row, machineID))
		if err != nil {
			return nil, fmt.Errorf("create part %q: %w", row.name, err)
		}
		parts = append(parts, newPartResponse(part))
	}
	return parts, nil
}

func createPartsFromJSONItems(ctx context.Context, q *db.Queries, items []jsonPartItem,
	machineID sql.NullInt64,
) ([]PartResponse, error) {
	var parts []PartResponse
	for _, item := range items {
		if strings.TrimSpace(item.Name) == "" {
			continue
		}
		part, err := q.CreatePart(ctx, itemToCreateParams(item, machineID))
		if err != nil {
			return nil, fmt.Errorf("create part %q: %w", item.Name, err)
		}
		parts = append(parts, newPartResponse(part))
	}
	return parts, nil
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

// ImportURL handles POST /api/parts/import-url.
// Expects JSON body: { "url": "https://pcpartpicker.com/list/XXXXXX" }.
func (h *Handler) ImportURL(w http.ResponseWriter, r *http.Request) {
	machineID, err := h.parseMachineID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var body struct {
		URL string `json:"url"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	url := strings.TrimSpace(body.URL)
	if url == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}

	matches := pcppListRe.FindStringSubmatch(url)
	if matches == nil {
		writeError(w, http.StatusBadRequest, "not a valid PCPartPicker list URL")
		return
	}
	pathType := matches[1]
	listID := matches[2]

	// Try the CSV export first.
	exportURL := "https://pcpartpicker.com/" + pathType + "/" + listID + "/export"
	exportData, exportStatus, err := fetchPCPP(r.Context(), exportURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to fetch PCPartPicker: "+err.Error())
		return
	}

	var rows []csvRow
	if exportStatus == http.StatusOK {
		rows, err = parseCSVToRows(csv.NewReader(bytes.NewReader(exportData)))
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to parse CSV: "+err.Error())
			return
		}
	} else {
		// CSV export unavailable (e.g. build guides) — parse the page HTML.
		rows, err = fetchAndParseHTML(r.Context(), url)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	parts, err := createPartsFromRows(r.Context(), h.queries, rows, machineID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, ImportResponse{
		Imported: len(parts),
		Parts:    parts,
	})
}
