package handlers

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime/multipart"
	"net/http"
	"slices"
	"strings"
	"time"

	"racket/internal/db"
)

// maxImportBody bounds the import request body (form fields + CSV upload).
const maxImportBody = 10 << 20

// maxImportRows bounds the parts one import creates, all in one transaction.
const maxImportRows = 500

// importPage is the import form state (also used for failure re-renders).
type importPage struct {
	Error       string
	MachineName string
	PCPPText    string
	Wanted      bool
	// Renames are the PCPartPicker types that land in a differently named
	// slot, shown so the slot names aren't a surprise.
	Renames []slotRename
}

// slotRename is one pcppSlotCategories entry, for the import page.
type slotRename struct {
	From  string
	To    string
	Style template.CSS
}

func newImportPage() importPage {
	var page importPage
	for from, to := range pcppSlotCategories {
		page.Renames = append(page.Renames, slotRename{From: from, To: to, Style: typeStyle(to)})
	}
	slices.SortFunc(page.Renames, func(a, b slotRename) int { return strings.Compare(a.From, b.From) })
	return page
}

// pcppSlotCategories maps PCPartPicker part types to the slot names new
// machines start with. Other types keep their own name.
//
//nolint:goconst // slot names, also listed in defaultSlots
var pcppSlotCategories = map[string]string{
	"memory":       "RAM",
	"video card":   "GPU",
	"power supply": "PSU",
}

// otherSlot is the slot for parts that have no type.
const otherSlot = "Other"

// ImportPage renders GET /import.
func (h *Handler) ImportPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, "import.html", newImportPage())
}

// Import handles POST /import: accepts a pasted PCPartPicker list (its
// Markup > Text) or a CSV file and imports the parts as the viewer's, in a
// single transaction. With a machine name, the parts go into that machine
// (created if needed), one per slot. Without one, they go to the viewer's
// inventory.
func (h *Handler) Import(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBody)
	// The paste form posts urlencoded; ParseMultipartForm has already parsed
	// those fields by the time it reports ErrNotMultipart.
	err := r.ParseMultipartForm(maxImportBody) //nolint:gosec // G120: body bounded by MaxBytesReader
	if err != nil && !errors.Is(err, http.ErrNotMultipart) {
		h.renderImportError(w, r, "", "", "failed to parse form")
		return
	}

	machineName := strings.TrimSpace(r.FormValue("machine_name"))
	text := strings.TrimSpace(r.FormValue("pcpartpicker_text"))
	var file multipart.File
	if f, _, fileErr := r.FormFile("file"); fileErr == nil {
		file = f
		defer f.Close()
	}
	status := partOwned
	if r.FormValue("wanted") != "" {
		status = partWanted
	}

	rows, source, msg := importRows(text, file)
	if msg == "" {
		msg = tooLong(textField{"Machine name", machineName, maxNameLen})
	}
	if msg != "" {
		h.renderImportError(w, r, machineName, text, msg)
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	q := h.queries.WithTx(tx)

	ownerID := viewer(r).ID
	var machineID int64

	// Attach to the named machine, creating it if it doesn't exist.
	if machineName != "" {
		machineID, err = h.findOrCreateMachine(r.Context(), q, ownerID, machineName)
		if err != nil {
			_ = tx.Rollback()
			h.serverError(w, r, err)
			return
		}
	}

	if err := importParts(r.Context(), q, ownerID, machineID, status, source, rows); err != nil {
		_ = tx.Rollback()
		h.serverError(w, r, err)
		return
	}

	if err := tx.Commit(); err != nil {
		h.serverError(w, r, err)
		return
	}

	// Redirect to the machine page if one was named, else to the spares.
	redirect := partsPath
	if machineID != 0 {
		redirect = fmt.Sprintf("%s/%d", machinesPath, machineID)
	}

	redirectNativeOrHX(w, r, redirect)
}

// findOrCreateMachine returns the ID of the owner's machine with the given
// name. A new machine gets the default slots; an existing one is left as is.
func (h *Handler) findOrCreateMachine(ctx context.Context, q *db.Queries, ownerID int64, name string) (int64, error) {
	machine, err := q.GetMachineByOwnerAndName(ctx, db.GetMachineByOwnerAndNameParams{OwnerID: ownerID, Name: name})
	if err == nil {
		return machine.ID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("look up machine %q: %w", name, err)
	}
	machine, err = q.CreateMachine(ctx, db.CreateMachineParams{
		OwnerID: ownerID,
		Name:    name,
		Kind:    machineKinds[0],
		Status:  machineActive,
	})
	if err != nil {
		return 0, fmt.Errorf("create machine %q: %w", name, err)
	}
	if err := seedDefaultSlots(ctx, q, ownerID, machine.ID); err != nil {
		return 0, err
	}
	return machine.ID, nil
}

