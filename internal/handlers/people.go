package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"racket/internal/db"
)

// The people page lists everyone who has signed in. Everyone in the Discord
// server counts as a friend: they can see each other's machines and borrow
// what each other lends.

// peoplePage is the people page data: the members matching Query.
type peoplePage struct {
	Query  string
	People []db.ListMembersRow
	// Total counts all members, matching or not.
	Total int
}

// matches reports whether a member's username or display name contains q,
// ignoring case.
func matches(u db.User, q string) bool {
	q = strings.ToLower(q)
	return strings.Contains(strings.ToLower(u.Username), q) ||
		strings.Contains(strings.ToLower(u.DisplayName.String), q)
}

// PeoplePage renders GET /people, optionally searched with ?q=. The search
// box asks htmx for the whole page and swaps in just the list.
func (h *Handler) PeoplePage(w http.ResponseWriter, r *http.Request) {
	members, err := h.queries.ListMembers(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	page := peoplePage{Query: strings.TrimSpace(r.URL.Query().Get("q")), Total: len(members)}
	for _, m := range members {
		if matches(m.User, page.Query) {
			page.People = append(page.People, m)
		}
	}
	h.render(w, r, http.StatusOK, "people.html", page)
}

// profilePage is a member's profile: their machines and the spares they
// lend.
type profilePage struct {
	Member   db.User
	IsMe     bool
	Machines []machineRow
	Lending  []inventoryPart
}

// PageTitle is the member's name, as the profile's heading shows it.
func (p profilePage) PageTitle() string {
	if p.Member.DisplayName.Valid && p.Member.DisplayName.String != "" {
		return p.Member.DisplayName.String
	}
	return p.Member.Username
}

// ProfilePage renders GET /users/{id}.
func (h *Handler) ProfilePage(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		h.renderError(w, r, http.StatusBadRequest, "invalid user id")
		return
	}
	member, err := h.queries.GetMember(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		h.renderError(w, r, http.StatusNotFound, "Nobody here by that ID. They may have left.")
		return
	}
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	page := profilePage{Member: member, IsMe: member.ID == viewer(r).ID}

	machines, err := h.machineRows(r.Context(), viewer(r).ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	for _, m := range machines {
		if m.Machine.OwnerID == member.ID {
			page.Machines = append(page.Machines, m)
		}
	}
	spares, err := h.queries.ListLendingSpares(r.Context(), member.ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	for _, s := range spares {
		page.Lending = append(page.Lending, newInventoryPart(s))
	}
	h.render(w, r, http.StatusOK, "profile.html", page)
}
