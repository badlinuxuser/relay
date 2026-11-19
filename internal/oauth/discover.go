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

// resourceMatchesTarget accepts a protected-resource base URI for an MCP endpoint below
// it, but never accepts another origin or a sibling path (for example /api vs /apix).
func ResourceMatchesTarget(resource, target string) bool {
	if checkURL(resource, "resource indicator") != nil {
		return false
	}
	r, err := url.Parse(resource)
	if err != nil {
		return false
	}
	t, err := url.Parse(target)
	if err != nil {
		return false
	}
	if r.Scheme != t.Scheme || !strings.EqualFold(r.Host, t.Host) || r.User != nil || r.Fragment != "" {
		return false
	}
	decodePath := func(u *url.URL) (string, bool) {
		escaped := strings.ToLower(u.EscapedPath())
		if strings.Contains(escaped, "%2f") || strings.Contains(escaped, "%5c") || strings.Contains(escaped, "%25") {
			return "", false
		}
		decoded, err := url.PathUnescape(u.EscapedPath())
		if err != nil || strings.Contains(decoded, "\\") {
			return "", false
		}
		for _, part := range strings.Split(decoded, "/") {
			if part == "." || part == ".." {
				return "", false
			}
		}
		return decoded, true
	}
	rp, rok := decodePath(r)
	tp, tok := decodePath(t)
	if !rok || !tok {
		return false
	}
	rp, tp = strings.TrimRight(rp, "/"), strings.TrimRight(tp, "/")
	if rp != "" && tp != rp && !strings.HasPrefix(tp, rp+"/") {
		return false
	}
	if r.RawQuery != "" && r.RawQuery != t.RawQuery {
		return false
	}
	return true
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
	if err := checkURL(serverURL, "protected resource"); err != nil {
		return nil, err
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
		var got struct {
			Resource string   `json:"resource"`
			Servers  []string `json:"authorization_servers"`
			Scopes   []string `json:"scopes_supported"`
		}
		if getJSON(ctx, hc, c, &got) == nil && len(got.Servers) > 0 {
			if got.Resource == "" {
				return nil, errors.New("protected-resource metadata omitted its required resource identifier")
			}
			if !ResourceMatchesTarget(got.Resource, serverURL) {
				return nil, fmt.Errorf("protected-resource metadata identifies %q, which does not cover the requested MCP endpoint %q", got.Resource, serverURL)
			}
			prm.Resource, prm.Scopes = got.Resource, got.Scopes
			issuer = got.Servers[0]
			break
		}
	}
	if err := checkURL(issuer, "authorization server"); err != nil {
		return nil, err
	}
	issuerURL, _ := url.Parse(issuer)
	if issuerURL.RawQuery != "" {
		return nil, errors.New("authorization-server issuer must not contain a query string")
	}
	var meta struct {
		Issuer                     string   `json:"issuer"`
		Authorize                  string   `json:"authorization_endpoint"`
		Token                      string   `json:"token_endpoint"`
		Registration               string   `json:"registration_endpoint"`
		CIMDSupported              bool     `json:"client_id_metadata_document_supported"`
		Revocation                 string   `json:"revocation_endpoint"`
		UserInfo                   string   `json:"userinfo_endpoint"`
		ResourceParameterSupported bool     `json:"resource_parameter_supported"`
		IssuerParameterSupported   bool     `json:"authorization_response_iss_parameter_supported"`
		TokenAuthMethods           []string `json:"token_endpoint_auth_methods_supported"`
		Methods                    []string `json:"code_challenge_methods_supported"`
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
	if meta.Issuer != "" {
		want, _ := url.Parse(issuer)
		got, _ := url.Parse(meta.Issuer)
		if want == nil || got == nil || want.String() != got.String() {
			return nil, fmt.Errorf("authorization-server metadata issuer %q does not match discovered issuer %q", meta.Issuer, issuer)
		}
	}
	if meta.Authorize == "" || meta.Token == "" {
		return nil, fmt.Errorf("the sign-in settings of %s have no authorization or token address", issuer)
	}
	ok := false
	for _, m := range meta.Methods {
		ok = ok || m == "S256"
	}
	if !ok {
		return nil, errors.New("the authorization server does not advertise PKCE S256, which Relay requires")
	}
	for what, a := range map[string]string{"authorization_endpoint": meta.Authorize, "token_endpoint": meta.Token} {
		if err := checkURL(a, what); err != nil {
			return nil, err
		}
	}
	if prm.Resource == "" {
		prm.Resource = serverURL
	}
	cfg := &Config{Issuer: issuer, PKCES256Supported: true, AuthorizeURL: meta.Authorize, TokenURL: meta.Token, SupportedScopes: prm.Scopes, Resource: prm.Resource, TargetURL: serverURL}
	if cfg.Resource == "" {
		cfg.Resource = serverURL
	}
	if meta.Registration != "" && checkURL(meta.Registration, "registration_endpoint") == nil {
		cfg.Registration = meta.Registration
	}
	if meta.Revocation != "" && checkURL(meta.Revocation, "revocation_endpoint") == nil {
		cfg.RevocationURL = meta.Revocation
	}
	if meta.UserInfo != "" && checkURL(meta.UserInfo, "userinfo_endpoint") == nil {
		cfg.UserInfoURL = meta.UserInfo
	}
	cfg.CIMDSupported = meta.CIMDSupported
	cfg.SendResource = true // MCP HTTP authorization requires resource in both authorize and token requests
	cfg.RequireIssuer = meta.IssuerParameterSupported
	if cfg.TokenAuthMethod == "" && len(meta.TokenAuthMethods) > 0 {
		for _, preferred := range []string{"none", "client_secret_basic", "client_secret_post"} {
			for _, supported := range meta.TokenAuthMethods {
				if supported == preferred {
					cfg.TokenAuthMethod = preferred
					break
				}
			}
			if cfg.TokenAuthMethod != "" {
				break
			}
		}
		if cfg.TokenAuthMethod == "" {
			return nil, errors.New("authorization server advertises no supported token endpoint authentication method")
		}
	}
	return cfg, nil
}

// Register creates an OAuth client at the service (dynamic client registration, RFC 7591).
func Register(ctx context.Context, hc *http.Client, endpoint, redirect, name string) (id, secret string, err error) {
	id, secret, _, err = RegisterClient(ctx, hc, endpoint, redirect, name, "none")
	return
}

// RegisterClient requests the selected endpoint authentication method and preserves the
// method returned by the authorization server. Public-client registration defaults to none.
func RegisterClient(ctx context.Context, hc *http.Client, endpoint, redirect, name, requestedMethod string) (id, secret, authMethod string, err error) {
	if hc == nil {
		hc = newHTTP()
	}
	if err := checkURL(endpoint, "registration_endpoint"); err != nil {
		return "", "", "", err
	}
	if err := checkURL(redirect, "redirect_uri"); err != nil {
		return "", "", "", err
	}
	if requestedMethod == "" {
		requestedMethod = "none"
	}
	if requestedMethod != "none" && requestedMethod != "client_secret_basic" && requestedMethod != "client_secret_post" {
		return "", "", "", fmt.Errorf("unsupported requested token auth method %q", requestedMethod)
	}
	body, _ := json.Marshal(map[string]any{
		"client_name": name, "redirect_uris": []string{redirect},
		"grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"},
		"token_endpoint_auth_method": requestedMethod,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("registration failed: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		ClientID        string `json:"client_id"`
		Secret          string `json:"client_secret"`
		TokenAuthMethod string `json:"token_endpoint_auth_method"`
		Error           string `json:"error"`
		Desc            string `json:"error_description"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if out.Error != "" {
		return "", "", "", &Error{Code: out.Error, Desc: out.Desc}
	}
	if resp.StatusCode >= 300 || out.ClientID == "" {
		return "", "", "", fmt.Errorf("registration failed (HTTP %d)", resp.StatusCode)
	}
	method := out.TokenAuthMethod
	if method == "" {
		method = requestedMethod
		// RFC 7591's default for a client that receives a secret is client_secret_basic.
		if method == "none" && out.Secret != "" {
			method = "client_secret_basic"
		}
	}
	if method != "client_secret_basic" && method != "client_secret_post" && method != "none" {
		return "", "", "", fmt.Errorf("registration returned unsupported token auth method %q", method)
	}
	if (method == "client_secret_basic" || method == "client_secret_post") && out.Secret == "" {
		return "", "", "", fmt.Errorf("registration selected %s but returned no client_secret", method)
	}
	return out.ClientID, out.Secret, method, nil
}
