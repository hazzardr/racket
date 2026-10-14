package handlers

import (
	"context"
	"database/sql"
	"html/template"
	"net/http"
	"slices"
	"strings"

	"racket/internal/db"
)

// The parts page is the viewer's side of lending: their spare parts, which
// of them friends may borrow and who wants them, and what's on loan either
// way. Every action re-renders the whole page body, like the machine board.

// partsPath is the parts page route, used in redirect targets.
const partsPath = "/parts"

// partsPage is the parts page data. The parts_board fragment renders from
// it too.
type partsPage struct {
	Spares   []inventoryPart
	Lent     []loanRow
	Borrowed []loanRow
	// Cats are the suggested categories for a new spare.
	Cats []string
	// Form is the add-a-spare form, filled in again after a failed add.
	Form spareForm
}

// inventoryPart is one of the viewer's spares.
type inventoryPart struct {
	db.Part
	State    string
	Category string
	Style    template.CSS
	// Wanters are friends' machines with a not-yet-bought part of the same
	// name; Friends counts the friends behind them.
	Wanters []wanter
	Friends int
}

// wanter is a friend's machine that wants a spare.
type wanter struct {
	UserID      int64
	UserName    string
	MachineID   int64
	MachineName string
}

// loanRow is a part on loan: the viewer's in a friend's machine, or a
// friend's in the viewer's. Friend is the other side ("" if they've left).
type loanRow struct {
	ID          int64
	Name        string
	Category    string
	Style       template.CSS
	MachineID   int64
	MachineName string
	Friend      string
	FriendID    int64
}

// spareForm is the add-a-spare form's values.
type spareForm struct {
	Error        string
	Name         string
	Category     string
	Manufacturer string
	Model        string
	Price        string
	Wanted       bool
	Lend         bool
}

func newInventoryPart(p db.Part) inventoryPart {
	category := slotCategory(p.Category.String)
	part := inventoryPart{Part: p, State: stateOwned, Category: category, Style: typeStyle(category)}
	if p.Status == partWanted {
		part.State = stateWanted
	}
	return part
}

// newLoanRow styles a loan by its part's category, and drops the friend's
// name if they've left.
func newLoanRow(row loanRow, category sql.NullString, friendLeft bool) loanRow {
	row.Category = slotCategory(category.String)
	row.Style = typeStyle(row.Category)
	if friendLeft {
		row.Friend = ""
	}
	return row
}

// partsBoard loads the parts page data for the viewer.
func (h *Handler) partsBoard(ctx context.Context, viewerID int64) (partsPage, error) {
	page := partsPage{Cats: slices.Concat(commonSlots, []string{otherSlot})}

	spares, err := h.queries.ListSparesByOwner(ctx, viewerID)
	if err != nil {
		return partsPage{}, err
	}
	demand, err := h.queries.ListSpareDemand(ctx, viewerID)
	if err != nil {
		return partsPage{}, err
	}
	wanters := map[int64][]wanter{}
	friends := map[int64]map[int64]bool{}
	for _, d := range demand {
		w := wanter{UserID: d.UserID, UserName: d.Username, MachineID: d.MachineID, MachineName: d.MachineName}
		wanters[d.PartID] = append(wanters[d.PartID], w)
		if friends[d.PartID] == nil {
			friends[d.PartID] = map[int64]bool{}
		}
		friends[d.PartID][d.UserID] = true
	}
	for _, s := range spares {
		p := newInventoryPart(s)
		p.Wanters, p.Friends = wanters[s.ID], len(friends[s.ID])
		page.Spares = append(page.Spares, p)
	}

	lent, err := h.queries.ListLentParts(ctx, viewerID)
	if err != nil {
		return partsPage{}, err
	}
	for _, l := range lent {
		page.Lent = append(page.Lent, newLoanRow(loanRow{
			ID: l.ID, Name: l.Name, MachineID: l.MachineID, MachineName: l.MachineName,
			Friend: l.BorrowerName, FriendID: l.BorrowerID,
		}, l.Category, l.BorrowerLeftAt.Valid))
	}
	borrowed, err := h.queries.ListBorrowedParts(ctx, viewerID)
	if err != nil {
		return partsPage{}, err
	}
	for _, b := range borrowed {
		page.Borrowed = append(page.Borrowed, newLoanRow(loanRow{
			ID: b.ID, Name: b.Name, MachineID: b.MachineID, MachineName: b.MachineName,
			Friend: b.LenderName, FriendID: b.LenderID,
		}, b.Category, b.LenderLeftAt.Valid))
	}
	return page, nil
}

// PartsPage renders GET /parts.
func (h *Handler) PartsPage(w http.ResponseWriter, r *http.Request) {
	page, err := h.partsBoard(r.Context(), viewer(r).ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, "parts.html", page)
}

