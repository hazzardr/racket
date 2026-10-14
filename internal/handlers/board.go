package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"racket/internal/db"
)

// The machine page is a "board": the machine drawn as a chassis of slot
// bays, each wired to a card for the part in it (board.js draws the wires),
// and a tray of spare parts the owner can drag into empty slots. Every slot
// action re-renders the whole board, since one change moves the totals, the
// bays, and the tray together.

// Slot states, as the board shows them. They map from part_source in
// ListSlotsByMachine (see slotState).
const (
	stateOwned    = "owned"
	stateWanted   = "wanted"
	stateBorrowed = "borrowed"
	stateEmpty    = "empty"
)

// Values of part_source that the board treats specially.
const (
	sourceBorrowed     = "borrowed"
	sourceFormerMember = "former_member"
)

// categoryStyle is how the board draws a slot category: a short code on its
// bay and a color for its wire.
type categoryStyle struct {
	code  string
	color string
}

// categoryStyles covers the default slots and PCPartPicker's part types,
// keyed by categoryKey.
//
//nolint:goconst // bay codes; some match slot names by coincidence
var categoryStyles = map[string]categoryStyle{
	"cpu":          {"CPU", "#7aa7ff"},
	"cpu cooler":   {"COOL", "#22d3ee"},
	"motherboard":  {"MB", "#a78bfa"},
	"ram":          {"RAM", "#f472b6"},
	"memory":       {"RAM", "#f472b6"},
	"storage":      {"STOR", "#34d399"},
	"gpu":          {"GPU", "#fb7185"},
	"video card":   {"GPU", "#fb7185"},
	"case":         {"CASE", "#cbd5e1"},
	"psu":          {"PSU", "#fbbf24"},
	"power supply": {"PSU", "#fbbf24"},
	"case fan":     {"FAN", "#2dd4bf"},
}

// otherColors are for categories categoryStyles doesn't know. A category
// always gets the same one.
var otherColors = []string{"#94a3b8", "#c084fc", "#67e8f9", "#fde68a", "#86efac", "#fda4af"}

func categoryKey(category string) string {
	return strings.ToLower(strings.TrimSpace(category))
}

func styleOf(category string) categoryStyle {
	key := categoryKey(category)
	if s, ok := categoryStyles[key]; ok {
		return s
	}
	code := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToUpper(r)
		}
		return -1
	}, category)
	if code == "" {
		code = "?"
	}
	if runes := []rune(code); len(runes) > 4 {
		code = string(runes[:4])
	}
	n := 0
	for _, r := range key {
		n = (n*31 + int(r)) % len(otherColors)
	}
	return categoryStyle{code: code, color: otherColors[n]}
}

// typeStyle is the inline style that gives an element its category color.
// The colors come from the tables above, never from user input.
func typeStyle(category string) template.CSS {
	return template.CSS("--type:" + styleOf(category).color) //nolint:gosec // G203: color from a fixed table
}

func slotState(partSource string) string {
	switch partSource {
	case "own":
		return stateOwned
	case "unpurchased":
		return stateWanted
	case sourceBorrowed, sourceFormerMember:
		return stateBorrowed
	}
	return stateEmpty
}

// slotCard is one slot on the board, as the viewer sees it: its card, its
// bay in the chassis, and its details panel.
type slotCard struct {
	db.ListSlotsByMachineRow
	State string
	Code  string
	Key   string
	Style template.CSS
	// CanEdit: the viewer owns the machine, so may add and remove slots and
	// parts.
	CanEdit bool
	// CanMarkBought: the part is the viewer's and not bought yet.
	CanMarkBought bool
	// TakeOutLabel names the machine owner's button that takes the part out
	// of the slot without deleting it ("" for other viewers).
	TakeOutLabel string
	// CanDelete: the part is the machine owner's own, so they may delete it.
	CanDelete bool
	// CanTakeBack: the viewer lent the part and may take it back.
	CanTakeBack bool
	// PriceDelta is how much the last price refresh moved a not-yet-bought
	// part's price (0 if it didn't); PriceDown says which way.
	PriceDelta int64
	PriceDown  bool
	// Fits are the viewer's spares of the slot's category, offered in an
	// empty slot's panel.
	Fits []sparePart
	// Error is set when a slot action failed; the board opens this slot's
	// panel to show it.
	Error string
}

