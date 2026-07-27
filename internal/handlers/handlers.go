package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"racket/internal/db"
)

type MachineResponse struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Location    *string `json:"location"`
	BuiltOn     *string `json:"built_on"`
	PurchasedOn *string `json:"purchased_on"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

func newMachineResponse(m db.Machine) MachineResponse {
	r := MachineResponse{
		ID:        m.ID,
		Name:      m.Name,
		CreatedAt: m.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt: m.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if m.Description.Valid {
		r.Description = &m.Description.String
	}
	if m.Location.Valid {
		r.Location = &m.Location.String
	}
	if m.BuiltOn.Valid {
		s := m.BuiltOn.Time.Format("2006-01-02")
		r.BuiltOn = &s
	}
	if m.PurchasedOn.Valid {
		s := m.PurchasedOn.Time.Format("2006-01-02")
		r.PurchasedOn = &s
	}
	return r
}

type PartResponse struct {
	ID              int64           `json:"id"`
	MachineID       *int64          `json:"machine_id"`
	SlotID          *int64          `json:"slot_id"`
	Machine         *MachineSummary `json:"machine"`
	Name            string          `json:"name"`
	Category        *string         `json:"category"`
	Description     *string         `json:"description"`
	Manufacturer    *string         `json:"manufacturer"`
	Model           *string         `json:"model"`
	SerialNumber    *string         `json:"serial_number"`
	WarrantyExpires *string         `json:"warranty_expires"`
	PurchasedOn     *string         `json:"purchased_on"`
	PurchasedFrom   *string         `json:"purchased_from"`
	PurchasePrice   *float64        `json:"purchase_price"`
	GuideURL        *string         `json:"guide_url"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
}

type MachineSummary struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func newPartResponse(p db.Part) PartResponse {
	r := PartResponse{
		ID:        p.ID,
		Name:      p.Name,
		CreatedAt: p.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt: p.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if p.MachineID.Valid {
		r.MachineID = &p.MachineID.Int64
	}
	if p.SlotID.Valid {
		r.SlotID = &p.SlotID.Int64
	}
	if p.Category.Valid {
		r.Category = &p.Category.String
	}
	if p.Description.Valid {
		r.Description = &p.Description.String
	}
	if p.Manufacturer.Valid {
		r.Manufacturer = &p.Manufacturer.String
	}
	if p.Model.Valid {
		r.Model = &p.Model.String
	}
	if p.SerialNumber.Valid {
		r.SerialNumber = &p.SerialNumber.String
	}
	if p.WarrantyExpires.Valid {
		s := p.WarrantyExpires.Time.Format("2006-01-02")
		r.WarrantyExpires = &s
	}
	if p.PurchasedOn.Valid {
		s := p.PurchasedOn.Time.Format("2006-01-02")
		r.PurchasedOn = &s
	}
	if p.PurchasedFrom.Valid {
		r.PurchasedFrom = &p.PurchasedFrom.String
	}
	if p.PurchasePrice.Valid {
		r.PurchasePrice = &p.PurchasePrice.Float64
	}
	if p.GuideUrl.Valid {
		r.GuideURL = &p.GuideUrl.String
	}
	return r
}

type SlotResponse struct {
	ID       int64             `json:"id"`
	Category string            `json:"category"`
	Part     *SlotPartResponse `json:"part"`
}

type SlotPartResponse struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Model string `json:"model"`
}

func newSlotResponse(s db.ListSlotsByMachineRow) SlotResponse {
	resp := SlotResponse{
		ID:       s.ID,
		Category: s.Category,
	}
	if s.PartID.Valid {
		resp.Part = &SlotPartResponse{
			ID:   s.PartID.Int64,
			Name: s.PartName.String,
		}
		if s.PartModel.Valid {
			resp.Part.Model = s.PartModel.String
		}
	}
	return resp
}

type DashboardResponse struct {
	MachineCount int               `json:"machine_count"`
	PartCount    int               `json:"part_count"`
	Machines     []MachineResponse `json:"machines"`
	Parts        []PartResponse    `json:"parts"`
}

type Handler struct {
	queries *db.Queries
}

func New(q *db.Queries) *Handler {
	return &Handler{queries: q}
}

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	machines, err := h.queries.ListMachines(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	parts, err := h.queries.ListParts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := DashboardResponse{
		MachineCount: len(machines),
		PartCount:    len(parts),
		Machines:     make([]MachineResponse, len(machines)),
		Parts:        make([]PartResponse, len(parts)),
	}
	for i, m := range machines {
		resp.Machines[i] = newMachineResponse(m)
	}
	for i, p := range parts {
		resp.Parts[i] = newPartResponse(p)
	}

	writeJSON(w, http.StatusOK, resp)
}

func parseID(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(chi.URLParam(r, name), 10, 64)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("failed to encode json response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
