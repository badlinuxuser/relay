package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func getJSON(ctx context.Context, hc *http.Client, addr string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, addr)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func originOf(u *url.URL) string { return u.Scheme + "://" + u.Host }

// asMetadataURLs lists where the metadata of an authorization server can be (RFC 8414 and
// OpenID Connect discovery).
func asMetadataURLs(issuer string) []string {
	u, err := url.Parse(issuer)
	if err != nil {
		return nil
	}
	origin, path := originOf(u), strings.TrimRight(u.Path, "/")
	if path == "" {
		return []string{origin + "/.well-known/oauth-authorization-server", origin + "/.well-known/openid-configuration"}
	}
	return []string{
		origin + "/.well-known/oauth-authorization-server" + path,
		origin + "/.well-known/openid-configuration" + path,
		strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration",
	}
}

// Discover finds how to sign in to a remote MCP server (the MCP authorization specification):
// the protected resource metadata names the authorization server, whose metadata names the
// endpoints. metaURL is the resource_metadata address from the 401 answer (may be empty).
func Discover(ctx context.Context, hc *http.Client, serverURL, metaURL string) (*Config, error) {
	if hc == nil {
		hc = newHTTP()
	}
	su, err := url.Parse(serverURL)
	if err != nil || su.Host == "" {
		return nil, fmt.Errorf("bad server address %q", serverURL)
	}
	var prm struct {
		Resource string   `json:"resource"`
		Servers  []string `json:"authorization_servers"`
		Scopes   []string `json:"scopes_supported"`
	}
	var cands []string
	if metaURL != "" {
		cands = append(cands, metaURL)
	} else {
		if p := strings.TrimRight(su.Path, "/"); p != "" {
			cands = append(cands, originOf(su)+"/.well-known/oauth-protected-resource"+p)
		}
		cands = append(cands, originOf(su)+"/.well-known/oauth-protected-resource")
	}
	issuer := originOf(su) // servers of the older specification are their own authorization server
	for _, c := range cands {
		if checkURL(c, "resource metadata") != nil {
			continue
		}
		var got = prm
		if getJSON(ctx, hc, c, &got) == nil && len(got.Servers) > 0 {
			prm, issuer = got, got.Servers[0]
			break
		}
	}
	if err := checkURL(issuer, "authorization server"); err != nil {
		return nil, err
	}
	var meta struct {
		Authorize    string   `json:"authorization_endpoint"`
		Token        string   `json:"token_endpoint"`
		Registration string   `json:"registration_endpoint"`
		Methods      []string `json:"code_challenge_methods_supported"`
	}
	var lastErr error = errors.New("no metadata address")
	found := false
	for _, c := range asMetadataURLs(issuer) {
		if err := getJSON(ctx, hc, c, &meta); err != nil {
			lastErr = err
			continue
		}
		found = true
		break
	}
	if !found {
		return nil, fmt.Errorf("could not read the sign-in settings of %s: %v", issuer, lastErr)
	}
	if meta.Authorize == "" || meta.Token == "" {
		return nil, fmt.Errorf("the sign-in settings of %s have no authorization or token address", issuer)
	}
	if len(meta.Methods) > 0 {
		ok := false
		for _, m := range meta.Methods {
			ok = ok || m == "S256"
		}
		if !ok {
			return nil, errors.New("the service does not support PKCE (S256), which Relay requires")
		}
	}
	for what, a := range map[string]string{"authorization_endpoint": meta.Authorize, "token_endpoint": meta.Token} {
		if err := checkURL(a, what); err != nil {
			return nil, err
		}
	}
	cfg := &Config{AuthorizeURL: meta.Authorize, TokenURL: meta.Token, Scopes: prm.Scopes, Resource: prm.Resource}
	if cfg.Resource == "" {
		cfg.Resource = serverURL
	}
	if meta.Registration != "" && checkURL(meta.Registration, "registration_endpoint") == nil {
		cfg.Registration = meta.Registration
	}
	return cfg, nil
}

// Register creates an OAuth client at the service (dynamic client registration, RFC 7591).
func Register(ctx context.Context, hc *http.Client, endpoint, redirect, name string) (id, secret string, err error) {
	if hc == nil {
		hc = newHTTP()
	}
	body, _ := json.Marshal(map[string]any{
		"client_name": name, "redirect_uris": []string{redirect},
		"grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("registration failed: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		ClientID string `json:"client_id"`
		Secret   string `json:"client_secret"`
		Error    string `json:"error"`
		Desc     string `json:"error_description"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if out.Error != "" {
		return "", "", &Error{Code: out.Error, Desc: out.Desc}
	}
	if resp.StatusCode >= 300 || out.ClientID == "" {
		return "", "", fmt.Errorf("registration failed (HTTP %d)", resp.StatusCode)
	}
	return out.ClientID, out.Secret, nil
}
