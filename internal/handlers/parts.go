package handlers

import (
	"database/sql"
	"errors"
	"net/http"

	"racket/internal/components"
	"racket/internal/db"
)

func (h *Handler) ListParts(w http.ResponseWriter, r *http.Request) {
	parts, err := h.queries.ListParts(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	machines, err := h.queries.ListMachines(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(r.Context(), w, components.PartsIndex(parts, machines))
}

func (h *Handler) NewPartForm(w http.ResponseWriter, r *http.Request) {
	machines, err := h.queries.ListMachines(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(r.Context(), w, components.PartForm("/parts", db.Part{}, machines, false))
}

func (h *Handler) CreatePart(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	params := db.CreatePartParams{
		MachineID:       components.ParseOptionalInt(r.FormValue("machine_id")),
		Name:            r.FormValue("name"),
		Category:        components.OptionalString(r.FormValue("category")),
		Description:     components.OptionalString(r.FormValue("description")),
		Manufacturer:    components.OptionalString(r.FormValue("manufacturer")),
		Model:           components.OptionalString(r.FormValue("model")),
		SerialNumber:    components.OptionalString(r.FormValue("serial_number")),
		WarrantyExpires: components.ParseDate(r.FormValue("warranty_expires")),
		PurchasedOn:     components.ParseDate(r.FormValue("purchased_on")),
		PurchasedFrom:   components.OptionalString(r.FormValue("purchased_from")),
		PurchasePrice:   components.ParseOptionalFloat(r.FormValue("purchase_price")),
		GuideUrl:        components.OptionalString(r.FormValue("guide_url")),
	}
	if _, err := h.queries.CreatePart(r.Context(), params); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	parts, err := h.queries.ListParts(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(r.Context(), w, components.PartList(parts))
}

func (h *Handler) GetPart(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	part, err := h.queries.GetPart(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	machine := db.Machine{}
	if part.MachineID.Valid {
		machine, err = h.queries.GetMachine(r.Context(), part.MachineID.Int64)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	render(r.Context(), w, components.PartDetail(part, machine))
}

func (h *Handler) EditPartForm(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	part, err := h.queries.GetPart(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	machines, err := h.queries.ListMachines(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(r.Context(), w, components.PartForm("/parts/"+components.StrconvID(id), part, machines, true))
}

func (h *Handler) UpdatePart(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	params := db.UpdatePartParams{
		ID:              id,
		MachineID:       components.ParseOptionalInt(r.FormValue("machine_id")),
		Name:            r.FormValue("name"),
		Category:        components.OptionalString(r.FormValue("category")),
		Description:     components.OptionalString(r.FormValue("description")),
		Manufacturer:    components.OptionalString(r.FormValue("manufacturer")),
		Model:           components.OptionalString(r.FormValue("model")),
		SerialNumber:    components.OptionalString(r.FormValue("serial_number")),
		WarrantyExpires: components.ParseDate(r.FormValue("warranty_expires")),
		PurchasedOn:     components.ParseDate(r.FormValue("purchased_on")),
		PurchasedFrom:   components.OptionalString(r.FormValue("purchased_from")),
		PurchasePrice:   components.ParseOptionalFloat(r.FormValue("purchase_price")),
		GuideUrl:        components.OptionalString(r.FormValue("guide_url")),
	}
	if _, err := h.queries.UpdatePart(r.Context(), params); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	parts, err := h.queries.ListParts(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(r.Context(), w, components.PartList(parts))
}

func (h *Handler) DeletePart(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := h.queries.DeletePart(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
