package handlers

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"racket/internal/auth"
	"racket/internal/db"
)

const (
	discordLoginPath    = "/auth/discord"
	discordCallbackPath = "/auth/discord/callback"
	devLoginPath        = "/auth/dev"

	// stateCookie carries the OAuth state between the redirect to Discord
	// and the callback, so a callback we didn't start is rejected.
	stateCookie = "racket_oauth_state"
	// stateMaxAge bounds how long a sign-in can sit on Discord's page.
	stateMaxAge = 10 * 60

	// devUserPrefix keeps dev-login users apart from real Discord IDs.
	devUserPrefix = "dev:"
	// maxDevUsername bounds dev-login usernames (Discord's own limit is 32).
	maxDevUsername = 32
)

// loginPage is the login page state.
type loginPage struct {
	Error    string
	Discord  bool
	DevLogin bool
}

func (h *Handler) renderLogin(w http.ResponseWriter, r *http.Request, status int, msg string) {
	h.render(w, r, status, "login.html", loginPage{
		Error:    msg,
		Discord:  h.auth.Discord != nil,
		DevLogin: h.auth.DevLogin,
	})
}

// LoginPage handles GET /login. Signed-in users go straight to their machines.
func (h *Handler) LoginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.UserFrom(r.Context()); ok {
		http.Redirect(w, r, machinesPath, http.StatusSeeOther)
		return
	}
	h.renderLogin(w, r, http.StatusOK, "")
}

// DiscordLogin handles GET /auth/discord: sends the browser to Discord.
func (h *Handler) DiscordLogin(w http.ResponseWriter, r *http.Request) {
	state := rand.Text()
	//nolint:gosec // G124: Secure follows the base URL's scheme; local dev is plain HTTP
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookie,
		Value:    state,
		Path:     discordLoginPath,
		MaxAge:   stateMaxAge,
		HttpOnly: true,
		Secure:   h.auth.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, h.auth.Discord.AuthCodeURL(state), http.StatusFound)
}

// DiscordCallback handles GET /auth/discord/callback: Discord sends the
// browser back here with a code (or an error) and our state.
func (h *Handler) DiscordCallback(w http.ResponseWriter, r *http.Request) {
	//nolint:gosec // G124: Secure follows the base URL's scheme; local dev is plain HTTP
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookie,
		Path:     discordLoginPath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.auth.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})

	q := r.URL.Query()
	c, err := r.Cookie(stateCookie)
	if err != nil || q.Get("state") == "" ||
		subtle.ConstantTimeCompare([]byte(c.Value), []byte(q.Get("state"))) != 1 {
		h.renderLogin(w, r, http.StatusBadRequest, "That sign-in link expired. Please try again.")
		return
	}
	if q.Get("error") != "" {
		h.renderLogin(w, r, http.StatusBadRequest, "Discord sign-in was cancelled.")
		return
	}

	id, err := h.auth.Discord.Identify(r.Context(), q.Get("code"))
	if errors.Is(err, auth.ErrNotMember) {
		h.renderLogin(w, r, http.StatusForbidden, "Racket is only open to members of our Discord server.")
		return
	}
	if err != nil {
		slog.WarnContext(r.Context(), "Discord sign-in failed", "error", err)
		h.renderLogin(w, r, http.StatusBadGateway, "Couldn't sign in with Discord. Please try again.")
		return
	}

	user, err := h.queries.UpsertDiscordUser(r.Context(), db.UpsertDiscordUserParams{
		DiscordID:   id.ID,
		Username:    id.Username,
		DisplayName: optionalString(id.GlobalName),
		AvatarHash:  optionalString(id.Avatar),
	})
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.startSession(w, r, user)
}

// DevLogin handles POST /auth/dev: signs in as any username, no Discord.
// It's only routed when dev login is on.
func (h *Handler) DevLogin(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("username"))
	if name == "" || len(name) > maxDevUsername {
		h.renderLogin(w, r, http.StatusUnprocessableEntity, "Pick a username of up to 32 characters.")
		return
	}
	user, err := h.queries.UpsertDiscordUser(r.Context(), db.UpsertDiscordUserParams{
		DiscordID: devUserPrefix + strings.ToLower(name),
		Username:  name,
	})
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.startSession(w, r, user)
}

func (h *Handler) startSession(w http.ResponseWriter, r *http.Request, user db.User) {
	if err := h.auth.Sessions.Start(r.Context(), w, user.ID); err != nil {
		h.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, machinesPath, http.StatusSeeOther)
}

// Logout handles POST /logout.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if err := h.auth.Sessions.End(w, r); err != nil {
		h.serverError(w, r, err)
		return
	}
	redirectNativeOrHX(w, r, "/")
}
