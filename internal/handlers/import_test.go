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

	"racket/internal/db"
	"racket/internal/web"
)

// postImport posts a multipart import form as cookie's user.
func (app *testApp) postImport(
	cookie *http.Cookie, fields map[string]string, csvData []byte,
) *httptest.ResponseRecorder {
	app.t.Helper()
	body, ct := newMultipartBody(fields, "file", "parts.csv", csvData)
	req := httptest.NewRequest(http.MethodPost, "/import", body)
	req.Header.Set("Content-Type", ct)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	app.router.ServeHTTP(rr, req)
	return rr
}

// ownedParts lists a user's parts.
func (app *testApp) ownedParts(username string) []db.Part {
	app.t.Helper()
	parts, err := app.queries.ListPartsByOwner(app.t.Context(), app.user(username).ID)
	if err != nil {
		app.t.Fatal(err)
	}
	return parts
}

// slotContents maps each filled slot's category to its part names, in slot order.
func slotContents(slots []db.ListSlotsByMachineRow) map[string][]string {
	got := map[string][]string{}
	for _, s := range slots {
		if s.PartID.Valid {
			got[s.Category] = append(got[s.Category], s.PartName.String)
		}
	}
	return got
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
	slot := sql.NullInt64{Int64: 7, Valid: true}
	params := rowToCreateParams(row, 3, slot, "wanted")
	if params.Name != "Ryzen 7 5800X" || params.OwnerID != 3 || params.SlotID != slot || params.Status != "wanted" {
		t.Errorf("params = %+v, want name, owner 3, slot 7, wanted", params)
	}
	if !params.Category.Valid || params.Category.String != cpu {
		t.Errorf("Category = %+v, want Valid=true String=CPU", params.Category)
	}
	if !params.PriceCents.Valid || params.PriceCents.Int64 != 32999 {
		t.Errorf("PriceCents = %+v, want 32999", params.PriceCents)
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
	app := newTestApp(t)
	alice := app.login("alice")

	csvData := `Type,Item,Price,Merchant
CPU,Ryzen 7 5800X,$329.99,Amazon
Video Card,RTX 3080,$599.99,Newegg
`
	rr := app.postImport(alice, map[string]string{"machine_name": "Test Build"}, []byte(csvData))
	assertStatus(t, rr, http.StatusSeeOther)
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, "/machines/") {
		t.Fatalf("expected redirect to /machines/{id}, got %q", loc)
	}

	parts := app.ownedParts("alice")
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	for _, p := range parts {
		if p.Status != "owned" || !p.SlotID.Valid {
			t.Errorf("part %q: status %q, in slot %v; want owned, in a slot", p.Name, p.Status, p.SlotID.Valid)
		}
	}
}

// Without a machine name, parts go to the viewer's inventory.
func TestImportCSV_NoMachine(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")

	csvData := "Type,Item,Price,Merchant\nCPU,Ryzen 7 5800X,$329.99,Amazon\n"
	rr := app.postImport(alice, nil, []byte(csvData))
	assertStatus(t, rr, http.StatusSeeOther)
	if loc := rr.Header().Get("Location"); loc != "/machines" {
		t.Fatalf("expected redirect to /machines, got %q", loc)
	}
	parts := app.ownedParts("alice")
	if len(parts) != 1 || parts[0].SlotID.Valid {
		t.Fatalf("parts = %+v, want one part outside any machine", parts)
	}
}

func TestImportURL_InvalidURL(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")

	for _, urlVal := range []string{"not-a-url", "https://example.com/list/abc"} {
		t.Run(urlVal, func(t *testing.T) {
			rr := app.postImport(alice, map[string]string{"pcpartpicker_url": urlVal}, nil)
			assertStatus(t, rr, http.StatusBadRequest)
		})
	}
}

// The URL import form posts urlencoded (no file field), not multipart.
func TestImportURL_URLEncodedForm(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")

	rr := app.post(alice, "/import", url.Values{"pcpartpicker_url": {"https://example.com/list/abc"}})
	assertStatus(t, rr, http.StatusBadRequest)
	assertContains(t, rr, "Not a valid PCPartPicker list URL.")
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

// Every page renders with its real data type and a signed-in user.
func TestRenderTemplates(t *testing.T) {
	w, err := web.New()
	if err != nil {
		t.Fatal(err)
	}
	user := &db.User{ID: 1, Username: "alice"}

	pages := []struct {
		name string
		data any
	}{
		{"error.html", struct{ Message string }{Message: "test error"}},
		{"import.html", importPage{}},
		{"landing.html", nil},
		{"login.html", loginPage{Discord: true, DevLogin: true}},
		{"machines.html", machinesPage{}},
		{"new_machine.html", newMachinePage{Kinds: machineKinds, Statuses: machineStatuses}},
		{"machine_detail.html", MachinePage{IsOwner: true, Slots: []slotCard{{CanEdit: true}}, Cats: commonSlots}},
	}

	for _, p := range pages {
		t.Run(p.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			w.Render(rr, http.StatusOK, p.name, web.Page{User: user, Data: p.data})
			assertStatus(t, rr, http.StatusOK)
			assertContains(t, rr, "<!doctype html>", "</html>", `<span class="nav-user">alice</span>`)
		})
	}
}

