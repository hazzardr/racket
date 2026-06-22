package handlers

import (
	"database/sql"
	"errors"
	"net/http"

	"racket/internal/components"
	"racket/internal/db"
)

func (h *Handler) ListMachines(w http.ResponseWriter, r *http.Request) {
	machines, err := h.queries.ListMachines(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(r.Context(), w, components.MachinesIndex(machines))
}

func (h *Handler) NewMachineForm(w http.ResponseWriter, r *http.Request) {
	render(r.Context(), w, components.MachineForm("/machines", db.Machine{}, false))
}

func (h *Handler) CreateMachine(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	params := db.CreateMachineParams{
		Name:        r.FormValue("name"),
		Description: components.OptionalString(r.FormValue("description")),
		Location:    components.OptionalString(r.FormValue("location")),
		BuiltOn:     components.ParseDate(r.FormValue("built_on")),
		PurchasedOn: components.ParseDate(r.FormValue("purchased_on")),
	}
	if _, err := h.queries.CreateMachine(r.Context(), params); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	machines, err := h.queries.ListMachines(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(r.Context(), w, components.MachineList(machines))
}

func (h *Handler) GetMachine(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	machine, err := h.queries.GetMachine(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	parts, err := h.queries.ListPartsByMachine(r.Context(), sql.NullInt64{Int64: id, Valid: true})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(r.Context(), w, components.MachineDetail(machine, parts))
}

func (h *Handler) EditMachineForm(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	machine, err := h.queries.GetMachine(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(r.Context(), w, components.MachineForm("/machines/"+components.StrconvID(id), machine, true))
}

func (h *Handler) UpdateMachine(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	params := db.UpdateMachineParams{
		ID:          id,
		Name:        r.FormValue("name"),
		Description: components.OptionalString(r.FormValue("description")),
		Location:    components.OptionalString(r.FormValue("location")),
		BuiltOn:     components.ParseDate(r.FormValue("built_on")),
		PurchasedOn: components.ParseDate(r.FormValue("purchased_on")),
	}
	if _, err := h.queries.UpdateMachine(r.Context(), params); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	machines, err := h.queries.ListMachines(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(r.Context(), w, components.MachineList(machines))
}

func (h *Handler) DeleteMachine(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := h.queries.DeleteMachine(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
