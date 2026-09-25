package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/go-chi/chi/v5"
	"github.com/pressly/goose/v3"

	"racket/internal/db"
	"racket/internal/web"
)

func newImportTestRouter(t *testing.T) (*chi.Mux, *sql.DB) {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
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

// newMultipartBody builds a multipart form body with the given fields.
func newMultipartBody(fields map[string]string, fileField, filename string, fileData []byte) (*bytes.Buffer, string) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		w.WriteField(k, v)
	}
	if fileData != nil {
		fw, _ := w.CreateFormFile(fileField, filename)
		fw.Write(fileData)
	}
	w.Close()
	return &buf, w.FormDataContentType()
}

func TestImportCSV_Endpoint(t *testing.T) {
	r, sqlDB := newImportTestRouter(t)
	defer sqlDB.Close()

	csvData := `Type,Item,Price,Merchant
CPU,Ryzen 7 5800X,$329.99,Amazon
GPU,RTX 3080,$599.99,Newegg
`
	body, ct := newMultipartBody(map[string]string{"machine_name": "Test Build"}, "file", "parts.csv", []byte(csvData))

	req := httptest.NewRequest(http.MethodPost, "/import", body)
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d body: %s", rr.Code, rr.Body.String())
	}
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, "/machines/") {
		t.Fatalf("expected redirect to /machines/{id}, got %q", loc)
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

func TestImportCSV_NoMachine(t *testing.T) {
	r, sqlDB := newImportTestRouter(t)
	defer sqlDB.Close()

	csvData := `Type,Item,Price,Merchant
CPU,Ryzen 7 5800X,$329.99,Amazon
`
	body, ct := newMultipartBody(nil, "file", "parts.csv", []byte(csvData))

	req := httptest.NewRequest(http.MethodPost, "/import", body)
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d body: %s", rr.Code, rr.Body.String())
	}
	loc := rr.Header().Get("Location")
	if loc != "/machines" {
		t.Fatalf("expected redirect to /machines, got %q", loc)
	}
}

func TestImportURL_InvalidURL(t *testing.T) {
	r, sqlDB := newImportTestRouter(t)
	defer sqlDB.Close()

	tests := []struct {
		urlVal string
	}{
		{"not-a-url"},
		{"https://example.com/list/abc"},
	}
	for _, tt := range tests {
		t.Run(tt.urlVal, func(t *testing.T) {
			body, ct := newMultipartBody(map[string]string{"pcpartpicker_url": tt.urlVal}, "", "", nil)

			req := httptest.NewRequest(http.MethodPost, "/import", body)
			req.Header.Set("Content-Type", ct)
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("expected 400 for url=%q, got %d", tt.urlVal, rr.Code)
			}
		})
	}
}

