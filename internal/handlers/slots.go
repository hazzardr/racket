package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"racket/internal/db"
)

// getSlot fetches a slot or renders the error response and returns false.
func (h *Handler) getSlot(w http.ResponseWriter, r *http.Request) (db.GetSlotRow, bool) {
	id, err := parseID(r, "id")
	if err != nil {
		h.renderError(w, r, http.StatusBadRequest, "invalid slot id")
		return db.GetSlotRow{}, false
	}
	slot, err := h.queries.GetSlot(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderError(w, r, http.StatusNotFound, "Slot not found.")
			return db.GetSlotRow{}, false
		}
		h.serverError(w, r, err)
		return db.GetSlotRow{}, false
	}
	return slot, true
}

// ownSlot is getSlot for changes: it also renders a 403 unless the viewer
// owns the slot's machine.
func (h *Handler) ownSlot(w http.ResponseWriter, r *http.Request) (db.GetSlotRow, bool) {
	slot, ok := h.getSlot(w, r)
	if !ok {
		return db.GetSlotRow{}, false
	}
	if slot.MachineOwnerID != viewer(r).ID {
		h.forbidden(w, r)
		return db.GetSlotRow{}, false
	}
	return slot, true
}

// slotRow returns the slot's current row, with its part if any.
func (h *Handler) slotRow(r *http.Request, slot db.GetSlotRow) (db.ListSlotsByMachineRow, error) {
	rows, err := h.queries.ListSlotsByMachine(r.Context(), slot.MachineID)
	if err != nil {
		return db.ListSlotsByMachineRow{}, err
	}
	for _, row := range rows {
		if row.ID == slot.ID {
			return row, nil
		}
	}
	return db.ListSlotsByMachineRow{}, fmt.Errorf("slot %d not found in machine %d", slot.ID, slot.MachineID)
}

// AddPart handles POST /slots/{id}/part: creates one of the viewer's parts in
// the slot and returns the re-rendered board.
func (h *Handler) AddPart(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, http.StatusBadRequest, "failed to parse form")
		return
	}
	slot, ok := h.ownSlot(w, r)
	if !ok {
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		h.finishSlotAction(w, r, slot, boardNote{err: "Part name is required."})
		return
	}
	status := partOwned
	if r.FormValue("wanted") != "" {
		status = partWanted
	}
	var price sql.NullInt64
	if p, ok := parsePrice(r.FormValue("price")); ok {
		price = sql.NullInt64{Int64: toCents(p), Valid: true}
	}

	_, err := h.queries.CreatePart(r.Context(), db.CreatePartParams{
		OwnerID:      viewer(r).ID,
		SlotID:       sql.NullInt64{Int64: slot.ID, Valid: true},
		Status:       status,
		Name:         name,
		Category:     sql.NullString{String: slot.Category, Valid: true},
		Manufacturer: optionalString(strings.TrimSpace(r.FormValue("manufacturer"))),
		Model:        optionalString(strings.TrimSpace(r.FormValue("model"))),
		PriceCents:   price,
	})
	if err != nil {
		if isUniqueViolation(err) {
			h.finishSlotAction(w, r, slot, boardNote{err: "Slot already filled."})
			return
		}
		h.serverError(w, r, err)
		return
	}
	h.finishSlotAction(w, r, slot, boardNote{seated: true})
}

// RemovePart handles POST /slots/{id}/part/delete. The machine's owner
// removing their own part deletes it; a borrowed part goes back to its
// owner's inventory, whether the borrower returns it or the lender takes it
// back.
func (h *Handler) RemovePart(w http.ResponseWriter, r *http.Request) {
	slot, ok := h.getSlot(w, r)
	if !ok {
		return
	}
	row, err := h.slotRow(r, slot)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if !row.PartID.Valid {
		h.finishSlotAction(w, r, slot, boardNote{})
		return
	}

	v := viewer(r)
	var n int64
	if slot.MachineOwnerID == v.ID && row.PartOwnerID.Int64 == v.ID {
		n, err = h.queries.DeletePart(r.Context(), db.DeletePartParams{ID: row.PartID.Int64, OwnerID: v.ID})
	} else {
		n, err = h.queries.UnassignPart(r.Context(), db.UnassignPartParams{PartID: row.PartID.Int64, UserID: v.ID})
	}
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if n == 0 {
		h.forbidden(w, r)
		return
	}
	h.finishSlotAction(w, r, slot, boardNote{})
}

