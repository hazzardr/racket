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
	"racket/internal/web"
)

type PartResponse struct {
	ID              int64    `json:"id"`
	MachineID       *int64   `json:"machine_id"`
	SlotID          *int64   `json:"slot_id"`
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
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
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

type Handler struct {
	queries *db.Queries
	web     *web.Web
}

func New(q *db.Queries, w *web.Web) *Handler {
	return &Handler{queries: q, web: w}
}

// renderError renders the error page with the given status and message.
func (h *Handler) renderError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	slog.ErrorContext(r.Context(), "request error", "status", status, "error", msg)
	h.web.Render(w, status, "error.html", struct{ Message string }{Message: msg})
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
