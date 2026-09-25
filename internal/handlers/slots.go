package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"racket/internal/db"
)

// slotCard is one slot card on the machine detail page. Error is set when a
// slot operation fails inline (e.g. double-fill) — htmx swaps the fragment
// back with the error embedded (200, not 422, so the swap applies).
type slotCard struct {
	db.ListSlotsByMachineRow
	Error string
}

// slotVM builds a slotCard from a slot and its part (nil part = empty slot).
func slotVM(slot db.MachineSlot, part *db.Part) slotCard {
	card := slotCard{ListSlotsByMachineRow: db.ListSlotsByMachineRow{
		ID:        slot.ID,
		MachineID: slot.MachineID,
		Category:  slot.Category,
		CreatedAt: slot.CreatedAt,
	}}
	if part != nil {
		card.PartID = sql.NullInt64{Int64: part.ID, Valid: true}
		card.PartName = sql.NullString{String: part.Name, Valid: true}
		card.PartModel = part.Model
	}
	return card
}

// getSlot fetches a slot or renders the error response and returns false.
func (h *Handler) getSlot(w http.ResponseWriter, r *http.Request, id int64) (db.MachineSlot, bool) {
	slot, err := h.queries.GetSlot(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderError(w, r, http.StatusNotFound, "Slot not found.")
			return db.MachineSlot{}, false
		}
		h.serverError(w, r, err)
		return db.MachineSlot{}, false
	}
	return slot, true
}

// AssignPart handles POST /slots/{id}/part: creates a part in the slot and
// returns the updated slot card fragment.
func (h *Handler) AssignPart(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		h.renderError(w, r, http.StatusBadRequest, "invalid slot id")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, http.StatusBadRequest, "failed to parse form")
		return
	}
	slot, ok := h.getSlot(w, r, id)
	if !ok {
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		card := slotVM(slot, nil)
		card.Error = "Part name is required."
		h.web.Fragment(w, "slot_card", card)
		return
	}

	part, err := h.queries.CreatePart(r.Context(), db.CreatePartParams{
		MachineID:    sql.NullInt64{Int64: slot.MachineID, Valid: true},
		Name:         name,
		Manufacturer: optionalString(strings.TrimSpace(r.FormValue("manufacturer"))),
		Model:        optionalString(strings.TrimSpace(r.FormValue("model"))),
		SlotID:       sql.NullInt64{Int64: slot.ID, Valid: true},
	})
	if err != nil {
		if isUniqueViolation(err) {
			h.renderFilledSlotError(w, r, slot)
			return
		}
		h.serverError(w, r, err)
		return
	}

	if redirectNative(w, r, fmt.Sprintf("%s/%d", machinesPath, slot.MachineID)) {
		return
	}
	h.web.Fragment(w, "slot_card", slotVM(slot, &part))
}

// renderFilledSlotError re-renders the slot card in its filled state with an
// error, for double-fill attempts. Queries the current slot state to show the
// already-assigned part in the card.
func (h *Handler) renderFilledSlotError(w http.ResponseWriter, r *http.Request, slot db.MachineSlot) {
	rows, err := h.queries.ListSlotsByMachine(r.Context(), slot.MachineID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	for _, row := range rows {
		if row.ID == slot.ID {
			card := slotCard{ListSlotsByMachineRow: row, Error: "Slot already filled."}
			h.web.Fragment(w, "slot_card", card)
			return
		}
	}
	h.serverError(w, r, fmt.Errorf("slot %d not found in machine %d", slot.ID, slot.MachineID))
}

// UnassignPart handles POST /slots/{id}/part/delete: deletes the part in the
// slot (matching the old UI's remove-part semantics) and returns the
// empty-state slot card fragment.
func (h *Handler) UnassignPart(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		h.renderError(w, r, http.StatusBadRequest, "invalid slot id")
		return
	}
	slot, ok := h.getSlot(w, r, id)
	if !ok {
		return
	}
	if err := h.queries.DeletePartsBySlot(r.Context(), sql.NullInt64{Int64: id, Valid: true}); err != nil {
		h.serverError(w, r, err)
		return
	}
	if redirectNative(w, r, fmt.Sprintf("%s/%d", machinesPath, slot.MachineID)) {
		return
	}
	h.web.Fragment(w, "slot_card", slotVM(slot, nil))
}

// CreateSlot handles POST /machines/{id}/slots (form field "category") and
// returns the new empty slot card fragment.
func (h *Handler) CreateSlot(w http.ResponseWriter, r *http.Request) {
	machineID, err := parseID(r, "id")
	if err != nil {
		h.renderError(w, r, http.StatusBadRequest, "invalid machine id")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, http.StatusBadRequest, "failed to parse form")
		return
	}
	category := strings.TrimSpace(r.FormValue("category"))
	if category == "" {
		h.renderError(w, r, http.StatusBadRequest, "category is required")
		return
	}
	if _, err := h.queries.GetMachine(r.Context(), machineID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderError(w, r, http.StatusNotFound, "Machine not found.")
			return
		}
		h.serverError(w, r, err)
		return
	}

	slot, err := h.queries.CreateSlot(r.Context(), db.CreateSlotParams{
		MachineID: machineID,
		Category:  category,
	})
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	if redirectNative(w, r, fmt.Sprintf("%s/%d", machinesPath, machineID)) {
		return
	}
	h.web.Fragment(w, "slot_card", slotVM(slot, nil))
}

// DeleteSlot handles POST /slots/{id}/delete. Assigned parts are unassigned
// first (they survive in "Other parts"). The htmx caller uses hx-swap="delete"
// to remove the card, so the response body is empty.
func (h *Handler) DeleteSlot(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		h.renderError(w, r, http.StatusBadRequest, "invalid slot id")
		return
	}
	slot, ok := h.getSlot(w, r, id)
	if !ok {
		return
	}
	if err := h.queries.UnassignPartsBySlot(r.Context(), sql.NullInt64{Int64: id, Valid: true}); err != nil {
		h.serverError(w, r, err)
		return
	}
	if err := h.queries.DeleteSlot(r.Context(), id); err != nil {
		h.serverError(w, r, err)
		return
	}
	if redirectNative(w, r, fmt.Sprintf("%s/%d", machinesPath, slot.MachineID)) {
		return
	}
	w.WriteHeader(http.StatusOK)
}
