package handlers

import (
	"context"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"racket/internal/db"
)

// The parts page's friends' tab: every spare friends lend, by category,
// each with the slots in the viewer's machines it could go in. A spare
// "matches" a slot holding the viewer's part not bought yet with the same
// name; borrowing it there replaces the planned part.

// Parts page tabs.
const (
	viewMine    = "mine"
	viewFriends = "friends"
)

// friendsView is the friends' tab data.
type friendsView struct {
	Groups []friendGroup
	// Filters are the categories friends lend in, for narrowing the tab
	// to one; Filter is the chosen one's key ("" for all).
	Filters []categoryFilter
	Filter  string
	// Total counts friends' spares; Matching counts the ones that match a
	// part the viewer hasn't bought yet.
	Total    int
	Matching int
	// Borrowed and Error report the last borrow.
	Borrowed *borrowTarget
	Error    string
}

// friendGroup is one category's spares.
type friendGroup struct {
	Category string
	Style    template.CSS
	Spares   []friendSpare
}

// categoryFilter is a category chip on the friends' tab.
type categoryFilter struct {
	Key      string
	Name     string
	Style    template.CSS
	Count    int
	Selected bool
}

// friendSpare is a spare a friend lends, with where the viewer could put it.
type friendSpare struct {
	inventoryPart
	OwnerID   int64
	OwnerName string
	// Targets are the slots it could go in, matches first. Matches are
	// the ones it would replace a planned part in.
	Targets []borrowTarget
	Matches []borrowTarget
}

// borrowTarget is a slot in one of the viewer's machines.
type borrowTarget struct {
	SlotID      int64
	MachineID   int64
	MachineName string
	Category    string
	// Instead: the slot holds the viewer's part not bought yet, which the
	// borrowed part replaces.
	Instead bool
	// PartName is the part going in, for the note after a borrow.
	PartName string
}

// borrowNote is what a borrow tells the friends' tab it re-renders.
type borrowNote struct {
	borrowed *borrowTarget
	err      string
}

// nameKey is a part name as matching compares it, like the queries'
// lower(trim(name)).
func nameKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// friendSpares loads the friends' tab for the viewer, narrowed to the
// category with key filter ("" for all).
func (h *Handler) friendSpares(ctx context.Context, viewerID int64, filter string) (friendsView, error) {
	spares, err := h.queries.ListFriendSpares(ctx, viewerID)
	if err != nil {
		return friendsView{}, err
	}
	slots, err := h.queries.ListBorrowTargets(ctx, viewerID)
	if err != nil {
		return friendsView{}, err
	}

	view := friendsView{Total: len(spares)}
	counts := map[string]*categoryFilter{}
	for _, row := range spares {
		s := newFriendSpare(row, slots)
		key := categoryKey(s.Category)
		if len(s.Matches) > 0 {
			view.Matching++
		}

		if counts[key] == nil {
			counts[key] = &categoryFilter{Key: key, Name: s.Category, Style: s.Style}
			view.Filters = append(view.Filters, categoryFilter{Key: key})
		}
		counts[key].Count++
		if filter != "" && key != filter {
			continue
		}
		if n := len(view.Groups); n == 0 || categoryKey(view.Groups[n-1].Category) != key {
			view.Groups = append(view.Groups, friendGroup{Category: s.Category, Style: s.Style})
		}
		g := &view.Groups[len(view.Groups)-1]
		g.Spares = append(g.Spares, s)
	}
	for i, f := range view.Filters {
		view.Filters[i] = *counts[f.Key]
		view.Filters[i].Selected = f.Key == filter
		if view.Filters[i].Selected {
			view.Filter = filter
		}
	}
	return view, nil
}

