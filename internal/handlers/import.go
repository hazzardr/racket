package handlers

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"racket/internal/db"
)

// priceRe strips everything but digits and the decimal point from a price.
var priceRe = regexp.MustCompile(`[^\d.]`)

// maxImportBody bounds the import request body (form fields + CSV upload).
const maxImportBody = 10 << 20

// importPage is the import form state (also used for failure re-renders).
type importPage struct {
	Error       string
	MachineName string
	PCPPText    string
	Wanted      bool
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
	h.render(w, r, http.StatusOK, "import.html", importPage{})
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
	if f, _, err := r.FormFile("file"); err == nil {
		file = f
		defer f.Close()
	}
	status := partOwned
	if r.FormValue("wanted") != "" {
		status = partWanted
	}

	rows, source, msg := importRows(text, file)
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

	// Redirect to the machine page if one was named, else to the machines list.
	redirect := machinesPath
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
	h.seedDefaultSlots(ctx, q, machine.ID)
	return machine.ID, nil
}

// importParts creates the rows as the owner's parts. With a machine, each
// part goes into the first empty slot of its category, or a new slot if
// none is free.
func importParts(
	ctx context.Context, q *db.Queries, ownerID, machineID int64, status string, source priceSource, rows []csvRow,
) error {
	// Free slot IDs by lowercased category, in slot order.
	free := map[string][]int64{}
	if machineID != 0 {
		slots, err := q.ListSlotsByMachine(ctx, machineID)
		if err != nil {
			return fmt.Errorf("list slots: %w", err)
		}
		for _, s := range slots {
			if !s.PartID.Valid {
				key := strings.ToLower(s.Category)
				free[key] = append(free[key], s.ID)
			}
		}
	}

	for _, row := range rows {
		var slotID sql.NullInt64
		if machineID != 0 {
			category := slotCategory(row.category)
			key := strings.ToLower(category)
			if ids := free[key]; len(ids) > 0 {
				slotID = sql.NullInt64{Int64: ids[0], Valid: true}
				free[key] = ids[1:]
			} else {
				slot, err := q.CreateSlot(ctx, db.CreateSlotParams{MachineID: machineID, Category: category})
				if err != nil {
					return fmt.Errorf("create %s slot: %w", category, err)
				}
				slotID = sql.NullInt64{Int64: slot.ID, Valid: true}
			}
		}
		if _, err := q.CreatePart(ctx, rowToCreateParams(row, ownerID, slotID, status, source)); err != nil {
			return fmt.Errorf("create part %q: %w", row.name, err)
		}
	}
	return nil
}

// slotCategory maps a PCPartPicker part type to a slot category.
func slotCategory(partType string) string {
	if partType == "" {
		return otherSlot
	}
	if c, ok := pcppSlotCategories[strings.ToLower(partType)]; ok {
		return c
	}
	return partType
}

// importRows reads the parts to import from a pasted list or a CSV file
// (nil if none was uploaded). It returns a user-facing message instead when
// there's nothing to import.
func importRows(text string, file multipart.File) ([]csvRow, priceSource, string) {
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
	h.render(w, r, http.StatusBadRequest, "import.html", importPage{
		Error:       msg,
		MachineName: machineName,
		PCPPText:    pcppText,
		Wanted:      r.FormValue("wanted") != "",
	})
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
		p.PriceCents = sql.NullInt64{Int64: toCents(r.price), Valid: true}
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
