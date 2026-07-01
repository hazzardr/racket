package handlers

import (
	"database/sql"
	"errors"
	"net/http"

	"racket/internal/db"
)

type MachineInput struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Location    *string `json:"location"`
	BuiltOn     *string `json:"built_on"`
	PurchasedOn *string `json:"purchased_on"`
}

func machineParamsFromInput(input MachineInput) db.CreateMachineParams {
	params := db.CreateMachineParams{
		Name: input.Name,
	}
	if input.Description != nil {
		params.Description = optionalString(*input.Description)
	}
	if input.Location != nil {
		params.Location = optionalString(*input.Location)
	}
	if input.BuiltOn != nil {
		params.BuiltOn = parseDate(*input.BuiltOn)
	}
	if input.PurchasedOn != nil {
		params.PurchasedOn = parseDate(*input.PurchasedOn)
	}
	return params
}

func updateMachineParamsFromInput(id int64, input MachineInput) db.UpdateMachineParams {
	params := db.UpdateMachineParams{
		ID:   id,
		Name: input.Name,
	}
	if input.Description != nil {
		params.Description = optionalString(*input.Description)
	}
	if input.Location != nil {
		params.Location = optionalString(*input.Location)
	}
	if input.BuiltOn != nil {
		params.BuiltOn = parseDate(*input.BuiltOn)
	}
	if input.PurchasedOn != nil {
		params.PurchasedOn = parseDate(*input.PurchasedOn)
	}
	return params
}

func (h *Handler) ListMachines(w http.ResponseWriter, r *http.Request) {
	machines, err := h.queries.ListMachines(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := make([]MachineResponse, len(machines))
	for i, m := range machines {
		resp[i] = newMachineResponse(m)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) CreateMachine(w http.ResponseWriter, r *http.Request) {
	var input MachineInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	machine, err := h.queries.CreateMachine(r.Context(), machineParamsFromInput(input))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, newMachineResponse(machine))
}

func (h *Handler) GetMachine(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	machine, err := h.queries.GetMachine(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	parts, err := h.queries.ListPartsByMachine(r.Context(), sql.NullInt64{Int64: id, Valid: true})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := struct {
		Machine MachineResponse `json:"machine"`
		Parts   []PartResponse  `json:"parts"`
	}{
		Machine: newMachineResponse(machine),
		Parts:   make([]PartResponse, len(parts)),
	}
	for i, p := range parts {
		resp.Parts[i] = newPartResponse(p)
	}
	writeJSON(w, http.StatusOK, resp)
}

//nolint:dupl // machine and part update handlers share the same CRUD shape
func (h *Handler) UpdateMachine(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var input MachineInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	machine, err := h.queries.UpdateMachine(r.Context(), updateMachineParamsFromInput(id, input))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, newMachineResponse(machine))
}

func (h *Handler) DeleteMachine(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.queries.DeleteMachine(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
