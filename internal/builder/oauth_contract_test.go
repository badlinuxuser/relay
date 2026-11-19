package builder

import "testing"

func TestValidateOAuthContract(t *testing.T) {
	cases := []struct {
		name, code string
		wantErr    bool
	}{
		{"shared broker", `from relay_mcp import http\nhttp("GET", "https://api.example/me", oauth=True)`, false},
		{"hardcoded GitHub token", `TOKEN = "gho_123456789012345678901234567890"`, true},
		{"inline client secret", `client_secret = "very-long-hardcoded-secret-value"`, true},
		{"JSON access token literal", `data = {"access_token": "very-long-secret-token-value"}`, true},
		{"custom OAuth exchange", `data = {"grant_type": "authorization_code"}`, true},
		{"raw token logging", `print(oauth_token())`, true},
		{"raw token handling", `headers = {"Authorization": "Bearer " + oauth_token()}`, true},
		{"raw token return", `return oauth_token()`, true},
		{"token file access", `open(os.environ["RELAY_OAUTH_TOKEN_FILE"])`, true},
		{"forged auth marker", `raise ToolError("relay:auth-required")`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOAuthContract(tc.code, nil)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