// importParts creates the rows as the owner's parts. With a machine, each
// part goes into the first empty slot of its category, or a new slot if
// none is free.
func importParts(
	ctx context.Context, q *db.Queries, ownerID, machineID int64, status string, source priceSource, rows []csvRow,
) error {
	var slots *slotPicker
	if machineID != 0 {
		var err error
		if slots, err = newSlotPicker(ctx, q, ownerID, machineID); err != nil {
			return err
		}
	}

	for _, row := range rows {
		var slotID sql.NullInt64
		if slots != nil {
			var err error
			if slotID, err = slots.next(ctx, slotCategory(row.category)); err != nil {
				return err
			}
		}
		if _, err := q.CreatePart(ctx, rowToCreateParams(row, ownerID, slotID, status, source)); err != nil {
			return fmt.Errorf("create part %q: %w", row.name, err)
		}
	}
	return nil
}

// slotPicker hands out a machine's empty slots by category during an
// import, creating a new slot when none of the category is left.
type slotPicker struct {
	q                  *db.Queries
	ownerID, machineID int64
	// Free slot IDs by matchKey of their category, in slot order.
	free map[string][]int64
}

func newSlotPicker(ctx context.Context, q *db.Queries, ownerID, machineID int64) (*slotPicker, error) {
	slots, err := q.ListSlotsByMachine(ctx, machineID)
	if err != nil {
		return nil, fmt.Errorf("list slots: %w", err)
	}
	p := &slotPicker{q: q, ownerID: ownerID, machineID: machineID, free: map[string][]int64{}}
	for _, s := range slots {
		if !s.PartID.Valid {
			key := matchKey(s.Category)
			p.free[key] = append(p.free[key], s.ID)
		}
	}
	return p, nil
}

// next returns the first free slot of the category, or a new one.
func (p *slotPicker) next(ctx context.Context, category string) (sql.NullInt64, error) {
	key := matchKey(category)
	if ids := p.free[key]; len(ids) > 0 {
		p.free[key] = ids[1:]
		return sql.NullInt64{Int64: ids[0], Valid: true}, nil
	}
	slot, err := p.q.CreateSlot(ctx, db.CreateSlotParams{
		MachineID: p.machineID, Category: category, UserID: p.ownerID,
	})
	if err != nil {
		return sql.NullInt64{}, fmt.Errorf("create %s slot: %w", category, err)
	}
	return sql.NullInt64{Int64: slot.ID, Valid: true}, nil
}

// slotCategory maps a PCPartPicker part type to a slot category.
func slotCategory(partType string) string {
	if partType == "" {
		return otherSlot
	}
	if c, ok := pcppSlotCategories[matchKey(partType)]; ok {
		return c
	}
	return partType
}

// importRows reads the parts to import from a pasted list or a CSV file
// (nil if none was uploaded). It returns a user-facing message instead when
// there's nothing to import, or more than it takes.
func importRows(text string, file multipart.File) ([]csvRow, priceSource, string) {
	rows, source, msg := readImportRows(text, file)
	if msg != "" {
		return nil, priceSource{}, msg
	}
	if len(rows) > maxImportRows {
		return nil, priceSource{}, fmt.Sprintf("That's more than %d parts. Import at most %d at a time.",
			maxImportRows, maxImportRows)
	}
	for _, row := range rows {
		if msg := tooLong(
			textField{"Part names", row.name, maxPartNameLen},
			textField{"Part types", row.category, maxCategoryLen},
			textField{"Merchants", row.merchant, maxPartFieldLen},
		); msg != "" {
			return nil, priceSource{}, msg
		}
	}
	return rows, source, ""
}

// readImportRows is importRows without the limits.
func readImportRows(text string, file multipart.File) ([]csvRow, priceSource, string) {
	switch {
	case text == "" && file == nil:
		return nil, priceSource{}, "Paste a PCPartPicker list or upload a CSV file."
	case text != "" && file != nil:
		return nil, priceSource{}, "Use one: a pasted list or a CSV file, not both."
	case text != "":
		list := parsePCPPText(text)
		if len(list.rows) == 0 {
			return nil, priceSource{}, noPartsInPaste
		}
		var source priceSource
		if list.url != "" {
			source = priceSource{url: list.url, checkedAt: time.Now().UTC()}
		}
		return list.rows, source, ""
	}
	rows, err := parseCSVToRows(csv.NewReader(file))
	if err != nil {
		return nil, priceSource{}, fmt.Sprintf("Failed to parse CSV: %v", err)
	}
	if len(rows) == 0 {
		return nil, priceSource{}, "That CSV has no parts in it."
	}
	return rows, priceSource{}, ""
}

