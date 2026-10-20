package handlers

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"racket/internal/db"
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
		want   int64
		wantOk bool
	}{
		{"$199.99", 19999, true},
		{"£150", 15000, true},
		{"€1,299.00", 129900, true},
		{"123.45", 12345, true},
		{"$1,000", 100000, true},
		{"$1,234,567", 0, false}, // over the cap
		{"1000000", 100000000, true},
		{"1000000.01", 0, false},
		{"5000000000000000000", 0, false},
		{"1" + strings.Repeat("0", 400), 0, false},
		// A comma with two digits after it is a decimal comma.
		{"12,50", 1250, true},
		{"1.299,00", 129900, true},
		{"€1.299,00", 129900, true},
		{"12,50 EUR", 1250, true},
		{"1,299", 129900, true},
		{"0.5", 50, true},
		{"19.999", 2000, true},
		{"USD 20", 2000, true},
		{"US$20", 2000, true},
		{"20 usd", 2000, true},
		{"$20 USD", 2000, true},
		{"", 0, false},
		{"  ", 0, false},
		{"free", 0, false},
		{"TBD", 0, false},
		{"-5", 0, false},
		{"$-5", 0, false},
		{"1e3", 0, false},
		{"12abc", 0, false},
		{"XYZ 12", 0, false},
		{"$20 EUR $", 0, false},
		{"12,5", 0, false},
		{"1,2,3", 0, false},
		{"1.2.3", 0, false},
		{"12.", 0, false},
		{".", 0, false},
		{"1,299,00", 0, false},
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

// Ragged rows read as far as they go, and a byte order mark doesn't hide
// the first column.
func TestParseCSVToRows_RaggedRowsAndBOM(t *testing.T) {
	csvData := "\ufeffType,Item,Price,Merchant\nCPU,Ryzen 7 5800X\nGPU\nRAM,16GB DDR4,$69.99,Amazon,extra\n"
	rows, err := parseCSVToRows(csv.NewReader(strings.NewReader(csvData)))
	if err != nil {
		t.Fatal(err)
	}
	want := []csvRow{
		{category: cpu, name: "Ryzen 7 5800X"},
		{category: ram, name: "16GB DDR4", cents: 6999, hasPrice: true, merchant: "Amazon"},
	}
	if len(rows) != len(want) || rows[0] != want[0] || rows[1] != want[1] {
		t.Errorf("rows = %+v, want %+v", rows, want)
	}
}

// A price that isn't one, or is over the cap, is left out.
func TestParseCSVToRows_BadPrices(t *testing.T) {
	csvData := "Type,Item,Price\nCPU,a,5000000000000000000\nCPU,b,-5\nCPU,c,\"12,5\"\n"
	rows, err := parseCSVToRows(csv.NewReader(strings.NewReader(csvData)))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.hasPrice {
			t.Errorf("row %q has price %d, want none", r.name, r.cents)
		}
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
		cents    int64
		merchant string
	}{
		{"Ryzen 7 5800X", cpu, 32999, "Amazon"},
		{"B550 Tomahawk", "Motherboard", 17999, "Newegg"},
		{"16GB DDR4", ram, 6999, "Amazon"},
	}
	for i, w := range want {
		if rows[i].name != w.name {
			t.Errorf("row[%d].name = %q, want %q", i, rows[i].name, w.name)
		}
		if rows[i].category != w.category {
			t.Errorf("row[%d].category = %q, want %q", i, rows[i].category, w.category)
		}
		if rows[i].cents != w.cents {
			t.Errorf("row[%d].cents = %v, want %v", i, rows[i].cents, w.cents)
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
		cents:    32999,
		hasPrice: true,
		merchant: "Amazon",
	}
	slot := sql.NullInt64{Int64: 7, Valid: true}
	params := rowToCreateParams(row, 3, slot, "wanted", priceSource{})
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
	if params.SourceUrl.Valid || params.PriceCheckedAt.Valid {
		t.Errorf("a CSV row has no price source, got %+v / %+v", params.SourceUrl, params.PriceCheckedAt)
	}

	checked := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	source := priceSource{url: "https://pcpartpicker.com/list/abc", checkedAt: checked}
	params = rowToCreateParams(row, 3, slot, "wanted", source)
	if params.SourceUrl.String != "https://pcpartpicker.com/list/abc" || !params.PriceCheckedAt.Time.Equal(checked) {
		t.Errorf("source = %+v checked = %+v, want the list URL and its fetch time", params.SourceUrl, params.PriceCheckedAt)
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
	if loc := rr.Header().Get("Location"); loc != partsPath {
		t.Fatalf("expected redirect to %s, got %q", partsPath, loc)
	}
	parts := app.ownedParts("alice")
	if len(parts) != 1 || parts[0].SlotID.Valid {
		t.Fatalf("parts = %+v, want one part outside any machine", parts)
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
	for category, names := range got {
		if _, ok := want[category]; !ok {
			t.Errorf("unexpected %s slots holding %v", category, names)
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

func TestImportCSV_EmptyCreatesNothing(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")
	rr := app.postImport(alice, map[string]string{"machine_name": "test"}, []byte("Type,Item,Price\n"))
	assertStatus(t, rr, http.StatusBadRequest)
	assertContains(t, rr, "That CSV has no parts in it.")
}

func TestImportLimits(t *testing.T) {
	app := newTestApp(t)
	alice := app.login("alice")

	tests := []struct {
		name   string
		fields map[string]string
		csv    string
		want   string
	}{
		{
			name: "too many rows", csv: "Type,Item\n" + strings.Repeat("CPU,Ryzen\n", maxImportRows+1),
			want: "more than 500 parts",
		},
		{
			name: "long machine name", fields: map[string]string{"machine_name": strings.Repeat("x", maxNameLen+1)},
			csv: "Type,Item\nCPU,Ryzen\n", want: "Machine name can be at most 100 characters.",
		},
		{
			name: "long part name", csv: "Type,Item\nCPU," + strings.Repeat("x", maxPartNameLen+1) + "\n",
			want: "Part names can be at most 300 characters.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := app.postImport(alice, tt.fields, []byte(tt.csv))
			assertStatus(t, rr, http.StatusBadRequest)
			assertContains(t, rr, tt.want)
			if parts := app.ownedParts("alice"); len(parts) != 0 {
				t.Errorf("a rejected import created %d parts", len(parts))
			}
		})
	}

	// Exactly the cap is fine.
	rr := app.postImport(alice, nil, []byte("Type,Item\n"+strings.Repeat("CPU,Ryzen\n", maxImportRows)))
	assertStatus(t, rr, http.StatusSeeOther)
	if parts := app.ownedParts("alice"); len(parts) != maxImportRows {
		t.Errorf("imported %d parts, want %d", len(parts), maxImportRows)
	}
}

func TestImportPage(t *testing.T) {
	app := newTestApp(t)
	rr := app.get(app.login("alice"), "/import")
	assertStatus(t, rr, http.StatusOK)
	assertContains(t, rr, `name="pcpartpicker_text"`, `name="machine_name"`, `type="file"`)
}
