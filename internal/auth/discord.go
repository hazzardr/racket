package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/oauth2"
)

// Discord's OAuth2 and REST endpoints.
const (
	discordAuthURL  = "https://discord.com/oauth2/authorize"
	discordTokenURL = "https://discord.com/api/oauth2/token" //nolint:gosec // G101: a URL, not a credential
	discordAPIBase  = "https://discord.com/api/v10"

	// maxDiscordBody caps a Discord API response; user and member objects
	// are a few KB.
	maxDiscordBody = 1 << 20

	// identifyTimeout bounds the calls to Discord during sign-in. It stays
	// under the server's 10s write timeout, so a hung Discord still gets
	// the browser an error page.
	identifyTimeout = 8 * time.Second
)

// ErrNotMember means the Discord user isn't in the configured server.
var ErrNotMember = errors.New("not a member of the Discord server")

// DiscordConfig configures Discord sign-in.
type DiscordConfig struct {
	ClientID     string
	ClientSecret string
	// RedirectURL is <base URL>/auth/discord/callback. It must also be
	// registered on the Discord application.
	RedirectURL string
	// GuildID is the Discord server whose members may sign in.
	GuildID string

	// AuthURL, TokenURL, and APIBase override Discord's endpoints in tests.
	AuthURL, TokenURL, APIBase string
}

// Identity is the Discord account behind a sign-in.
type Identity struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
	Avatar     string `json:"avatar"`
}

// Discord runs the OAuth2 authorization-code flow against Discord.
type Discord struct {
	oauth   oauth2.Config
	apiBase string
	guildID string
}

// NewDiscord returns a Discord sign-in client.
func NewDiscord(cfg DiscordConfig) *Discord {
	authURL, tokenURL, apiBase := discordAuthURL, discordTokenURL, discordAPIBase
	if cfg.AuthURL != "" {
		authURL = cfg.AuthURL
	}
	if cfg.TokenURL != "" {
		tokenURL = cfg.TokenURL
	}
	if cfg.APIBase != "" {
		apiBase = cfg.APIBase
	}
	return &Discord{
		oauth: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			// identify: the user's ID and name. guilds.members.read: whether
			// they're in the server, without listing all their servers.
			Scopes: []string{"identify", "guilds.members.read"},
			Endpoint: oauth2.Endpoint{
				AuthURL:   authURL,
				TokenURL:  tokenURL,
				AuthStyle: oauth2.AuthStyleInParams,
			},
		},
		apiBase: apiBase,
		guildID: cfg.GuildID,
	}
}

// AuthCodeURL returns the Discord consent page URL. state must come back
// unchanged on the callback.
func (d *Discord) AuthCodeURL(state string) string {
	// prompt=none skips the consent screen for users who already approved.
	return d.oauth.AuthCodeURL(state, oauth2.SetAuthURLParam("prompt", "none"))
}

// Identify exchanges the callback's code for a token, then returns the
// user's identity. It returns ErrNotMember if they aren't in the server.
func (d *Discord) Identify(ctx context.Context, code string) (Identity, error) {
	ctx, cancel := context.WithTimeout(ctx, identifyTimeout)
	defer cancel()
	tok, err := d.oauth.Exchange(ctx, code)
	if err != nil {
		return Identity{}, fmt.Errorf("exchange code: %w", err)
	}
	client := d.oauth.Client(ctx, tok)

	var id Identity
	status, err := d.getJSON(ctx, client, "/users/@me", &id)
	if err != nil {
		return Identity{}, err
	}
	if status != http.StatusOK || id.ID == "" {
		return Identity{}, fmt.Errorf("get user: status %d", status)
	}

	// 200 with a member object if they're in the server, 404 if not.
	status, err = d.getJSON(ctx, client, "/users/@me/guilds/"+url.PathEscape(d.guildID)+"/member", nil)
	if err != nil {
		return Identity{}, err
	}
	switch status {
	case http.StatusOK:
		return id, nil
	case http.StatusNotFound:
		return Identity{}, ErrNotMember
	default:
		return Identity{}, fmt.Errorf("get server membership: status %d", status)
	}
}

// getJSON GETs an API path and, on a 200, decodes the body into out (if not
// nil). It returns the status code.
func (d *Discord) getJSON(ctx context.Context, client *http.Client, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.apiBase+path, nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("get %s: %w", path, err)
	}
	defer resp.Body.Close()
	body := io.LimitReader(resp.Body, maxDiscordBody)
	if resp.StatusCode != http.StatusOK || out == nil {
		_, _ = io.Copy(io.Discard, body)
		return resp.StatusCode, nil
	}
	if err := json.NewDecoder(body).Decode(out); err != nil {
		return resp.StatusCode, fmt.Errorf("decode %s: %w", path, err)
	}
	return resp.StatusCode, nil
}
