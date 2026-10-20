// Package authtest provides a fake Discord for testing sign-in without the
// network.
package authtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"racket/internal/auth"
)

// Credentials and server the fake expects.
const (
	ClientID     = "test-client"
	ClientSecret = "test-secret"
	GuildID      = "guild-1"
)

// Discord fakes Discord's token endpoint and the two API calls sign-in makes.
// Each authorization code maps to one user.
type Discord struct {
	server *httptest.Server

	mu      sync.Mutex
	users   map[string]auth.Identity // by authorization code
	members map[string]bool          // by user ID
	failAPI bool
	// failMember makes the membership check fail with this status.
	failMember int
}

// NewDiscord starts a fake Discord, closed when the test ends.
func NewDiscord(t *testing.T) *Discord {
	t.Helper()
	d := &Discord{users: map[string]auth.Identity{}, members: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth2/token", d.token)
	mux.HandleFunc("GET /api/users/@me", d.me)
	mux.HandleFunc("GET /api/users/@me/guilds/{guild}/member", d.member)
	d.server = httptest.NewServer(mux)
	t.Cleanup(d.server.Close)
	return d
}

// AddUser makes code sign in as id, as a member of the server or not.
func (d *Discord) AddUser(code string, id auth.Identity, member bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.users[code] = id
	d.members[id.ID] = member
}

// SetFailAPI makes the user endpoint return 500 (or stop doing so).
func (d *Discord) SetFailAPI(fail bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.failAPI = fail
}

// SetFailMember makes the server membership endpoint return status (0 to
// stop), for failures other than "not a member".
func (d *Discord) SetFailMember(status int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.failMember = status
}

// Config returns a DiscordConfig pointed at the fake.
func (d *Discord) Config(redirectURL string) auth.DiscordConfig {
	return auth.DiscordConfig{
		ClientID:     ClientID,
		ClientSecret: ClientSecret,
		RedirectURL:  redirectURL,
		GuildID:      GuildID,
		AuthURL:      d.server.URL + "/oauth2/authorize",
		TokenURL:     d.server.URL + "/oauth2/token",
		APIBase:      d.server.URL + "/api",
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a Discord-style error body.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"message": msg})
}

func (d *Discord) token(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	_, known := d.users[r.FormValue("code")]
	d.mu.Unlock()
	if r.FormValue("client_id") != ClientID || r.FormValue("client_secret") != ClientSecret ||
		r.FormValue("grant_type") != "authorization_code" || !known {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": "token-" + r.FormValue("code"),
		"token_type":   "Bearer",
		"expires_in":   3600,
	})
}

// caller returns the user whose token authorizes r.
func (d *Discord) caller(r *http.Request) (auth.Identity, bool) {
	code, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer token-")
	if !ok {
		return auth.Identity{}, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	id, ok := d.users[code]
	return id, ok
}

func (d *Discord) me(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	fail := d.failAPI
	d.mu.Unlock()
	if fail {
		writeError(w, http.StatusInternalServerError, "boom")
		return
	}
	id, ok := d.caller(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "401: Unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, id)
}

func (d *Discord) member(w http.ResponseWriter, r *http.Request) {
	id, ok := d.caller(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "401: Unauthorized")
		return
	}
	d.mu.Lock()
	member, fail := d.members[id.ID], d.failMember
	d.mu.Unlock()
	if fail != 0 {
		writeError(w, fail, "boom")
		return
	}
	if r.PathValue("guild") != GuildID || !member {
		writeError(w, http.StatusNotFound, "Unknown Guild")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": []string{}})
}
