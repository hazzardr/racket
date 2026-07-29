package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/go-chi/chi/v5"
	"github.com/pressly/goose/v3"

	"racket/internal/db"
)

func newImportTestRouter(t *testing.T) (*chi.Mux, *sql.DB) {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(sqlDB, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	queries := db.New(sqlDB)
	h := New(sqlDB, queries, newTestWeb(t))

	r := chi.NewRouter()
	r.Post("/import", h.Import)
	return r, sqlDB
}

const (
	cpu = "CPU"
	gpu = "GPU"
	ram = "RAM"
)

func TestParsePrice(t *testing.T) {
	tests := []struct {
		input  string
		want   float64
		wantOk bool
	}{
		{"$199.99", 199.99, true},
		{"£150", 150, true},
		{"€1,299.00", 1299.00, true},
		{"123.45", 123.45, true},
		{"$1,000", 1000, true},
		{"", 0, false},
		{"  ", 0, false},
		{"free", 0, false},
		{"TBD", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, ok := parsePrice(tt.input)
			if ok != tt.wantOk {
				t.Errorf("parsePrice(%q) ok=%v, want %v", tt.input, ok, tt.wantOk)
			}
			if ok && got != tt.want {
				t.Errorf("parsePrice(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseCSVToRows(t *testing.T) {
	csvData := `Type,Item,Price,Merchant
CPU,Ryzen 7 5800X,$329.99,Amazon
Motherboard,B550 Tomahawk,$179.99,Newegg
RAM,16GB DDR4,$69.99,Amazon
`
	reader := csv.NewReader(strings.NewReader(csvData))
	rows, err := parseCSVToRows(reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	want := []struct {
		name     string
		category string
		price    float64
		merchant string
	}{
		{"Ryzen 7 5800X", cpu, 329.99, "Amazon"},
		{"B550 Tomahawk", "Motherboard", 179.99, "Newegg"},
		{"16GB DDR4", ram, 69.99, "Amazon"},
	}
	for i, w := range want {
		if rows[i].name != w.name {
			t.Errorf("row[%d].name = %q, want %q", i, rows[i].name, w.name)
		}
		if rows[i].category != w.category {
			t.Errorf("row[%d].category = %q, want %q", i, rows[i].category, w.category)
		}
		if rows[i].price != w.price {
			t.Errorf("row[%d].price = %v, want %v", i, rows[i].price, w.price)
		}
		if rows[i].merchant != w.merchant {
			t.Errorf("row[%d].merchant = %q, want %q", i, rows[i].merchant, w.merchant)
		}
	}
}

func TestParseCSVToRows_SkipsEmptyNames(t *testing.T) {
	csvData := `Type,Item,Price,Merchant
CPU,Ryzen 7 5800X,$329.99,Amazon
,,100,Amazon
GPU,RTX 3080,$599.99,Amazon
`
	reader := csv.NewReader(strings.NewReader(csvData))
	rows, err := parseCSVToRows(reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows (empty name skipped), got %d", len(rows))
	}
}

func TestParseCSVToRows_NameColumn(t *testing.T) {
	// Test with "Name" column instead of "Item".
	csvData := `Type,Name,Price,Merchant
CPU,Ryzen 7 5800X,$329.99,Amazon
`
	reader := csv.NewReader(strings.NewReader(csvData))
	rows, err := parseCSVToRows(reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].name != "Ryzen 7 5800X" {
		t.Errorf("name = %q, want %q", rows[0].name, "Ryzen 7 5800X")
	}
}

func TestRowToCreateParams(t *testing.T) {
	row := csvRow{
		name:     "Ryzen 7 5800X",
		category: cpu,
		price:    329.99,
		hasPrice: true,
		merchant: "Amazon",
	}
	params := rowToCreateParams(row, sql.NullInt64{})
	if params.Name != "Ryzen 7 5800X" {
		t.Errorf("Name = %q, want %q", params.Name, "Ryzen 7 5800X")
	}
	if !params.Category.Valid || params.Category.String != cpu {
		t.Errorf("Category = %+v, want Valid=true String=CPU", params.Category)
	}
	if !params.PurchasePrice.Valid || params.PurchasePrice.Float64 != 329.99 {
		t.Errorf("PurchasePrice = %+v, want Valid=true Float64=329.99", params.PurchasePrice)
	}
	if !params.PurchasedFrom.Valid || params.PurchasedFrom.String != "Amazon" {
		t.Errorf("PurchasedFrom = %+v, want Valid=true String=Amazon", params.PurchasedFrom)
	}
}

func TestImportCSV_Endpoint(t *testing.T) {
	r, sqlDB := newImportTestRouter(t)
	defer sqlDB.Close()

	csvData := `Type,Item,Price,Merchant
CPU,Ryzen 7 5800X,$329.99,Amazon
GPU,RTX 3080,$599.99,Newegg
`
	var buf bytes.Buffer
	mw := newMPWriter(&buf, "form-boundary")
	mw.writeField("file", "parts.csv", []byte(csvData))
	mw.close()

	req := httptest.NewRequest(http.MethodPost, "/import", &buf)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=form-boundary")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	// Redirect to /machines (no machine created).
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d body: %s", rr.Code, rr.Body.String())
	}
	location := rr.Header().Get("Location")
	if location != "/machines" {
		t.Errorf("Location = %q, want %q", location, "/machines")
	}

	// Verify parts were created.
	queries := db.New(sqlDB)
	parts, err := queries.ListParts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
}

func TestImportCSV_WithMachineName(t *testing.T) {
	r, sqlDB := newImportTestRouter(t)
	defer sqlDB.Close()

	csvData := `Type,Item,Price,Merchant
CPU,Ryzen 7 5800X,$329.99,Amazon
`
	var buf bytes.Buffer
	mw := newMPWriter(&buf, "form-boundary")
	mw.writeField("file", "parts.csv", []byte(csvData))
	mw.writeField("machine_name", "", []byte("Test Machine"))
	mw.close()

	req := httptest.NewRequest(http.MethodPost, "/import", &buf)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=form-boundary")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d body: %s", rr.Code, rr.Body.String())
	}
	location := rr.Header().Get("Location")
	if location != "/machines/1" {
		t.Errorf("Location = %q, want %q", location, "/machines/1")
	}

	// Verify machine was created.
	queries := db.New(sqlDB)
	machine, err := queries.GetMachine(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if machine.Name != "Test Machine" {
		t.Errorf("machine name = %q, want %q", machine.Name, "Test Machine")
	}

	// Verify part was associated with the machine.
	parts, err := queries.ListPartsByMachine(context.Background(), sql.NullInt64{Int64: 1, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
}

func TestImport_NoURLNoFile(t *testing.T) {
	r, sqlDB := newImportTestRouter(t)
	defer sqlDB.Close()

	req := httptest.NewRequest(http.MethodPost, "/import", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Provide a PCPartPicker URL or a CSV file") {
		t.Errorf("body missing error message: %s", rr.Body.String())
	}
}

func TestImport_InvalidURL(t *testing.T) {
	r, sqlDB := newImportTestRouter(t)
	defer sqlDB.Close()

	tests := []string{
		"not-a-url",
		"https://example.com/list/abc",
	}
	for _, urlVal := range tests {
		t.Run(urlVal, func(t *testing.T) {
			body := fmt.Sprintf("pcpartpicker_url=%s", urlVal)
			req := httptest.NewRequest(http.MethodPost, "/import", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)
			if rr.Code != http.StatusUnprocessableEntity {
				t.Errorf("expected 422 for url=%q, got %d", urlVal, rr.Code)
			}
		})
	}
}

func TestPCPPListRegex(t *testing.T) {
	tests := []struct {
		url       string
		wantID    string
		wantMatch bool
	}{
		{"https://pcpartpicker.com/list/abc123", "abc123", true},
		{"http://www.pcpartpicker.com/list/xyz-789", "xyz-789", true},
		{"https://pcpartpicker.com/list/my-build-2024", "my-build-2024", true},
		{"https://pcpartpicker.com/b/PtrD4D", "PtrD4D", true},
		{"http://www.pcpartpicker.com/b/abc_123", "abc_123", true},
		{"https://pcpartpicker.com/part/abc123", "", false},
		{"https://example.com/list/abc123", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			matches := pcppListRe.FindStringSubmatch(tt.url)
			if tt.wantMatch {
				if matches == nil {
					t.Errorf("expected match for %q", tt.url)
				} else if matches[2] != tt.wantID {
					t.Errorf("extracted ID %q, want %q", matches[2], tt.wantID)
				}
			} else {
				if matches != nil {
					t.Errorf("expected no match for %q, got %v", tt.url, matches)
				}
			}
		})
	}
}

// mpWriter builds a minimal multipart form body.
type mpWriter struct {
	w        *bytes.Buffer
	boundary string
	first    bool
}

func newMPWriter(w *bytes.Buffer, boundary string) *mpWriter {
	return &mpWriter{w: w, boundary: boundary, first: true}
}

func (m *mpWriter) writeField(name, filename string, data []byte) {
	if !m.first {
		m.w.WriteString("\r\n")
	}
	m.first = false
	if filename != "" {
		m.w.WriteString("--" + m.boundary + "\r\n")
		fmt.Fprintf(m.w, "Content-Disposition: form-data; name=%q; filename=%q\r\n", name, filename)
		m.w.WriteString("Content-Type: text/csv\r\n\r\n")
	} else {
		m.w.WriteString("--" + m.boundary + "\r\n")
		fmt.Fprintf(m.w, "Content-Disposition: form-data; name=%q\r\n\r\n", name)
	}
	m.w.Write(data)
}

func (m *mpWriter) close() {
	m.w.WriteString("\r\n--" + m.boundary + "--\r\n")
}

func TestParsePartlistHTML(t *testing.T) {
	htmlData := []byte(`<html><body>
<table class="partlist partlist--mini">
<tr><td class="td__component" colspan="2"><h4>CPU</h4></td></tr>
<tr>
  <td class="td__image"><img src="..."/></td>
  <td class="td__name"><a href="/part/abc">Ryzen 7 5800X</a><p class="td__price">$329.99</p></td>
</tr>
<tr><td class="td__component" colspan="2"><h4>GPU</h4></td></tr>
<tr>
  <td class="td__image"><img src="..."/></td>
  <td class="td__name"><a href="/part/def">RTX 3080</a><p class="td__price">$599.00</p></td>
</tr>
<tr>
  <td class="td__image"><img src="..."/></td>
  <td class="td__name"><a href="/part/ghi">16GB DDR4 RAM</a><p class="td__price">$69.99</p></td>
</tr>
</table>
</body></html>`)

	rows, err := parsePartlistHTML(htmlData)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	want := []struct {
		name     string
		category string
		price    float64
	}{
		{"Ryzen 7 5800X", cpu, 329.99},
		{"RTX 3080", gpu, 599.00},
		{"16GB DDR4 RAM", gpu, 69.99},
	}
	for i, w := range want {
		if rows[i].name != w.name {
			t.Errorf("row[%d].name = %q, want %q", i, rows[i].name, w.name)
		}
		if rows[i].category != w.category {
			t.Errorf("row[%d].category = %q, want %q", i, rows[i].category, w.category)
		}
		if !rows[i].hasPrice {
			t.Errorf("row[%d] missing price", i)
		}
		if rows[i].price != w.price {
			t.Errorf("row[%d].price = %v, want %v", i, rows[i].price, w.price)
		}
	}
}

func TestParsePartlistHTML_NoTable(t *testing.T) {
	rows, err := parsePartlistHTML([]byte("<html><body><p>No table here</p></body></html>"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}

func TestParsePartlistHTML_NoPrice(t *testing.T) {
	htmlData := []byte(`<html><body>
<table class="partlist partlist--mini">
<tr><td class="td__component" colspan="2"><h4>CPU</h4></td></tr>
<tr>
  <td class="td__image"><img src="..."/></td>
  <td class="td__name"><a href="/part/abc">Some CPU</a></td>
</tr>
</table>
</body></html>`)

	rows, err := parsePartlistHTML(htmlData)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].hasPrice {
		t.Error("expected no price")
	}
}

func TestParsePartlistHTML_EmptyNameSkipped(t *testing.T) {
	htmlData := []byte(`<html><body>
<table class="partlist partlist--mini">
<tr><td class="td__component" colspan="2"><h4>CPU</h4></td></tr>
<tr>
  <td class="td__image"><img src="..."/></td>
  <td class="td__name"><a href="/part/abc"></a><p class="td__price">$100</p></td>
</tr>
<tr>
  <td class="td__image"><img src="..."/></td>
  <td class="td__name"><a href="/part/def">Valid CPU</a><p class="td__price">$200</p></td>
</tr>
</table>
</body></html>`)

	rows, err := parsePartlistHTML(htmlData)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row (empty name skipped), got %d", len(rows))
	}
}

func TestImportURL_BuildGuideHTMLFallback(t *testing.T) {
	htmlPage := []byte(`<html><body>
<table class="partlist partlist--mini">
<tr><td class="td__component" colspan="2"><h4>CPU</h4></td></tr>
<tr>
  <td class="td__image"><img src="..."/></td>
  <td class="td__name"><a href="/part/abc">Test CPU</a><p class="td__price">$150.00</p></td>
</tr>
</table>
</body></html>`)

	rows, err := parsePartlistHTML(htmlPage)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].name != "Test CPU" {
		t.Errorf("name = %q, want %q", rows[0].name, "Test CPU")
	}
	if rows[0].category != cpu {
		t.Errorf("category = %q, want %q", rows[0].category, cpu)
	}
	if rows[0].price != 150.00 {
		t.Errorf("price = %v, want %v", rows[0].price, 150.00)
	}
}