// CreateSlot handles POST /machines/{id}/slots (form field "category") and
// returns the re-rendered board.
func (h *Handler) CreateSlot(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, http.StatusBadRequest, "failed to parse form")
		return
	}
	machine, ok := h.ownMachine(w, r)
	if !ok {
		return
	}
	category := strings.TrimSpace(r.FormValue("category"))
	if category == "" {
		h.renderError(w, r, http.StatusBadRequest, "category is required")
		return
	}

	slot, err := h.queries.CreateSlot(r.Context(), db.CreateSlotParams{
		MachineID: machine.Machine.ID,
		Category:  category,
	})
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.finishSlotAction(w, r, db.GetSlotRow{
		ID:             slot.ID,
		MachineID:      slot.MachineID,
		MachineOwnerID: machine.Machine.OwnerID,
	}, boardNote{})
}

// DeleteSlot handles POST /slots/{id}/delete. A part in the slot goes back to
// its owner's inventory.
func (h *Handler) DeleteSlot(w http.ResponseWriter, r *http.Request) {
	slot, ok := h.ownSlot(w, r)
	if !ok {
		return
	}
	_, err := h.queries.DeleteSlot(r.Context(), db.DeleteSlotParams{SlotID: slot.ID, UserID: viewer(r).ID})
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.finishSlotAction(w, r, slot, boardNote{})
}

// PlacePart handles POST /slots/{id}/part/place (form field "part_id"): the
// machine's owner puts a spare part in the slot, their own or a friend's.
// A part already in the slot goes back to its owner's spares, unless the
// form has "replace" and it's the owner's part not bought yet: then the
// spare stands in for it, and it's deleted.
func (h *Handler) PlacePart(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, http.StatusBadRequest, "failed to parse form")
		return
	}
	slot, ok := h.ownSlot(w, r)
	if !ok {
		return
	}
	partID, err := strconv.ParseInt(r.FormValue("part_id"), 10, 64)
	if err != nil {
		h.renderError(w, r, http.StatusBadRequest, "invalid part id")
		return
	}
	placed, err := h.placePart(r.Context(), slot, partID, viewer(r).ID, r.FormValue("replace") != "")
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if !placed {
		h.finishSlotAction(w, r, slot, boardNote{err: "That part can't go in this slot. It may be in another machine now."})
		return
	}
	h.finishSlotAction(w, r, slot, boardNote{seated: true})
}

// placePart swaps the part into the slot in one transaction and reports
// whether the user was allowed to. With replace, the user's own part not
// bought yet that was in the slot is deleted instead of set aside.
func (h *Handler) placePart(ctx context.Context, slot db.GetSlotRow, partID, userID int64, replace bool) (bool, error) {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	q := h.queries.WithTx(tx)

	rows, err := q.ListSlotsByMachine(ctx, slot.MachineID)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row.ID != slot.ID || !row.PartID.Valid {
			continue
		}
		if row.PartID.Int64 == partID {
			return true, nil
		}
		if replace && row.PartStatus.String == partWanted && row.PartOwnerID.Int64 == userID {
			_, err = q.DeletePart(ctx, db.DeletePartParams{ID: row.PartID.Int64, OwnerID: userID})
		} else {
			_, err = q.UnassignPart(ctx, db.UnassignPartParams{PartID: row.PartID.Int64, UserID: userID})
		}
		if err != nil {
			return false, err
		}
	}
	n, err := q.AssignPartToSlot(ctx, db.AssignPartToSlotParams{
		SlotID: sql.NullInt64{Int64: slot.ID, Valid: true},
		PartID: partID,
		UserID: userID,
	})
	if err != nil || n == 0 {
		return false, err
	}
	return true, tx.Commit()
}

// TakeOutPart handles POST /slots/{id}/part/takeout: the machine's owner
// takes the part out without deleting it. It goes back to its owner's
// spares, so a borrowed part goes back to the friend who lent it. With form
// field "lend", the owner's own part also becomes lendable.
func (h *Handler) TakeOutPart(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, http.StatusBadRequest, "failed to parse form")
		return
	}
	slot, ok := h.ownSlot(w, r)
	if !ok {
		return
	}
	row, err := h.slotRow(r, slot)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if !row.PartID.Valid {
		h.finishSlotAction(w, r, slot, boardNote{})
		return
	}
	partID, userID := row.PartID.Int64, viewer(r).ID
	if r.FormValue("lend") != "" && row.PartOwnerID.Int64 == userID {
		n, err := h.queries.SetPartLendable(r.Context(), db.SetPartLendableParams{Lendable: true, ID: partID, UserID: userID})
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		if n == 0 {
			h.finishSlotAction(w, r, slot, boardNote{err: "Only parts you've bought can be lent."})
			return
		}
	}
	if _, err = h.queries.UnassignPart(r.Context(), db.UnassignPartParams{PartID: partID, UserID: userID}); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.finishSlotAction(w, r, slot, boardNote{})
}
