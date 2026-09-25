package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"racket/internal/db"
)

// slotCard is one slot card on the machine detail page, as the viewer sees
// it. Error is set when a slot operation fails inline (e.g. double-fill) —
// htmx swaps the fragment back with the error embedded (200, not 422, so the
// swap applies).
type slotCard struct {
	db.ListSlotsByMachineRow
	// CanEdit: the viewer owns the machine, so may add and remove slots and
	// parts.
	CanEdit bool
	// RemoveLabel names the remove-part button, or is "" when the viewer
	// can't remove the part.
	RemoveLabel string
	Error       string
}

func newSlotCard(row db.ListSlotsByMachineRow, viewerID, machineOwnerID int64) slotCard {
	card := slotCard{ListSlotsByMachineRow: row, CanEdit: viewerID == machineOwnerID}
	if !row.PartID.Valid {
		return card
	}
	ownsPart := row.PartOwnerID.Int64 == viewerID
	switch {
	case card.CanEdit && ownsPart:
		card.RemoveLabel = "Remove"
	case card.CanEdit:
		card.RemoveLabel = "Return"
	case ownsPart:
		card.RemoveLabel = "Take back"
	}
	return card
}

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

// finishSlotAction ends a slot action: a 303 back to the machine for native
// forms, or the slot's current card (with errMsg, if any) for htmx.
func (h *Handler) finishSlotAction(w http.ResponseWriter, r *http.Request, slot db.GetSlotRow, errMsg string) {
	if errMsg == "" && redirectNative(w, r, fmt.Sprintf("%s/%d", machinesPath, slot.MachineID)) {
		return
	}
	row, err := h.slotRow(r, slot)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	card := newSlotCard(row, viewer(r).ID, slot.MachineOwnerID)
	card.Error = errMsg
	h.web.Fragment(w, "slot_card", card)
}

// AddPart handles POST /slots/{id}/part: creates one of the viewer's parts in
// the slot and returns the updated slot card fragment.
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
		h.finishSlotAction(w, r, slot, "Part name is required.")
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
		Manufacturer: optionalString(strings.TrimSpace(r.FormValue("manufacturer"))),
		Model:        optionalString(strings.TrimSpace(r.FormValue("model"))),
		PriceCents:   price,
	})
	if err != nil {
		if isUniqueViolation(err) {
			h.finishSlotAction(w, r, slot, "Slot already filled.")
			return
		}
		h.serverError(w, r, err)
		return
	}
	h.finishSlotAction(w, r, slot, "")
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
		h.finishSlotAction(w, r, slot, "")
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
	h.finishSlotAction(w, r, slot, "")
}

// CreateSlot handles POST /machines/{id}/slots (form field "category") and
// returns the new empty slot card fragment.
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
	}, "")
}

// DeleteSlot handles POST /slots/{id}/delete. A part in the slot goes back to
// its owner's inventory. The htmx caller uses hx-swap="delete" to remove the
// card, so the response body is empty.
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
	if redirectNative(w, r, fmt.Sprintf("%s/%d", machinesPath, slot.MachineID)) {
		return
	}
	w.WriteHeader(http.StatusOK)
}