func newSlotCard(row db.ListSlotsByMachineRow, viewerID, machineOwnerID int64) slotCard {
	style := styleOf(row.Category)
	card := slotCard{
		ListSlotsByMachineRow: row,
		State:                 slotState(row.PartSource),
		Code:                  style.code,
		Key:                   categoryKey(row.Category),
		Style:                 typeStyle(row.Category),
		CanEdit:               viewerID == machineOwnerID,
	}
	if !row.PartID.Valid {
		return card
	}
	ownsPart := row.PartOwnerID.Int64 == viewerID
	if row.PartStatus.String == partWanted {
		card.CanMarkBought = card.CanEdit && ownsPart
		if row.PartPreviousPriceCents.Valid && row.PartPriceCents.Valid {
			delta := row.PartPriceCents.Int64 - row.PartPreviousPriceCents.Int64
			card.PriceDown = delta < 0
			card.PriceDelta = max(delta, -delta)
		}
	}
	switch {
	case card.CanEdit && ownsPart:
		card.TakeOutLabel = "Take out"
		card.CanDelete = true
	case card.CanEdit && row.PartSource == sourceBorrowed:
		card.TakeOutLabel = "Give back to " + row.PartOwnerName.String
	case card.CanEdit:
		card.TakeOutLabel = "Take out"
	case ownsPart:
		card.CanTakeBack = true
	}
	return card
}

// sparePart is a part in the tray: in no slot, and placeable in the
// viewer's machines.
type sparePart struct {
	db.ListSparePartsRow
	Mine  bool
	State string
	// Category is the slot category the part fits, Key its categoryKey.
	Category string
	Key      string
	Style    template.CSS
}

func newSparePart(row db.ListSparePartsRow, viewerID int64) sparePart {
	category := slotCategory(row.Category.String)
	p := sparePart{
		ListSparePartsRow: row,
		Mine:              row.OwnerID == viewerID,
		State:             stateOwned,
		Category:          category,
		Key:               categoryKey(category),
		Style:             typeStyle(category),
	}
	switch {
	case row.Status == partWanted:
		p.State = stateWanted
	case !p.Mine:
		p.State = stateBorrowed
	}
	return p
}

// legendEntry is a category with its color: a swatch under the board, or
// an add-slot button.
type legendEntry struct {
	Name  string
	Style template.CSS
}

// MachinePage is the machine page data. The board fragment renders from it
// too.
type MachinePage struct {
	Machine   db.Machine
	OwnerName string
	IsOwner   bool
	Slots     []slotCard
	// Left and Right split Slots into the board's two columns.
	Left, Right []slotCard
	// Spares are the parts the viewer could put in a slot (owner only).
	// HasMine and HasTheirs say whether any are the viewer's own or friends'.
	Spares    []sparePart
	HasMine   bool
	HasTheirs bool
	// Legend lists the board's categories; Cats are the add-slot buttons.
	Legend []legendEntry
	Cats   []legendEntry
	// Filled counts the slots with a part. TotalCents sums the priced
	// parts; ToBuyCents is the part of it not bought yet. Counts tallies
	// slots by state, for the meter.
	Filled     int
	TotalCents int64
	ToBuyCents int64
	Counts     map[string]int
	// Open is a slot whose panel the board opens after rendering (one with
	// an error to show). Seated is a slot a part just went into, so the
	// board draws its wire in.
	Open   int64
	Seated int64
	// PriceLists are the PCPartPicker lists the viewer's not-yet-bought
	// parts came from, when the viewer owns the machine. Pasting one again
	// updates those parts' prices. PricesCheckedAt is the latest paste
	// (zero if no part had a price).
	PriceLists      []string
	PricesCheckedAt time.Time
}

