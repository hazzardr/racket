package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"racket/internal/db"
)

// sourceMatch pairs a part with its row on a fresh copy of its list; row is
// nil when the list no longer has the part.
type sourceMatch struct {
	part db.Part
	row  *csvRow
}

// matchSourceRows finds each part's row on its list by type and name,
// ignoring case. Each row matches at most one part, so a list with two of
// the same SSD prices both.
func matchSourceRows(parts []db.Part, rows []csvRow) []sourceMatch {
	key := func(category, name string) string {
		return strings.ToLower(strings.TrimSpace(category)) + "\x00" + strings.ToLower(strings.TrimSpace(name))
	}
	available := map[string][]*csvRow{}
	for i := range rows {
		k := key(rows[i].category, rows[i].name)
		available[k] = append(available[k], &rows[i])
	}
	matches := make([]sourceMatch, len(parts))
	for i, p := range parts {
		matches[i] = sourceMatch{part: p}
		k := key(p.Category.String, p.Name)
		if rs := available[k]; len(rs) > 0 {
			matches[i].row = rs[0]
			available[k] = rs[1:]
		}
	}
	return matches
}

// RefreshPrices handles POST /machines/{id}/prices (form field "list", a
// PCPartPicker list pasted again). It updates the prices of the machine's
// not-yet-bought parts that came from that list. Bought parts keep the
// price paid. A part that's no longer on the list is marked as such, not
// matched to something else.
func (h *Handler) RefreshPrices(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, http.StatusBadRequest, "failed to parse form")
		return
	}
	machine, ok := h.ownMachine(w, r)
	if !ok {
		return
	}
	list := parsePCPPText(r.FormValue("list"))
	switch {
	case len(list.rows) == 0:
		h.renderError(w, r, http.StatusBadRequest, noPartsInPaste)
		return
	case list.url == "":
		h.renderError(w, r, http.StatusBadRequest, "Paste the whole list, including its first line "+
			"(PCPartPicker Part List: …), so Racket knows which list it is.")
		return
	}

	v := viewer(r)
	parts, err := h.queries.ListRefreshableParts(r.Context(), db.ListRefreshablePartsParams{
		MachineID: machine.Machine.ID,
		UserID:    v.ID,
	})
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	parts = slices.DeleteFunc(parts, func(p db.Part) bool { return p.SourceUrl.String != list.url })
	if len(parts) == 0 {
		h.renderError(w, r, http.StatusBadRequest,
			"None of this machine's parts that aren't bought yet came from that list.")
		return
	}

	if err := h.applyPriceMatches(r.Context(), v.ID, matchSourceRows(parts, list.rows)); err != nil {
		h.serverError(w, r, err)
		return
	}
	redirectNativeOrHX(w, r, fmt.Sprintf("%s/%d", machinesPath, machine.Machine.ID))
}

// applyPriceMatches writes a refresh's results in one transaction.
func (h *Handler) applyPriceMatches(ctx context.Context, userID int64, matches []sourceMatch) error {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := h.queries.WithTx(tx)
	checkedAt := sql.NullTime{Time: time.Now().UTC(), Valid: true}
	for _, m := range matches {
		if m.row == nil {
			_, err = q.MarkPartMissingFromSource(ctx, db.MarkPartMissingFromSourceParams{
				CheckedAt: checkedAt, ID: m.part.ID, UserID: userID,
			})
		} else {
			var price sql.NullInt64
			if m.row.hasPrice {
				price = sql.NullInt64{Int64: toCents(m.row.price), Valid: true}
			}
			_, err = q.RefreshPartPrice(ctx, db.RefreshPartPriceParams{
				PriceCents: price, CheckedAt: checkedAt, ID: m.part.ID, UserID: userID,
			})
		}
		if err != nil {
			return fmt.Errorf("update part %d: %w", m.part.ID, err)
		}
	}
	return tx.Commit()
}

// MarkPartBought handles POST /slots/{id}/part/bought (optional form field
// "price", what the owner paid). The part stops being part of the plan:
// refreshes leave its price alone from now on.
func (h *Handler) MarkPartBought(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, http.StatusBadRequest, "failed to parse form")
		return
	}
	slot, ok := h.ownSlot(w, r)
	if !ok {
		return
	}
	row, err := h.slotRow(r, slot)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if !row.PartID.Valid {
		h.finishSlotAction(w, r, slot, boardNote{})
		return
	}
	var price sql.NullInt64
	if p, ok := parsePrice(r.FormValue("price")); ok {
		price = sql.NullInt64{Int64: toCents(p), Valid: true}
	}
	n, err := h.queries.MarkPartBought(r.Context(), db.MarkPartBoughtParams{
		PriceCents: price, ID: row.PartID.Int64, UserID: viewer(r).ID,
	})
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if n == 0 {
		h.finishSlotAction(w, r, slot, boardNote{err: "Only your own parts that aren't bought yet can be marked bought."})
		return
	}
	h.finishSlotAction(w, r, slot, boardNote{seated: true})
}
