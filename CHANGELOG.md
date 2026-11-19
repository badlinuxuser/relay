# Changelog

## 0.3.0 — LFI Core implementation

- Added AES-256-GCM encryption for OAuth records, Linux Secret Service key storage when available, and a private local-key fallback with transparent migration of legacy records.
- Added explicit, validated OAuth lifecycle states; one-time callback consumption; separate OAuth-start approval before client registration/browser launch; cancellation and expiry handling. Remote discovery must advertise PKCE S256; custom static providers require an explicit PKCE-S256 declaration, and built-in profiles document support.
- Bound stored credentials to their resource indicator before attaching bearer tokens to MCP HTTP requests.
- Added resolver-aware direct OAuth endpoint dialing, private/non-routable DNS answer rejection and redirect refusal.
- Builder SDK migration: connectors must use `http(..., oauth=True)` and declare `oauth.resource`; the legacy raw-token helper no longer returns token material.
- Added the Builder SDK resource-bound HTTP client: OAuth requests are restricted to the configured resource origin/path, DNS answers are validated and pinned, redirects are refused, and generated source cannot read or manually forward raw tokens.
- Added built-in endpoint profiles for GitHub, Google and Microsoft; token endpoint authentication (`client_secret_basic`, `client_secret_post`, `none`) with metadata/DCR negotiation; optional HTTPS userinfo account labels; provider revocation where an RFC 7009 endpoint is available.
- Distinguished provider-supported scopes from explicitly requested scopes (supported scopes are never automatically requested); HTTP MCP always sends canonical RFC 8707 `resource` in authorization and token requests per MCP 2025-06-18, while Builder stdio uses an explicit provider-specific opt-in.
- Added redacted Connections API and `/ui/connections` with provider/resource/scope consent, sign-in, reconnect, disconnect, revoke and forget actions.
- Added Builder source checks for common hard-coded credentials and custom OAuth implementations; constrained stdio auth-marker handling to explicitly OAuth-configured servers.
- Fixed a race where `Reload()` could start an unapproved stdio process while `Trust()` was being requested.
- Added tests for vault encryption/tampering/migration, lifecycle transitions, resource binding, provider profiles, userinfo, and MCP authorization behavior.
- Recorded the exact MCP `2025-06-18` authorization target and legacy `2025-03-26` transport compatibility in ADR-0001; later `2025-11-25`/`2026-07-28` revisions are not claimed.
- Updated README, PROJECT, protocol, extension, OAuth, transport and release documentation.

### Verification limitations

The execution environment provides Go 1.23.2 while `go.mod` requires Go 1.24.2, and cannot download Go modules/toolchains. The isolated OAuth and MCP/OAuth packages passed `go test -race` under Go 1.23.2. The complete repository test suite, `go vet`, cross-platform builds and real-provider/browser acceptance tests remain unverified. Native macOS Keychain and Windows Credential Manager adapters, key rotation, hosted project-wide CIMD document, GitHub device flow, and Slack/Notion provider-specific flows are not implemented.


## 0.3.0 (continued) — OAuth setup usability

- Allow OAuth MCP entries to load without a Client ID or DCR endpoint; surface `needs_setup` instead of rejecting the MCP configuration.
- Add Connections UI client-setup form and `POST /api/connections/{id}/client`.
- Store Client ID, optional Client Secret and token endpoint auth method in the encrypted OAuth vault; vault credentials override mcp.json and secrets are never returned.
- Add explicit `oauth.cimd_url` support and use it as `client_id` when CIMD is supported; retain DCR fallback.
- Add regression tests for late client setup and CIMD client selection.
- GitHub device authorization (RFC 8628) and a hosted shared CIMD document remain outstanding; GitHub still requires a registered OAuth client in this build.

### Usability fixes (still 0.3.0)

- The version is 0.3.0 again (`proto.Version`, README, docs).
- "Always allow this tool in this session" now works for MCP tools (per tool name) and for `validate_mcp_server`; `create_mcp_server`, skills, shell, file writes and `oauth_connect` are still asked every time. SECURITY.md updated.
- `/connections` is listed in the input-line command hints.
- Connections: a "Client & scopes" button for any connection and a Scopes field. Scopes are kept in the encrypted vault (`user_scopes`), replace `oauth.scopes` from mcp.json at the next sign-in, and can be changed without re-entering the Client ID. GitHub note now explains that a token without scopes only reads the profile.
- An HTTP 403 about scopes from an MCP server now tells the model not to retry and to ask the user to add scopes in /connections.
- Tests added: per-tool session approval, user scopes override, actionable scope error.

Not verified: no Go toolchain was available, so these changes were not compiled or tested; run `go build ./... && go test ./...` first.