// Naming one of your machines imports into it; a taken slot gets a sibling.
func TestImportCSV_ExistingMachine(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")

	csvData := []byte("Type,Item,Price,Merchant\nCPU,Ryzen 7 5800X,$329.99,Amazon\n")
	var locs []string
	for range 2 {
		rr := app.postImport(alice, map[string]string{"machine_name": "Test Build"}, csvData)
		assertStatus(t, rr, http.StatusSeeOther)
		locs = append(locs, rr.Header().Get("Location"))
	}
	if locs[0] != locs[1] {
		t.Fatalf("expected both imports to redirect to the same machine, got %q and %q", locs[0], locs[1])
	}

	machines, err := app.queries.ListMachines(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(machines) != 1 {
		t.Fatalf("expected 1 machine, got %d", len(machines))
	}
	slots := app.slots(machines[0].Machine.ID)
	// Default slots are seeded once; the second CPU needed a new CPU slot.
	if len(slots) != len(defaultSlots)+1 {
		t.Fatalf("expected %d slots, got %d", len(defaultSlots)+1, len(slots))
	}
	if got := slotContents(slots)["CPU"]; len(got) != 2 {
		t.Fatalf("CPU slots hold %v, want both CPUs", got)
	}
}

// Another user's machine with the same name isn't touched.
func TestImportCSV_MachineNamesArePerUser(t *testing.T) {
	app := newTestApp(t)
	alice, bob := app.login("alice"), app.login("bob")
	bobsID := app.createMachine(bob, "Test Build")

	csvData := []byte("Type,Item\nCPU,Ryzen 7 5800X\n")
	rr := app.postImport(alice, map[string]string{"machine_name": "Test Build"}, csvData)
	assertStatus(t, rr, http.StatusSeeOther)
	if loc := rr.Header().Get("Location"); loc == machinePath(bobsID, "") {
		t.Fatal("import went into Bob's machine")
	}
	if got := slotContents(app.slots(bobsID)); len(got) != 0 {
		t.Errorf("Bob's machine gained parts: %v", got)
	}
}

func TestImportPlacesPartsInSlots(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")

	csvData := `Type,Item,Price
CPU,Ryzen 7 5800X,$329.99
Memory,32GB DDR4 kit A,$60.00
Memory,32GB DDR4 kit B,$60.00
Memory,32GB DDR4 kit C,$60.00
Video Card,RTX 3080,$599.99
Power Supply,RM850x,$120.00
Case Fan,P12 5-pack,$30.00
,Mystery part,
`
	rr := app.postImport(alice, map[string]string{"machine_name": "Build", "wanted": "on"}, []byte(csvData))
	assertStatus(t, rr, http.StatusSeeOther)

	machines, err := app.queries.ListMachines(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := slotContents(app.slots(machines[0].Machine.ID))
	want := map[string][]string{
		"CPU":      {"Ryzen 7 5800X"},
		"RAM":      {"32GB DDR4 kit A", "32GB DDR4 kit B", "32GB DDR4 kit C"},
		"GPU":      {"RTX 3080"},
		"PSU":      {"RM850x"},
		"Case Fan": {"P12 5-pack"},
		"Other":    {"Mystery part"},
	}
	for category, names := range want {
		if strings.Join(got[category], "|") != strings.Join(names, "|") {
			t.Errorf("%s slots = %v, want %v", category, got[category], names)
		}
	}
	for _, p := range app.ownedParts("alice") {
		if p.Status != "wanted" {
			t.Errorf("part %q status = %q, want wanted", p.Name, p.Status)
		}
	}
	if machines[0].PartsTotalCents != 32999+3*6000+59999+12000+3000 {
		t.Errorf("machine total = %d cents", machines[0].PartsTotalCents)
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
	app := newTestApp(t)
	alice := app.login("alice")

	form := url.Values{"pcpartpicker_url": {"https://pcpartpicker.com/list/abc123"}, "machine_name": {"From URL"}}
	assertStatus(t, app.post(alice, "/import", form), http.StatusSeeOther)
	if len(requested) != 1 || requested[0] != "https://pcpartpicker.com/list/abc123/export" {
		t.Fatalf("expected one request to the export URL, got %v", requested)
	}
	parts := app.ownedParts("alice")
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
