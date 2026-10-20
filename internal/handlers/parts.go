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

// partsTemplate is the parts page's template.
const partsTemplate = "parts.html"

// partsPage is the parts page data. The parts_board fragment renders from
// it too.
type partsPage struct {
	// View is the tab showing: viewMine, or viewFriends with Friends.
	View    string
	Friends friendsView

	Spares   []inventoryPart
	Lent     []loanRow
	Borrowed []loanRow
	// Lending counts the spares friends may borrow.
	Lending int
	// Cats are the suggested categories for a new spare.
	Cats []string
	// Form and Import are the add-by-hand and PCPartPicker forms, filled
	// in again after a failed add.
	Form   spareForm
	Import pcppForm
	// Error is set when an action on a part failed, like giving back a
	// part its owner already took back.
	Error string
}

// pcppForm is the paste-a-PCPartPicker-list form's values.
type pcppForm struct {
	Error  string
	Text   string
	Wanted bool
	Lend   bool
}

// partsNote is what a parts page action tells the page it re-renders: the
// forms as they were sent, when adding failed, or why an action on a part
// failed.
type partsNote struct {
	form   spareForm
	pasted pcppForm
	err    string
}

func (n partsNote) failed() bool {
	return n.form.Error != "" || n.pasted.Error != "" || n.err != ""
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
	page := partsPage{View: viewMine, Cats: slices.Concat(commonSlots, []string{otherSlot})}

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
		if s.Lendable && s.Status == partOwned {
			page.Lending++
		}
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

// PartsPage renders GET /parts: the viewer's parts, or with ?view=friends
// the spares friends lend (narrowed to one category with ?cat=).
func (h *Handler) PartsPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("view") == viewFriends {
		h.renderFriendSpares(w, r, http.StatusOK, borrowNote{})
		return
	}
	page, err := h.partsBoard(r.Context(), viewer(r).ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, partsTemplate, page)
}

// finishPartsAction ends a parts page action: a 303 back to the page for
// native forms, or the re-rendered page body for htmx. A failed action
// re-renders with its note, and a failed add with the form filled in (422
// for native forms, 200 for htmx so it swaps in).
func (h *Handler) finishPartsAction(w http.ResponseWriter, r *http.Request, note partsNote) {
	if !note.failed() && redirectNative(w, r, partsPath) {
		return
	}
	page, err := h.partsBoard(r.Context(), viewer(r).ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	page.Form, page.Import, page.Error = note.form, note.pasted, note.err
	if isHX(r) {
		h.web.Fragment(w, "parts_board", page)
		return
	}
	h.render(w, r, http.StatusUnprocessableEntity, partsTemplate, page)
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
	price, priceOK := formPrice(r)
	form.Error = tooLong(
		textField{"Part name", form.Name, maxPartNameLen},
		textField{"Category", form.Category, maxCategoryLen},
		textField{"Manufacturer", form.Manufacturer, maxPartFieldLen},
		textField{"Model", form.Model, maxPartFieldLen},
	)
	switch {
	case form.Name == "":
		form.Error = partNameRequired
	case !priceOK:
		form.Error = badPrice
	}
	if form.Error != "" {
		h.finishPartsAction(w, r, partsNote{form: form})
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
	h.finishPartsAction(w, r, partsNote{})
}

// ImportSpares handles POST /parts/import: a pasted PCPartPicker list
// (its Markup > Text) becomes the viewer's spares, all at once.
func (h *Handler) ImportSpares(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBody)
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, http.StatusBadRequest, "failed to parse form")
		return
	}
	form := pcppForm{
		Text:   strings.TrimSpace(r.FormValue("pcpartpicker_text")),
		Wanted: r.FormValue("wanted") != "",
		Lend:   r.FormValue("lend") != "",
	}
	rows, source, msg := importRows(form.Text, nil)
	if msg != "" {
		form.Error = msg
		h.finishPartsAction(w, r, partsNote{pasted: form})
		return
	}
	status := partOwned
	if form.Wanted {
		status = partWanted
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	q := h.queries.WithTx(tx)
	for _, row := range rows {
		p := rowToCreateParams(row, viewer(r).ID, sql.NullInt64{}, status, source)
		p.Lendable = form.Lend && !form.Wanted
		if _, err := q.CreatePart(r.Context(), p); err != nil {
			h.serverError(w, r, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.finishPartsAction(w, r, partsNote{})
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

// finishPartWrite ends a write that should have changed one row: the
// re-rendered page, with the note unchanged if it changed none. The write
// queries filter on the viewer, so that's either someone else's part or one
// that moved since the page loaded (a lender took it back first); the page
// shows its parts as they are now either way.
func (h *Handler) finishPartWrite(w http.ResponseWriter, r *http.Request, n int64, err error, unchanged string) {
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if n == 0 {
		h.finishPartsAction(w, r, partsNote{err: unchanged})
		return
	}
	h.finishPartsAction(w, r, partsNote{})
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
	h.finishPartWrite(w, r, n, err, "Only your own parts that you've bought can be lent.")
}

// ReturnPart handles POST /parts/{id}/return: the lender takes a part back,
// or the borrower gives it back. Either way it goes to its owner's spares.
func (h *Handler) ReturnPart(w http.ResponseWriter, r *http.Request) {
	id, ok := h.partAction(w, r)
	if !ok {
		return
	}
	n, err := h.queries.UnassignPart(r.Context(), db.UnassignPartParams{PartID: id, UserID: viewer(r).ID})
	h.finishPartWrite(w, r, n, err, "That part isn't on loan to or from you anymore.")
}

// DeleteSparePart handles POST /parts/{id}/delete. Only a spare can be
// deleted here; a part in a machine is removed from its slot.
func (h *Handler) DeleteSparePart(w http.ResponseWriter, r *http.Request) {
	id, ok := h.partAction(w, r)
	if !ok {
		return
	}
	n, err := h.queries.DeleteSparePart(r.Context(), db.DeleteSparePartParams{ID: id, OwnerID: viewer(r).ID})
	h.finishPartWrite(w, r, n, err,
		"That part isn't one of your spares, so it can't be deleted here. A part on loan stays where it is.")
}
