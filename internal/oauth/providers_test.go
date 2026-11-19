package oauth

import "testing"

func TestProviderProfilesFillOnlyMissingFields(t *testing.T) {
	c, err := ApplyProvider(Config{AuthorizeURL: "https://custom.example/auth", Scopes: []string{"files.read"}, Params: map[string]string{"access_type": "online"}}, "google")
	if err != nil {
		t.Fatal(err)
	}
	if !c.PKCES256Supported || c.Issuer != "https://accounts.google.com" || c.TokenAuthMethod != "client_secret_post" || c.AuthorizeURL != "https://custom.example/auth" || c.TokenURL != "https://oauth2.googleapis.com/token" {
		t.Fatalf("profile merge: %#v", c)
	}
	if len(c.Scopes) != 1 || c.Scopes[0] != "files.read" || c.Params["access_type"] != "online" || c.Params["include_granted_scopes"] != "" {
		t.Fatalf("defaults not merged safely: %#v", c)
	}
}

func TestProviderRegistryIsConservative(t *testing.T) {
	for _, id := range []string{"github", "google", "microsoft"} {
		if _, ok := Provider(id); !ok {
			t.Fatalf("missing documented provider profile %s", id)
		}
	}
	if _, ok := Provider("slack"); ok {
		t.Fatal("provider without a verified PKCE S256 contract must not be advertised")
	}
}

func TestProviderDefaultsMergeWithCustomAuthorizeParams(t *testing.T) {
	c, err := ApplyProvider(Config{Params: map[string]string{"prompt": "consent"}}, "google")
	if err != nil {
		t.Fatal(err)
	}
	if c.Params["access_type"] != "offline" || c.Params["prompt"] != "consent" {
		t.Fatalf("params=%v", c.Params)
	}
}