// noPartsInPaste is the error for a paste with no part lines in it.
const noPartsInPaste = "Racket couldn't find any parts in that text. " +
	"On PCPartPicker, open the list's Markup, choose the Text tab, and paste all of it."

// renderImportError re-renders the import page (400) with the error and the
// user's prior input so they can retry.
func (h *Handler) renderImportError(w http.ResponseWriter, r *http.Request, machineName, pcppText, msg string) {
	page := newImportPage()
	page.Error = msg
	page.MachineName = machineName
	page.PCPPText = pcppText
	page.Wanted = r.FormValue("wanted") != ""
	h.render(w, r, http.StatusBadRequest, "import.html", page)
}

// csvRow holds the parsed fields from a PCPartPicker-style CSV row.
type csvRow struct {
	category string
	name     string
	cents    int64
	hasPrice bool
	merchant string
}

// parseCSVToRows parses a PCPartPicker-style CSV reader into structured rows.
// Expected columns: Type, Item/Name, Price, Merchant. Rows may be short or
// long; a missing cell is empty. It stops reading after maxImportRows+1
// parts, enough for importRows to say there are too many. A price that
// isn't one is left out.
func parseCSVToRows(r *csv.Reader) ([]csvRow, error) {
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if errors.Is(err, io.EOF) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("parse CSV: %w", err)
	}

	cols, err := newCSVColumns(header)
	if err != nil {
		return nil, err
	}

	var rows []csvRow
	for len(rows) <= maxImportRows {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse CSV: %w", err)
		}
		if row := cols.row(rec); row.name != "" {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// csvColumns are the indexes of a CSV's columns, -1 for one it doesn't
// have.
type csvColumns struct {
	name, category, price, merchant int
}

// newCSVColumns finds the columns in a CSV's header, ignoring case. A
// spreadsheet app's byte order mark doesn't hide the first one.
func newCSVColumns(header []string) (csvColumns, error) {
	idx := map[string]int{}
	for i, col := range header {
		if i == 0 {
			col = strings.TrimPrefix(col, "\ufeff")
		}
		idx[strings.ToLower(strings.TrimSpace(col))] = i
	}
	find := func(names ...string) int {
		for _, name := range names {
			if i, ok := idx[name]; ok {
				return i
			}
		}
		return -1
	}
	cols := csvColumns{
		name: find("item", "name"), category: find("type"), price: find("price"), merchant: find("merchant"),
	}
	if cols.name < 0 {
		return csvColumns{}, errors.New("CSV missing Item/Name column")
	}
	return cols, nil
}

// row reads a record, which may be short. A row with no name is the zero
// csvRow.
func (c csvColumns) row(rec []string) csvRow {
	cell := func(i int) string {
		if i < 0 || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}
	row := csvRow{name: cell(c.name)}
	if row.name == "" {
		return csvRow{}
	}
	row.category, row.merchant = cell(c.category), cell(c.merchant)
	if price := cell(c.price); price != "" {
		row.cents, row.hasPrice = parsePrice(price)
	}
	return row
}

// priceSource is where an import's prices came from: a PCPartPicker list
// and when it was pasted. It's the zero value for a CSV upload, whose
// prices can't be refreshed.
type priceSource struct {
	url       string
	checkedAt time.Time
}

// rowToCreateParams converts a csvRow to db.CreatePartParams.
func rowToCreateParams(
	r csvRow, ownerID int64, slotID sql.NullInt64, status string, source priceSource,
) db.CreatePartParams {
	p := db.CreatePartParams{
		OwnerID: ownerID,
		SlotID:  slotID,
		Status:  status,
		Name:    r.name,
	}
	if r.category != "" {
		p.Category = sql.NullString{String: r.category, Valid: true}
	}
	if r.hasPrice {
		p.PriceCents = sql.NullInt64{Int64: r.cents, Valid: true}
	}
	if r.merchant != "" {
		p.PurchasedFrom = sql.NullString{String: r.merchant, Valid: true}
	}
	if source.url != "" {
		p.SourceUrl = sql.NullString{String: source.url, Valid: true}
		if r.hasPrice {
			p.PriceCheckedAt = sql.NullTime{Time: source.checkedAt, Valid: true}
		}
	}
	return p
}
