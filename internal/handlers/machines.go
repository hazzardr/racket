package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"racket/internal/db"
)

//nolint:goconst // seed list and chip list serve different purposes; shared strings are incidental
var defaultSlots = []string{"Motherboard", "CPU", "RAM", "RAM", "Storage", "Storage", "GPU", "PSU", "Case"}

// commonSlots are the category chips offered on the machine detail page.
var commonSlots = []string{"Motherboard", "CPU", "RAM", "Storage", "GPU", "PSU", "Case"}

// seedDefaultSlots creates the default slot categories for a new machine.
// Failures are logged but not fatal (slots can be added manually later).
func (h *Handler) seedDefaultSlots(ctx context.Context, q *db.Queries, machineID int64) {
	for _, cat := range defaultSlots {
		if _, err := q.CreateSlot(ctx, db.CreateSlotParams{MachineID: machineID, Category: cat}); err != nil {
			slog.WarnContext(ctx, "failed to seed slot", "machine", machineID, "category", cat, "error", err)
		}
	}
}

// machineRow is a machine list entry with computed slot-fill and price totals.
type machineRow struct {
	Machine     db.Machine
	FilledSlots int
	TotalSlots  int
	PartsTotal  float64
}

// newMachinePage is the new-machine form state (also used for validation re-renders).
type newMachinePage struct {
	Error       string
	Name        string
	Description string
	Location    string
	BuiltOn     string
	PurchasedOn string
}

// MachinePage is the machine detail page data.
type MachinePage struct {
	Machine db.Machine
	Slots   []slotCard
	Parts   []db.Part // unassigned to any slot
	Cats    []string
}

func (h *Handler) Landing(w http.ResponseWriter, _ *http.Request) {
	h.web.Render(w, http.StatusOK, "landing.html", nil)
}

func (h *Handler) MachinesPage(w http.ResponseWriter, r *http.Request) {
	machines, err := h.queries.ListMachines(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	rows := make([]machineRow, 0, len(machines))
	for _, m := range machines {
		row := machineRow{Machine: m}
		slots, err := h.queries.ListSlotsByMachine(r.Context(), m.ID)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		row.TotalSlots = len(slots)
		for _, s := range slots {
			if s.PartID.Valid {
				row.FilledSlots++
			}
		}
		parts, err := h.queries.ListPartsByMachine(r.Context(), sql.NullInt64{Int64: m.ID, Valid: true})
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		for _, p := range parts {
			if p.PurchasePrice.Valid {
				row.PartsTotal += p.PurchasePrice.Float64
			}
		}
		rows = append(rows, row)
	}

	h.web.Render(w, http.StatusOK, "machines.html", rows)
}

func (h *Handler) NewMachinePage(w http.ResponseWriter, _ *http.Request) {
	h.web.Render(w, http.StatusOK, "new_machine.html", newMachinePage{})
}

func (h *Handler) MachineDetailPage(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		h.renderError(w, r, http.StatusBadRequest, "invalid machine id")
		return
	}
	machine, err := h.queries.GetMachine(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderError(w, r, http.StatusNotFound, "Machine not found.")
			return
		}
		h.serverError(w, r, err)
		return
	}
	slots, err := h.queries.ListSlotsByMachine(r.Context(), id)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	parts, err := h.queries.ListPartsByMachine(r.Context(), sql.NullInt64{Int64: id, Valid: true})
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	unassigned := make([]db.Part, 0, len(parts))
	for _, p := range parts {
		if !p.SlotID.Valid {
			unassigned = append(unassigned, p)
		}
	}
	cards := make([]slotCard, len(slots))
	for i, s := range slots {
		cards[i] = slotCard{ListSlotsByMachineRow: s}
	}

	h.web.Render(w, http.StatusOK, "machine_detail.html", MachinePage{
		Machine: machine,
		Slots:   cards,
		Parts:   unassigned,
		Cats:    commonSlots,
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
		Description: strings.TrimSpace(r.FormValue("description")),
		Location:    strings.TrimSpace(r.FormValue("location")),
		BuiltOn:     r.FormValue("built_on"),
		PurchasedOn: r.FormValue("purchased_on"),
	}
	if page.Name == "" {
		page.Error = "Name is required."
		h.web.Render(w, http.StatusUnprocessableEntity, "new_machine.html", page)
		return
	}

	machine, err := h.queries.CreateMachine(r.Context(), db.CreateMachineParams{
		Name:        page.Name,
		Description: optionalString(page.Description),
		Location:    optionalString(page.Location),
		BuiltOn:     parseDate(page.BuiltOn),
		PurchasedOn: parseDate(page.PurchasedOn),
	})
	if err != nil {
		if isUniqueViolation(err) {
			page.Error = fmt.Sprintf("A machine named %q already exists.", page.Name)
			h.web.Render(w, http.StatusUnprocessableEntity, "new_machine.html", page)
			return
		}
		h.serverError(w, r, err)
		return
	}
	h.seedDefaultSlots(r.Context(), h.queries, machine.ID)

	redirectNativeOrHX(w, r, fmt.Sprintf("%s/%d", machinesPath, machine.ID))
}

// DeleteMachine handles POST /machines/{id}/delete and redirects to the list.
func (h *Handler) DeleteMachine(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		h.renderError(w, r, http.StatusBadRequest, "invalid machine id")
		return
	}
	if err := h.queries.DeleteMachine(r.Context(), id); err != nil {
		h.serverError(w, r, err)
		return
	}
	redirectNativeOrHX(w, r, machinesPath)
}
