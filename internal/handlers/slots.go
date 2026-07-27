package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"racket/internal/db"
)

type SlotInput struct {
	Category string `json:"category"`
}

func (h *Handler) CreateSlot(w http.ResponseWriter, r *http.Request) {
	machineID, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	var input SlotInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	input.Category = strings.TrimSpace(input.Category)
	if input.Category == "" {
		writeError(w, http.StatusBadRequest, "category is required")
		return
	}

	_, err = h.queries.GetMachine(r.Context(), machineID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "machine not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	slot, err := h.queries.CreateSlot(r.Context(), db.CreateSlotParams{
		MachineID: machineID,
		Category:  input.Category,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, SlotResponse{
		ID:       slot.ID,
		Category: slot.Category,
		Part:     nil,
	})
}

func (h *Handler) DeleteSlot(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	if err := h.queries.UnassignPartsBySlot(r.Context(), sql.NullInt64{Int64: id, Valid: true}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.queries.DeleteSlot(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
