package handlers

import (
	"database/sql"
	"errors"
	"net/http"

	"racket/internal/db"
)

type PartInput struct {
	MachineID       *int64   `json:"machine_id"`
	Name            string   `json:"name"`
	Category        *string  `json:"category"`
	Description     *string  `json:"description"`
	Manufacturer    *string  `json:"manufacturer"`
	Model           *string  `json:"model"`
	SerialNumber    *string  `json:"serial_number"`
	WarrantyExpires *string  `json:"warranty_expires"`
	PurchasedOn     *string  `json:"purchased_on"`
	PurchasedFrom   *string  `json:"purchased_from"`
	PurchasePrice   *float64 `json:"purchase_price"`
	GuideURL        *string  `json:"guide_url"`
}

func partParamsFromInput(input PartInput) db.CreatePartParams {
	params := db.CreatePartParams{
		Name: input.Name,
	}
	if input.MachineID != nil {
		params.MachineID = sql.NullInt64{Int64: *input.MachineID, Valid: true}
	}
	if input.Category != nil {
		params.Category = optionalString(*input.Category)
	}
	if input.Description != nil {
		params.Description = optionalString(*input.Description)
	}
	if input.Manufacturer != nil {
		params.Manufacturer = optionalString(*input.Manufacturer)
	}
	if input.Model != nil {
		params.Model = optionalString(*input.Model)
	}
	if input.SerialNumber != nil {
		params.SerialNumber = optionalString(*input.SerialNumber)
	}
	if input.WarrantyExpires != nil {
		params.WarrantyExpires = parseDate(*input.WarrantyExpires)
	}
	if input.PurchasedOn != nil {
		params.PurchasedOn = parseDate(*input.PurchasedOn)
	}
	if input.PurchasedFrom != nil {
		params.PurchasedFrom = optionalString(*input.PurchasedFrom)
	}
	if input.PurchasePrice != nil {
		params.PurchasePrice = sql.NullFloat64{Float64: *input.PurchasePrice, Valid: true}
	}
	if input.GuideURL != nil {
		params.GuideUrl = optionalString(*input.GuideURL)
	}
	return params
}

func updatePartParamsFromInput(id int64, input PartInput) db.UpdatePartParams {
	params := db.UpdatePartParams{
		ID:   id,
		Name: input.Name,
	}
	if input.MachineID != nil {
		params.MachineID = sql.NullInt64{Int64: *input.MachineID, Valid: true}
	}
	if input.Category != nil {
		params.Category = optionalString(*input.Category)
	}
	if input.Description != nil {
		params.Description = optionalString(*input.Description)
	}
	if input.Manufacturer != nil {
		params.Manufacturer = optionalString(*input.Manufacturer)
	}
	if input.Model != nil {
		params.Model = optionalString(*input.Model)
	}
	if input.SerialNumber != nil {
		params.SerialNumber = optionalString(*input.SerialNumber)
	}
	if input.WarrantyExpires != nil {
		params.WarrantyExpires = parseDate(*input.WarrantyExpires)
	}
	if input.PurchasedOn != nil {
		params.PurchasedOn = parseDate(*input.PurchasedOn)
	}
	if input.PurchasedFrom != nil {
		params.PurchasedFrom = optionalString(*input.PurchasedFrom)
	}
	if input.PurchasePrice != nil {
		params.PurchasePrice = sql.NullFloat64{Float64: *input.PurchasePrice, Valid: true}
	}
	if input.GuideURL != nil {
		params.GuideUrl = optionalString(*input.GuideURL)
	}
	return params
}

func (h *Handler) ListParts(w http.ResponseWriter, r *http.Request) {
	parts, err := h.queries.ListParts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := make([]PartResponse, len(parts))
	for i, p := range parts {
		resp[i] = newPartResponse(p)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) CreatePart(w http.ResponseWriter, r *http.Request) {
	var input PartInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	part, err := h.queries.CreatePart(r.Context(), partParamsFromInput(input))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, newPartResponse(part))
}

func (h *Handler) GetPart(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	part, err := h.queries.GetPart(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := newPartResponse(part)
	if part.MachineID.Valid {
		machine, err := h.queries.GetMachine(r.Context(), part.MachineID.Int64)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err == nil {
			resp.Machine = &MachineSummary{
				ID:   machine.ID,
				Name: machine.Name,
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

//nolint:dupl // machine and part update handlers share the same CRUD shape
func (h *Handler) UpdatePart(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var input PartInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	part, err := h.queries.UpdatePart(r.Context(), updatePartParamsFromInput(id, input))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, newPartResponse(part))
}

func (h *Handler) DeletePart(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.queries.DeletePart(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