// The URL import form posts urlencoded (no file field), not multipart.
func TestImportURL_URLEncodedForm(t *testing.T) {
	r, sqlDB := newImportTestRouter(t)
	defer sqlDB.Close()

	form := url.Values{"pcpartpicker_url": {"https://example.com/list/abc"}}
	req := httptest.NewRequest(http.MethodPost, "/import", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Not a valid PCPartPicker list URL.") {
		t.Fatalf("expected URL validation error, got: %s", rr.Body.String())
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

func TestRenderTemplates(t *testing.T) {
	w, err := web.New()
	if err != nil {
		t.Fatal(err)
	}

	pages := []struct {
		name string
		data any
	}{
		{"error.html", struct{ Message string }{Message: "test error"}},
		{"import.html", importPage{}},
		{"landing.html", nil},
		{"machines.html", []machineRow{}},
		{"new_machine.html", newMachinePage{}},
		{"machine_detail.html", MachinePage{Slots: []slotCard{}, Cats: []string{}}},
	}

	for _, p := range pages {
		t.Run(p.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			w.Render(rr, 200, p.name, p.data)
			if rr.Code != http.StatusOK {
				t.Fatalf("template %s: expected 200, got %d", p.name, rr.Code)
			}
			body := rr.Body.String()
			if !strings.Contains(body, "<!doctype html>") || !strings.Contains(body, "</html>") {
				n := 100
				if len(body) < n {
					n = len(body)
				}
				t.Errorf("template %s rendered an incomplete page, got: %s", p.name, body[:n])
			}
		})
	}
}

// Naming an existing machine attaches the imported parts to it.
func TestImportCSV_ExistingMachine(t *testing.T) {
	r, sqlDB := newImportTestRouter(t)
	defer sqlDB.Close()

	csvData := []byte("Type,Item,Price,Merchant\nCPU,Ryzen 7 5800X,$329.99,Amazon\n")
	var locs []string
	for range 2 {
		body, ct := newMultipartBody(map[string]string{"machine_name": "Test Build"}, "file", "parts.csv", csvData)
		req := httptest.NewRequest(http.MethodPost, "/import", body)
		req.Header.Set("Content-Type", ct)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("expected 303, got %d body: %s", rr.Code, rr.Body.String())
		}
		locs = append(locs, rr.Header().Get("Location"))
	}
	if locs[0] != locs[1] {
		t.Fatalf("expected both imports to redirect to the same machine, got %q and %q", locs[0], locs[1])
	}

	queries := db.New(sqlDB)
	machines, err := queries.ListMachines(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(machines) != 1 {
		t.Fatalf("expected 1 machine, got %d", len(machines))
	}
	parts, err := queries.ListPartsByMachine(context.Background(), sql.NullInt64{Int64: machines[0].ID, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts on the machine, got %d", len(parts))
	}
	// Default slots are seeded once, not again on the second import.
	slots, err := queries.ListSlotsByMachine(context.Background(), machines[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != len(defaultSlots) {
		t.Fatalf("expected %d slots, got %d", len(defaultSlots), len(slots))
	}
}

// roundTripFunc lets tests stand in for PCPartPicker without network access.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakePCPP routes pcppClient through fn for the duration of the test.
func fakePCPP(t *testing.T, fn roundTripFunc) {
	t.Helper()
	orig := pcppClient.Transport
	pcppClient.Transport = fn
	t.Cleanup(func() { pcppClient.Transport = orig })
}

func textResponse(r *http.Request, status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}
}

func TestImportURL_CSVExport(t *testing.T) {
	var requested []string
	fakePCPP(t, func(r *http.Request) (*http.Response, error) {
		requested = append(requested, r.URL.String())
		return textResponse(r, http.StatusOK, "Type,Item,Price\nCPU,Ryzen 7 5800X,$329.99\n", nil), nil
	})
	r, sqlDB := newImportTestRouter(t)
	defer sqlDB.Close()

	form := url.Values{"pcpartpicker_url": {"https://pcpartpicker.com/list/abc123"}, "machine_name": {"From URL"}}
	req := httptest.NewRequest(http.MethodPost, "/import", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d body: %s", rr.Code, rr.Body.String())
	}
	if len(requested) != 1 || requested[0] != "https://pcpartpicker.com/list/abc123/export" {
		t.Fatalf("expected one request to the export URL, got %v", requested)
	}
	parts, err := db.New(sqlDB).ListParts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 || parts[0].Name != "Ryzen 7 5800X" {
		t.Fatalf("expected the exported part to be imported, got %+v", parts)
	}
}

func TestFetchPCPP_RejectsOffsiteRedirect(t *testing.T) {
	var offsite bool
	fakePCPP(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "pcpartpicker.com" {
			offsite = true
			return textResponse(r, http.StatusOK, "internal data", nil), nil
		}
		loc := http.Header{"Location": {"http://169.254.169.254/latest/meta-data"}}
		return textResponse(r, http.StatusFound, "", loc), nil
	})

	_, _, err := fetchPCPP(context.Background(), "https://pcpartpicker.com/list/abc123/export")
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected redirect to be rejected, got err=%v", err)
	}
	if offsite {
		t.Fatal("client followed the redirect off pcpartpicker.com")
	}
}

func TestFetchPCPP_FollowsOnsiteRedirect(t *testing.T) {
	fakePCPP(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "pcpartpicker.com" {
			loc := http.Header{"Location": {"https://www.pcpartpicker.com" + r.URL.Path}}
			return textResponse(r, http.StatusMovedPermanently, "", loc), nil
		}
		return textResponse(r, http.StatusOK, "ok", nil), nil
	})

	body, status, err := fetchPCPP(context.Background(), "https://pcpartpicker.com/list/abc123/export")
	if err != nil || status != http.StatusOK || string(body) != "ok" {
		t.Fatalf("expected redirect within pcpartpicker.com to be followed, got status=%d body=%q err=%v", status, body, err)
	}
}

func TestFetchPCPP_BodyLimit(t *testing.T) {
	size := maxPCPPBody
	fakePCPP(t, func(r *http.Request) (*http.Response, error) {
		return textResponse(r, http.StatusOK, strings.Repeat("x", size), nil), nil
	})

	if _, _, err := fetchPCPP(context.Background(), "https://pcpartpicker.com/list/a/export"); err != nil {
		t.Fatalf("body exactly at the limit should be accepted, got %v", err)
	}
	size = maxPCPPBody + 1
	if _, _, err := fetchPCPP(context.Background(), "https://pcpartpicker.com/list/a/export"); err == nil {
		t.Fatal("expected oversized body to be rejected")
	}
}