// newFriendSpare is a friend's spare with the slots it could go in: empty
// ones of its category, and ones whose planned part it matches (first).
func newFriendSpare(row db.ListFriendSparesRow, slots []db.ListBorrowTargetsRow) friendSpare {
	s := friendSpare{inventoryPart: newInventoryPart(row.Part), OwnerID: row.Part.OwnerID, OwnerName: row.OwnerName}
	key := categoryKey(s.Category)
	for _, slot := range slots {
		t := borrowTarget{
			SlotID: slot.SlotID, MachineID: slot.MachineID, MachineName: slot.MachineName, Category: slot.Category,
		}
		switch {
		case slot.WantedName.Valid && nameKey(slot.WantedName.String) == nameKey(s.Name):
			t.Instead = true
			s.Matches = append(s.Matches, t)
		case !slot.WantedName.Valid && categoryKey(slot.Category) == key:
			s.Targets = append(s.Targets, t)
		}
	}
	s.Targets = slices.Concat(s.Matches, s.Targets)
	return s
}

// renderFriendSpares renders the friends' tab: the whole page, or just its
// body for htmx.
func (h *Handler) renderFriendSpares(w http.ResponseWriter, r *http.Request, status int, note borrowNote) {
	filter := r.URL.Query().Get("cat")
	if r.Method == http.MethodPost {
		filter = r.FormValue("cat")
	}
	view, err := h.friendSpares(r.Context(), viewer(r).ID, filter)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	view.Borrowed, view.Error = note.borrowed, note.err
	page := partsPage{View: viewFriends, Friends: view}
	if isHX(r) {
		h.web.Fragment(w, "parts_board", page)
		return
	}
	h.render(w, r, status, "parts.html", page)
}

// friendsPath is the friends' tab, narrowed to the category with key cat.
func friendsPath(cat string) string {
	q := url.Values{"view": {viewFriends}}
	if cat != "" {
		q.Set("cat", cat)
	}
	return partsPath + "?" + q.Encode()
}

// BorrowPart handles POST /parts/{id}/borrow (form fields "slot_id", and
// "cat" to keep the tab's filter): the viewer puts a friend's spare in a
// slot of one of their machines. A part of theirs not bought yet in the
// slot is replaced, as on the machine page.
func (h *Handler) BorrowPart(w http.ResponseWriter, r *http.Request) {
	partID, ok := h.partAction(w, r)
	if !ok {
		return
	}
	slotID, err := strconv.ParseInt(r.FormValue("slot_id"), 10, 64)
	if err != nil {
		h.renderFriendSpares(w, r, http.StatusUnprocessableEntity, borrowNote{err: "Pick a slot to put it in."})
		return
	}
	slot, err := h.queries.GetSlot(r.Context(), slotID)
	if err != nil || slot.MachineOwnerID != viewer(r).ID {
		h.renderFriendSpares(w, r, http.StatusUnprocessableEntity, borrowNote{err: "Pick a slot in one of your machines."})
		return
	}
	// Only a slot the tab offers for this spare: an empty one of its
	// category, or one whose planned part it matches.
	view, err := h.friendSpares(r.Context(), viewer(r).ID, "")
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	target := findTarget(view, partID, slotID)
	if target == nil {
		h.renderFriendSpares(w, r, http.StatusConflict, borrowNote{
			err: "That part can't go there anymore. Someone may have borrowed it first.",
		})
		return
	}

	placed, err := h.placePart(r.Context(), slot, partID, viewer(r).ID, true)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if !placed {
		h.renderFriendSpares(w, r, http.StatusConflict, borrowNote{
			err: "That part can't go there anymore. Someone may have borrowed it first.",
		})
		return
	}
	if redirectNative(w, r, friendsPath(r.FormValue("cat"))) {
		return
	}
	h.renderFriendSpares(w, r, http.StatusOK, borrowNote{borrowed: target})
}

// findTarget finds the slot slotID among the targets of friend's spare
// partID, or returns nil if it isn't one.
func findTarget(view friendsView, partID, slotID int64) *borrowTarget {
	for _, g := range view.Groups {
		for _, s := range g.Spares {
			if s.ID != partID {
				continue
			}
			for _, t := range s.Targets {
				if t.SlotID == slotID {
					t.PartName = s.Name
					return &t
				}
			}
		}
	}
	return nil
}