// finishPartsAction ends a parts page action: a 303 back to the page for
// native forms, or the re-rendered page body for htmx. A failed add
// re-renders with the form filled in (422 for native forms, 200 for htmx
// so it swaps in).
func (h *Handler) finishPartsAction(w http.ResponseWriter, r *http.Request, form spareForm) {
	if form.Error == "" && redirectNative(w, r, partsPath) {
		return
	}
	page, err := h.partsBoard(r.Context(), viewer(r).ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	page.Form = form
	if isHX(r) {
		h.web.Fragment(w, "parts_board", page)
		return
	}
	h.render(w, r, http.StatusUnprocessableEntity, "parts.html", page)
}

// CreateSparePart handles POST /parts: adds a part to the viewer's spares.
func (h *Handler) CreateSparePart(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, http.StatusBadRequest, "failed to parse form")
		return
	}
	form := spareForm{
		Name:         strings.TrimSpace(r.FormValue("name")),
		Category:     strings.TrimSpace(r.FormValue("category")),
		Manufacturer: strings.TrimSpace(r.FormValue("manufacturer")),
		Model:        strings.TrimSpace(r.FormValue("model")),
		Price:        strings.TrimSpace(r.FormValue("price")),
		Wanted:       r.FormValue("wanted") != "",
		Lend:         r.FormValue("lend") != "",
	}
	var price sql.NullInt64
	if form.Price != "" {
		p, ok := parsePrice(form.Price)
		if !ok {
			form.Error = "Price should be a number, like 129.99."
		}
		price = sql.NullInt64{Int64: toCents(p), Valid: ok}
	}
	if form.Name == "" {
		form.Error = "Part name is required."
	}
	if form.Error != "" {
		h.finishPartsAction(w, r, form)
		return
	}
	status := partOwned
	if form.Wanted {
		status = partWanted
	}

	_, err := h.queries.CreatePart(r.Context(), db.CreatePartParams{
		OwnerID:      viewer(r).ID,
		Status:       status,
		Name:         form.Name,
		Category:     optionalString(form.Category),
		Manufacturer: optionalString(form.Manufacturer),
		Model:        optionalString(form.Model),
		PriceCents:   price,
		// Nobody can borrow a part that isn't bought yet.
		Lendable: form.Lend && !form.Wanted,
	})
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.finishPartsAction(w, r, spareForm{})
}

// partAction parses a parts page action's part ID, or renders a 400.
func (h *Handler) partAction(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, http.StatusBadRequest, "failed to parse form")
		return 0, false
	}
	id, err := parseID(r, "id")
	if err != nil {
		h.renderError(w, r, http.StatusBadRequest, "invalid part id")
		return 0, false
	}
	return id, true
}

// finishPartWrite ends a write that should have changed one row: a 403 if
// it changed none, the re-rendered page otherwise.
func (h *Handler) finishPartWrite(w http.ResponseWriter, r *http.Request, n int64, err error) {
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if n == 0 {
		h.forbidden(w, r)
		return
	}
	h.finishPartsAction(w, r, spareForm{})
}

// SetPartLendable handles POST /parts/{id}/lend (form field "lendable":
// "1" to lend, anything else to stop). A part already on loan stays where
// it is.
func (h *Handler) SetPartLendable(w http.ResponseWriter, r *http.Request) {
	id, ok := h.partAction(w, r)
	if !ok {
		return
	}
	n, err := h.queries.SetPartLendable(r.Context(), db.SetPartLendableParams{
		Lendable: r.FormValue("lendable") == "1",
		ID:       id,
		UserID:   viewer(r).ID,
	})
	h.finishPartWrite(w, r, n, err)
}

// ReturnPart handles POST /parts/{id}/return: the lender takes a part back,
// or the borrower gives it back. Either way it goes to its owner's spares.
func (h *Handler) ReturnPart(w http.ResponseWriter, r *http.Request) {
	id, ok := h.partAction(w, r)
	if !ok {
		return
	}
	n, err := h.queries.UnassignPart(r.Context(), db.UnassignPartParams{PartID: id, UserID: viewer(r).ID})
	h.finishPartWrite(w, r, n, err)
}

// DeleteSparePart handles POST /parts/{id}/delete. Only a spare can be
// deleted here; a part in a machine is removed from its slot.
func (h *Handler) DeleteSparePart(w http.ResponseWriter, r *http.Request) {
	id, ok := h.partAction(w, r)
	if !ok {
		return
	}
	n, err := h.queries.DeleteSparePart(r.Context(), db.DeleteSparePartParams{ID: id, OwnerID: viewer(r).ID})
	h.finishPartWrite(w, r, n, err)
}
