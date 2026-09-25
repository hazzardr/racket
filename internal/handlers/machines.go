package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"racket/internal/db"
)

//nolint:goconst // seed list and chip list serve different purposes; shared strings are incidental
var defaultSlots = []string{"Motherboard", "CPU", "RAM", "RAM", "Storage", "Storage", "GPU", "PSU", "Case"}

// commonSlots are the category chips offered on the machine detail page.
var commonSlots = []string{"Motherboard", "CPU", "RAM", "Storage", "GPU", "PSU", "Case"}

// Part and machine statuses used in code; the full sets are the CHECK
// constraints in the schema.
const (
	partOwned     = "owned"
	partWanted    = "wanted"
	machineActive = "active"
)

// machineKinds and machineStatuses mirror the CHECK constraints on machines.
//
//nolint:goconst // enumerations of the schema's values; tests reuse the literals
var (
	machineKinds    = []string{"desktop", "server", "nas", "network", "laptop", "other"}
	machineStatuses = []string{"planned", machineActive, "retired"}
)

// seedDefaultSlots creates the default slot categories for a new machine.
// Failures are logged but not fatal (slots can be added manually later).
func (h *Handler) seedDefaultSlots(ctx context.Context, q *db.Queries, machineID int64) {
	for _, cat := range defaultSlots {
		if _, err := q.CreateSlot(ctx, db.CreateSlotParams{MachineID: machineID, Category: cat}); err != nil {
			slog.WarnContext(ctx, "failed to seed slot", "machine", machineID, "category", cat, "error", err)
		}
	}
}

// machinesPage is the machine list page data.
type machinesPage struct {
	Machines []db.ListMachinesRow
}

// newMachinePage is the new-machine form state (also used for validation re-renders).
type newMachinePage struct {
	Error       string
	Name        string
	Kind        string
	Status      string
	Description string
	Location    string
	BuiltOn     string
	PurchasedOn string
	Kinds       []string
	Statuses    []string
}

// MachinePage is the machine detail page data.
type MachinePage struct {
	Machine   db.Machine
	OwnerName string
	IsOwner   bool
	Slots     []slotCard
	Cats      []string
}

func (h *Handler) Landing(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, "landing.html", nil)
}

func (h *Handler) MachinesPage(w http.ResponseWriter, r *http.Request) {
	machines, err := h.queries.ListMachines(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, "machines.html", machinesPage{Machines: machines})
}

func (h *Handler) NewMachinePage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, "new_machine.html", newMachinePage{
		Kind:     machineKinds[0],
		Status:   machineActive,
		Kinds:    machineKinds,
		Statuses: machineStatuses,
	})
}

// getMachine fetches a machine whose owner hasn't left, or renders the error
// response and returns false.
func (h *Handler) getMachine(w http.ResponseWriter, r *http.Request, id int64) (db.GetMachineRow, bool) {
	machine, err := h.queries.GetMachine(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderError(w, r, http.StatusNotFound, "Machine not found.")
			return db.GetMachineRow{}, false
		}
		h.serverError(w, r, err)
		return db.GetMachineRow{}, false
	}
	return machine, true
}

// ownMachine is getMachine for changes: it also renders a 403 unless the
// viewer owns the machine.
func (h *Handler) ownMachine(w http.ResponseWriter, r *http.Request) (db.GetMachineRow, bool) {
	id, err := parseID(r, "id")
	if err != nil {
		h.renderError(w, r, http.StatusBadRequest, "invalid machine id")
		return db.GetMachineRow{}, false
	}
	machine, ok := h.getMachine(w, r, id)
	if !ok {
		return db.GetMachineRow{}, false
	}
	if machine.Machine.OwnerID != viewer(r).ID {
		h.forbidden(w, r)
		return db.GetMachineRow{}, false
	}
	return machine, true
}

func (h *Handler) MachineDetailPage(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		h.renderError(w, r, http.StatusBadRequest, "invalid machine id")
		return
	}
	machine, ok := h.getMachine(w, r, id)
	if !ok {
		return
	}
	rows, err := h.queries.ListSlotsByMachine(r.Context(), id)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	v := viewer(r)
	cards := make([]slotCard, len(rows))
	for i, row := range rows {
		cards[i] = newSlotCard(row, v.ID, machine.Machine.OwnerID)
	}

	h.render(w, r, http.StatusOK, "machine_detail.html", MachinePage{
		Machine:   machine.Machine,
		OwnerName: machine.OwnerName,
		IsOwner:   machine.Machine.OwnerID == v.ID,
		Slots:     cards,
		Cats:      commonSlots,
	})
}

// CreateMachine handles POST /machines (form). On success it seeds the default
// slots and redirects to the new machine's page (HX-Redirect for htmx, 303 for
// native forms). Validation errors re-render the form with a 422.
func (h *Handler) CreateMachine(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, http.StatusBadRequest, "failed to parse form")
		return
	}

	page := newMachinePage{
		Name:        strings.TrimSpace(r.FormValue("name")),
		Kind:        r.FormValue("kind"),
		Status:      r.FormValue("status"),
		Description: strings.TrimSpace(r.FormValue("description")),
		Location:    strings.TrimSpace(r.FormValue("location")),
		BuiltOn:     r.FormValue("built_on"),
		PurchasedOn: r.FormValue("purchased_on"),
		Kinds:       machineKinds,
		Statuses:    machineStatuses,
	}
	if page.Kind == "" {
		page.Kind = machineKinds[0]
	}
	if page.Status == "" {
		page.Status = machineActive
	}
	switch {
	case page.Name == "":
		page.Error = "Name is required."
	case !slices.Contains(machineKinds, page.Kind):
		page.Error = "Pick a kind from the list."
	case !slices.Contains(machineStatuses, page.Status):
		page.Error = "Pick a status from the list."
	}
	if page.Error != "" {
		h.render(w, r, http.StatusUnprocessableEntity, "new_machine.html", page)
		return
	}

	machine, err := h.queries.CreateMachine(r.Context(), db.CreateMachineParams{
		OwnerID:     viewer(r).ID,
		Name:        page.Name,
		Kind:        page.Kind,
		Status:      page.Status,
		Description: optionalString(page.Description),
		Location:    optionalString(page.Location),
		BuiltOn:     parseDate(page.BuiltOn),
		PurchasedOn: parseDate(page.PurchasedOn),
	})
	if err != nil {
		if isUniqueViolation(err) {
			page.Error = fmt.Sprintf("You already have a machine named %q.", page.Name)
			h.render(w, r, http.StatusUnprocessableEntity, "new_machine.html", page)
			return
		}
		h.serverError(w, r, err)
		return
	}
	h.seedDefaultSlots(r.Context(), h.queries, machine.ID)

	redirectNativeOrHX(w, r, fmt.Sprintf("%s/%d", machinesPath, machine.ID))
}

// DeleteMachine handles POST /machines/{id}/delete and redirects to the list.
// Parts in the machine go back to their owners' inventories.
func (h *Handler) DeleteMachine(w http.ResponseWriter, r *http.Request) {
	machine, ok := h.ownMachine(w, r)
	if !ok {
		return
	}
	_, err := h.queries.DeleteMachine(r.Context(), db.DeleteMachineParams{
		ID:      machine.Machine.ID,
		OwnerID: viewer(r).ID,
	})
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	redirectNativeOrHX(w, r, machinesPath)
}
