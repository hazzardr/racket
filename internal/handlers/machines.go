package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
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

// seedDefaultSlots creates the default slot categories for the owner's new
// machine. Run it in the transaction that created the machine, so a
// machine never starts with only some of them.
func seedDefaultSlots(ctx context.Context, q *db.Queries, ownerID, machineID int64) error {
	for _, cat := range defaultSlots {
		_, err := q.CreateSlot(ctx, db.CreateSlotParams{MachineID: machineID, Category: cat, UserID: ownerID})
		if err != nil {
			return fmt.Errorf("seed %s slot: %w", cat, err)
		}
	}
	return nil
}

// machinesPage is the machine list page data: the viewer's machines, then
// everyone else's.
type machinesPage struct {
	Mine   []machineRow
	Others []machineRow
}

// machineRow is one machine in the list; Mine says the viewer owns it.
type machineRow struct {
	db.ListMachinesRow
	Mine bool
	// LEDs are the machine's slots in board order, drawn as a strip of
	// lights on its card. Counts tallies them by slot state.
	LEDs   []slotLED
	Counts map[string]int
}

// slotLED is one slot as a light on a machine card or a bay in the new
// machine preview.
type slotLED struct {
	Category string
	Code     string
	State    string
	Style    template.CSS
}

func newSlotLED(category, state string) slotLED {
	return slotLED{Category: category, Code: styleOf(category).code, State: state, Style: typeStyle(category)}
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
	// Slots are the slots a new machine starts with, for the preview.
	Slots []slotLED
}

// newMachineForm is the new-machine form's starting state.
func newMachineForm() newMachinePage {
	page := newMachinePage{
		Kind:     machineKinds[0],
		Status:   machineActive,
		Kinds:    machineKinds,
		Statuses: machineStatuses,
	}
	for _, category := range defaultSlots {
		page.Slots = append(page.Slots, newSlotLED(category, stateEmpty))
	}
	return page
}

// landingPage is the landing page data: the slots a new machine starts with
// and the PCPartPicker type mapping, so the import demo sorts its sample
// list the way a real import does, and the board's category styles, so its
// board looks like a machine page.
type landingPage struct {
	Demo importDemo
}

type importDemo struct {
	Slots     []string
	SlotMap   map[string]string
	OtherSlot string
	// Styles is keyed by matchKey.
	Styles map[string]demoStyle
}

// demoStyle is a categoryStyle the demo can read.
type demoStyle struct {
	Code  string
	Color string
}

func (h *Handler) Landing(w http.ResponseWriter, r *http.Request) {
	styles := make(map[string]demoStyle, len(categoryStyles)+1)
	for key := range categoryStyles {
		s := styleOf(key)
		styles[key] = demoStyle{Code: s.code, Color: s.color}
	}
	other := styleOf(otherSlot)
	styles[matchKey(otherSlot)] = demoStyle{Code: other.code, Color: other.color}
	h.render(w, r, http.StatusOK, "landing.html", landingPage{Demo: importDemo{
		Slots:     defaultSlots,
		SlotMap:   pcppSlotCategories,
		OtherSlot: otherSlot,
		Styles:    styles,
	}})
}

// machineRows lists every machine whose owner hasn't left, as the machine
// list shows them to the viewer.
func (h *Handler) machineRows(ctx context.Context, viewerID int64) ([]machineRow, error) {
	machines, err := h.queries.ListMachines(ctx)
	if err != nil {
		return nil, err
	}
	states, err := h.queries.ListSlotStates(ctx)
	if err != nil {
		return nil, err
	}
	leds := make(map[int64][]slotLED)
	for _, s := range states {
		leds[s.MachineID] = append(leds[s.MachineID], newSlotLED(s.Category, slotState(s.PartSource)))
	}
	rows := make([]machineRow, len(machines))
	for i, m := range machines {
		row := machineRow{ListMachinesRow: m, LEDs: leds[m.Machine.ID], Counts: map[string]int{}}
		for _, led := range row.LEDs {
			row.Counts[led.State]++
		}
		row.Mine = m.Machine.OwnerID == viewerID
		rows[i] = row
	}
	return rows, nil
}

func (h *Handler) MachinesPage(w http.ResponseWriter, r *http.Request) {
	rows, err := h.machineRows(r.Context(), viewer(r).ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	var page machinesPage
	for _, row := range rows {
		if row.Mine {
			page.Mine = append(page.Mine, row)
		} else {
			page.Others = append(page.Others, row)
		}
	}
	h.render(w, r, http.StatusOK, "machines.html", page)
}

func (h *Handler) NewMachinePage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, "new_machine.html", newMachineForm())
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
	page, err := h.machineBoard(r.Context(), machine, viewer(r).ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, "machine_detail.html", page)
}

// CreateMachine handles POST /machines (form). On success it seeds the default
// slots and redirects to the new machine's page (HX-Redirect for htmx, 303 for
// native forms). Validation errors re-render the form with a 422.
func (h *Handler) CreateMachine(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, http.StatusBadRequest, "failed to parse form")
		return
	}

	page := newMachineForm()
	page.Name = strings.TrimSpace(r.FormValue("name"))
	if kind := r.FormValue("kind"); kind != "" {
		page.Kind = kind
	}
	if status := r.FormValue("status"); status != "" {
		page.Status = status
	}
	page.Description = strings.TrimSpace(r.FormValue("description"))
	page.Location = strings.TrimSpace(r.FormValue("location"))
	page.BuiltOn = strings.TrimSpace(r.FormValue("built_on"))
	page.PurchasedOn = strings.TrimSpace(r.FormValue("purchased_on"))
	builtOn, builtOK := parseDate(page.BuiltOn)
	purchasedOn, purchasedOK := parseDate(page.PurchasedOn)
	switch {
	case page.Name == "":
		page.Error = "Name is required."
	case !slices.Contains(machineKinds, page.Kind):
		page.Error = "Pick a kind from the list."
	case !slices.Contains(machineStatuses, page.Status):
		page.Error = "Pick a status from the list."
	case !builtOK || !purchasedOK:
		page.Error = "Dates should look like 2026-10-20."
	default:
		page.Error = tooLong(
			textField{"Name", page.Name, maxNameLen},
			textField{"Description", page.Description, maxDescriptionLen},
			textField{"Location", page.Location, maxLocationLen},
		)
	}
	if page.Error != "" {
		h.render(w, r, http.StatusUnprocessableEntity, "new_machine.html", page)
		return
	}

	id, err := h.createMachine(r.Context(), db.CreateMachineParams{
		OwnerID:     viewer(r).ID,
		Name:        page.Name,
		Kind:        page.Kind,
		Status:      page.Status,
		Description: optionalString(page.Description),
		Location:    optionalString(page.Location),
		BuiltOn:     builtOn,
		PurchasedOn: purchasedOn,
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

	redirectNativeOrHX(w, r, fmt.Sprintf("%s/%d", machinesPath, id))
}

// createMachine creates the machine with its default slots, in one
// transaction, and returns its ID.
func (h *Handler) createMachine(ctx context.Context, p db.CreateMachineParams) (int64, error) {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	q := h.queries.WithTx(tx)
	machine, err := q.CreateMachine(ctx, p)
	if err != nil {
		return 0, err
	}
	if err := seedDefaultSlots(ctx, q, p.OwnerID, machine.ID); err != nil {
		return 0, err
	}
	return machine.ID, tx.Commit()
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
