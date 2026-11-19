package oauth

import (
	"errors"
	"sort"
	"strings"
)

// ProviderProfile contains provider-documented public endpoints and conservative defaults. Client IDs
// and secrets are always supplied by the user: Relay cannot register apps on the user's behalf
// with providers that require developer-console setup.
type ProviderProfile struct {
	ID                string            `json:"id"`
	PKCES256Supported bool              `json:"pkce_s256_supported"`
	Issuer            string            `json:"issuer,omitempty"`
	Name              string            `json:"name"`
	AuthorizeURL      string            `json:"authorize_url"`
	TokenURL          string            `json:"token_url"`
	RevocationURL     string            `json:"revocation_url,omitempty"`
	UserInfoURL       string            `json:"userinfo_url,omitempty"`
	TokenAuthMethod   string            `json:"token_endpoint_auth_method,omitempty"`
	DefaultScopes     []string          `json:"default_scopes,omitempty"`
	DefaultParams     map[string]string `json:"default_params,omitempty"`
	Registration      string            `json:"registration"`
	Notes             string            `json:"notes"`
}

var providerProfiles = map[string]ProviderProfile{
	"github": {
		ID: "github", PKCES256Supported: true, Name: "GitHub", AuthorizeURL: "https://github.com/login/oauth/authorize",
		TokenURL: "https://github.com/login/oauth/access_token", TokenAuthMethod: "client_secret_post", UserInfoURL: "https://api.github.com/user", Registration: "manual",
		Notes: "Create an OAuth App or GitHub App and configure Relay's exact loopback callback URL. PKCE S256 is supported. Without scopes the token only reads your profile: most GitHub MCP tools need some, for example repo (repositories, issues, pull requests), read:org (organizations and teams), read:user and notifications. Enter them in the Scopes field and sign in again.",
	},
	"google": {
		ID: "google", PKCES256Supported: true, Issuer: "https://accounts.google.com", Name: "Google", AuthorizeURL: "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL: "https://oauth2.googleapis.com/token", TokenAuthMethod: "client_secret_post", RevocationURL: "https://oauth2.googleapis.com/revoke", UserInfoURL: "https://openidconnect.googleapis.com/v1/userinfo",
		DefaultParams: map[string]string{"access_type": "offline"}, Registration: "manual",
		Notes: "Create an OAuth client in Google Cloud Console and register Relay's exact loopback callback URL. Enable the APIs and specify their least-privilege scopes.",
	},
	"microsoft": {
		ID: "microsoft", PKCES256Supported: true, Name: "Microsoft identity platform", AuthorizeURL: "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
		TokenURL: "https://login.microsoftonline.com/common/oauth2/v2.0/token", TokenAuthMethod: "none", UserInfoURL: "https://graph.microsoft.com/oidc/userinfo", Registration: "manual",
		Notes: "Create an app registration in Microsoft Entra ID and register Relay's loopback callback URL as a mobile/desktop or web redirect as appropriate. Configure the app as a public client for Relay's desktop flow; specify the exact resource scopes (for example Microsoft Graph) and offline_access when refresh tokens are needed. A confidential web app can override the token auth method and supply its secret.",
	},
}

// Providers returns profiles whose public provider documentation describes compatible authorization-code/PKCE endpoints. Real provider E2E tests remain a release gate.
func Providers() []ProviderProfile {
	out := make([]ProviderProfile, 0, len(providerProfiles))
	for _, p := range providerProfiles {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func Provider(id string) (ProviderProfile, bool) {
	p, ok := providerProfiles[strings.ToLower(strings.TrimSpace(id))]
	return p, ok
}

// ApplyProvider fills only missing endpoint/default fields; explicit user configuration wins.
func ApplyProvider(c Config, id string) (Config, error) {
	p, ok := Provider(id)
	if !ok {
		return c, errors.New("unknown OAuth provider profile " + id)
	}
	c.Provider = p.ID
	c.PKCES256Supported = p.PKCES256Supported
	if c.Issuer == "" {
		c.Issuer = p.Issuer
	}
	if c.AuthorizeURL == "" {
		c.AuthorizeURL = p.AuthorizeURL
	}
	if c.TokenURL == "" {
		c.TokenURL = p.TokenURL
	}
	if c.RevocationURL == "" {
		c.RevocationURL = p.RevocationURL
	}
	if c.UserInfoURL == "" {
		c.UserInfoURL = p.UserInfoURL
	}
	if c.TokenAuthMethod == "" {
		c.TokenAuthMethod = p.TokenAuthMethod
	}
	if len(c.Scopes) == 0 {
		c.Scopes = append([]string(nil), p.DefaultScopes...)
	}
	if c.Params == nil {
		c.Params = map[string]string{}
	}
	for k, v := range p.DefaultParams {
		if _, exists := c.Params[k]; !exists {
			c.Params[k] = v
		}
	}
	return c, nil
}
