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
		h.renderError(w, r, http.StatusInternalServerError, err.Error())
		return db.MachineSlot{}, false
	}
	return slot, true
}

// nativeRedirect issues a 303 redirect for native (non-htmx) form posts and
// reports whether the request was native. Fragment handlers return early when
// it is; htmx requests carry on and render their fragment.
func nativeRedirect(w http.ResponseWriter, r *http.Request, machineID int64) bool {
	if r.Header.Get("Hx-Request") != "" {
		return false
	}
	//nolint:gosec // path is built from an int64 id only — no taint possible
	http.Redirect(w, r, fmt.Sprintf("/machines/%d", machineID), http.StatusSeeOther)
	return true
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
		if strings.Contains(err.Error(), "UNIQUE") {
			h.renderFilledSlotError(w, r, slot)
			return
		}
		h.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}

	if nativeRedirect(w, r, slot.MachineID) {
		return
	}
	h.web.Fragment(w, "slot_card", slotVM(slot, &part))
}

// renderFilledSlotError re-renders the slot card in its filled state with an
// error, for double-fill attempts.
func (h *Handler) renderFilledSlotError(w http.ResponseWriter, r *http.Request, slot db.MachineSlot) {
	card := slotVM(slot, nil)
	rows, err := h.queries.ListSlotsByMachine(r.Context(), slot.MachineID)
	if err != nil {
		h.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	for _, row := range rows {
		if row.ID == slot.ID {
			card.ListSlotsByMachineRow = row
			break
		}
	}
	card.Error = "Slot already filled."
	h.web.Fragment(w, "slot_card", card)
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
		h.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	if nativeRedirect(w, r, slot.MachineID) {
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
		h.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}

	slot, err := h.queries.CreateSlot(r.Context(), db.CreateSlotParams{
		MachineID: machineID,
		Category:  category,
	})
	if err != nil {
		h.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}

	if nativeRedirect(w, r, machineID) {
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
		h.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.queries.DeleteSlot(r.Context(), id); err != nil {
		h.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	if nativeRedirect(w, r, slot.MachineID) {
		return
	}
	w.WriteHeader(http.StatusOK)
}