// machineBoard loads a machine's page data for the viewer.
func (h *Handler) machineBoard(ctx context.Context, machine db.GetMachineRow, viewerID int64) (MachinePage, error) {
	rows, err := h.queries.ListSlotsByMachine(ctx, machine.Machine.ID)
	if err != nil {
		return MachinePage{}, err
	}
	page := MachinePage{
		Machine:   machine.Machine,
		OwnerName: machine.OwnerName,
		IsOwner:   machine.Machine.OwnerID == viewerID,
		Slots:     make([]slotCard, len(rows)),
		Cats:      make([]legendEntry, len(commonSlots)),
		Counts:    map[string]int{},
	}
	if page.IsOwner {
		spares, err := h.queries.ListSpareParts(ctx, viewerID)
		if err != nil {
			return MachinePage{}, err
		}
		for _, s := range spares {
			p := newSparePart(s, viewerID)
			page.HasMine = page.HasMine || p.Mine
			page.HasTheirs = page.HasTheirs || !p.Mine
			page.Spares = append(page.Spares, p)
		}
	}
	for i, c := range commonSlots {
		page.Cats[i] = legendEntry{Name: c, Style: typeStyle(c)}
	}
	for i, row := range rows {
		page.Slots[i] = newSlotCard(row, viewerID, machine.Machine.OwnerID)
		page.tally(row, viewerID)
	}
	page.fitSpares()
	half := (len(page.Slots) + 1) / 2
	page.Left, page.Right = page.Slots[:half], page.Slots[half:]
	return page, nil
}

// tally adds a slot to the page's totals and price lists.
func (page *MachinePage) tally(row db.ListSlotsByMachineRow, viewerID int64) {
	page.Counts[slotState(row.PartSource)]++
	sameCategory := func(e legendEntry) bool { return categoryKey(e.Name) == categoryKey(row.Category) }
	if !slices.ContainsFunc(page.Legend, sameCategory) {
		page.Legend = append(page.Legend, legendEntry{Name: row.Category, Style: typeStyle(row.Category)})
	}
	if !row.PartID.Valid {
		return
	}
	page.Filled++
	page.TotalCents += row.PartPriceCents.Int64
	if row.PartStatus.String != partWanted {
		return
	}
	page.ToBuyCents += row.PartPriceCents.Int64
	if page.IsOwner && row.PartOwnerID.Int64 == viewerID && row.PartSourceUrl.Valid {
		if !slices.Contains(page.PriceLists, row.PartSourceUrl.String) {
			page.PriceLists = append(page.PriceLists, row.PartSourceUrl.String)
		}
		if row.PartPriceCheckedAt.Valid && row.PartPriceCheckedAt.Time.After(page.PricesCheckedAt) {
			page.PricesCheckedAt = row.PartPriceCheckedAt.Time
		}
	}
}

// fitSpares offers each empty slot the spares of its category.
func (page *MachinePage) fitSpares() {
	for i := range page.Slots {
		s := &page.Slots[i]
		if s.PartID.Valid || !s.CanEdit {
			continue
		}
		for _, p := range page.Spares {
			if p.Key == s.Key {
				s.Fits = append(s.Fits, p)
			}
		}
	}
}

// boardNote is what a slot action tells the board it re-renders: an error
// to show in the slot's panel, or that a part just went into the slot.
type boardNote struct {
	err    string
	seated bool
}

// finishSlotAction ends a slot action: a 303 back to the machine for native
// forms, or the re-rendered board for htmx. An error re-renders with 200 so
// htmx swaps it in.
func (h *Handler) finishSlotAction(w http.ResponseWriter, r *http.Request, slot db.GetSlotRow, note boardNote) {
	if note.err == "" && redirectNative(w, r, fmt.Sprintf("%s/%d", machinesPath, slot.MachineID)) {
		return
	}
	machine, err := h.queries.GetMachine(r.Context(), slot.MachineID)
	if errors.Is(err, sql.ErrNoRows) {
		// A lender took a part back from a machine whose owner has left.
		redirectNativeOrHX(w, r, machinesPath)
		return
	}
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	page, err := h.machineBoard(r.Context(), machine, viewer(r).ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	for i := range page.Slots {
		if page.Slots[i].ID == slot.ID && note.err != "" {
			page.Slots[i].Error = note.err
			page.Open = slot.ID
		}
	}
	if note.seated {
		page.Seated = slot.ID
	}
	h.web.Fragment(w, "machine_board", page)
}
