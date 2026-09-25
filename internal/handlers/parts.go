package handlers

import (
	"net/http"
)

// DeletePart handles POST /parts/{id}/delete.
func (h *Handler) DeletePart(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		h.renderError(w, r, http.StatusBadRequest, "invalid part id")
		return
	}
	if err := h.queries.DeletePart(r.Context(), id); err != nil {
		h.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
