# LFI Core — Relay 0.3.0

**Status:** substantially implemented foundation; release-wide verification remains incomplete. Do not label this production-certified until the Go 1.24.2 full suite, OS builds and real-provider/browser acceptance tests pass.  
**Architecture:** local-first; no Relay cloud broker. One Relay-owned OAuth Broker is shared by connected MCP servers and Builder-generated stdio connectors.

## Repository audit and architecture

The accepted architecture decision record is [ADR-0001](ADR-0001-LFI-CORE.md); normative reference: [MCP Authorization 2025-06-18](https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization). The transport target is MCP `2025-06-18` with `2025-03-26` compatibility; the authorization profile is MCP Authorization `2025-06-18`. Later revisions are not claimed.

The supplied 0.2.x source already had PKCE, callback, discovery, refresh, Streamable HTTP/stdio and an agent pause/resume path. Relay 0.3.0 extends those packages instead of creating a duplicate identity subsystem:

- `internal/oauth`: PKCE S256, discovery, authorization lifecycle, AES-GCM vault, Linux Secret Service adapter/fallback, refresh, revocation, provider profiles, callback and redacted connection metadata.
- `internal/mcp`: HTTP/stdio transport, structured 401 handling, resource binding, session lifecycle, reconnect and shared Broker integration.
- `internal/agent`: separate OAuth-start approval before the browser flow; safe single retry after authentication.
- `internal/builder`: shared token-file contract, resource-bound SDK HTTP helper and source validation against raw credential access/custom-OAuth patterns.
- `internal/server`: Connections API, provider registry API and Connections page.
- `internal/tui`: `/connections` shortcut and OAuth consent prompt through the existing approval overlay.

## Implemented in this iteration

1. AES-256-GCM encryption of OAuth records. Linux Secret Service (`secret-tool`) stores the master key when available; a 0600/0700 Unix local-key fallback is used otherwise (other OSes rely on user-profile ACLs). Legacy plaintext records migrate on first load. The Builder receives only the access-token handoff file plus its resource binding; generated code must use the SDK HTTP helper, which validates/pins DNS, enforces origin/path scope and refuses redirects.
2. Explicit lifecycle states and validated transitions for consent, browser authorization, callback, token exchange, authentication, decline, cancellation, expiry and failure. Callback state is single-use and cancellation/expiry prevents late completion.
3. Separate OAuth-start approval before client registration/browser opening. The approval description includes the discovered/configured issuer, resource, explicitly requested scopes and separately identified provider-supported scopes. Supported scopes are never automatically requested. Decline cancels the flow.
4. PKCE S256 is mandatory: discovered authorization servers must advertise S256; custom static providers require an explicit `pkce_s256_supported` declaration; built-in profiles declare documented support.
5. Resource/audience binding: credentials are returned only for the stored resource identity and exact MCP target URL; discovery verifies that the resource URI covers the target endpoint. MCP server ID reuse does not silently forward the token to another target.
6. OAuth endpoint DNS checks in a direct resolver-aware HTTP dialer; private, loopback, link-local and multicast DNS answers are rejected for remote endpoints. Redirects are not followed.
7. Provider profiles for GitHub, Google and Microsoft with provider-documented public authorization/token endpoints and optional userinfo; Google also has its documented revocation endpoint. Static provider profiles require user app registration; RFC 7591 DCR is used only when the authorization server advertises a registration endpoint.
8. Connections metadata/API and `/ui/connections`: status, issuer, resource, scopes, expiry, account label when userinfo is available, last-used time, sign-in, reconnect, disconnect, revoke and forget.
9. Builder source validation rejects common hard-coded token formats, inline token/client-secret assignments, raw token-file/token-helper access and obvious custom OAuth authorization-code flows. Runtime stdio auth markers are accepted only for servers with an explicit OAuth block.
10. Fixed a manager startup race: unapproved stdio processes are no longer briefly marked connecting/launched by a Reload racing with Trust.
11. Added OAuth vault, lifecycle, resource-binding, provider-profile, userinfo and Builder-contract tests. Updated README, PROJECT, protocol, OAuth and transport docs.

## Provider coverage and limitations

The registry deliberately advertises only GitHub, Google and Microsoft. Client IDs/secrets are user-supplied; profiles do not imply provider registration or approval. Slack and Notion are not advertised because their provider-specific OAuth contracts have not been implemented and tested against real provider environments. Custom providers may be configured explicitly if they implement the supported authorization-code + PKCE S256 and token response contract.

## Security decisions

| Area | Decision | Limitation |
|---|---|---|
| PKCE/state | S256, cryptographic random state, one-time callback | Real provider/browser testing still required |
| Consent | Separate OAuth-start approval before registration/browser | Metadata discovery itself is read-only and occurs before consent to show scopes |
| Vault | AES-256-GCM records; Linux Secret Service if available; private key-file fallback (0600/0700 on Unix, platform ACL elsewhere) | Native macOS Keychain and Windows Credential Manager adapters are not implemented; local-file key does not protect against same-user malware |
| Builder token handoff | Access-token-only file with resource binding, 0600 on Unix; SDK restricts destination and pins DNS | Connector process can read its own access token by design |
| Resource binding | The resource URI must cover the MCP endpoint; the exact MCP target URL is separately bound | Providers with unusual audience aliases need explicit matching resource configuration |
| SSRF | OAuth HTTP direct dialer validates all resolved IPs; Builder SDK pins validated DNS answers to close the rebinding window; redirects disabled | OAuth endpoints and Builder API requests are validated; Builder SDK pins its resolved API IP. The configured MCP server endpoint remains user-selected and redirects are refused |
| Revocation | RFC 7009 where metadata/profile advertises endpoint; local token removed regardless of remote result | Provider-specific non-RFC revocation APIs are not implemented |
| Identity label | Optional HTTPS userinfo endpoint; only returned claims used | No unverified ID-token decoding; account label may be absent |
| HTTP MCP | Streamable HTTP POST JSON/SSE, session/version headers, 401 challenge | Legacy HTTP+SSE and client-initiated GET SSE not supported |
| stdio MCP | Explicit Builder SDK auth marker only | Arbitrary stdio text is not inferred as OAuth |
| Retry | One retry only for structured pre-execution credential miss or explicit HTTP 401 marked safe; ambiguous network failures are never replayed | Ambiguous network failures are never replayed automatically |

## Connections endpoints

- `GET /api/connections`
- `DELETE /api/connections/{id}` (local disconnect)
- `POST /api/connections/{id}/revoke` (provider revoke if available, then local token deletion)
- `DELETE /api/connections/{id}/forget` (delete complete local record)
- `GET /api/oauth/providers`
- `GET /api/mcp/{id}/authorization` (prepares the provider/resource/scope consent description)
- `POST /api/mcp/{id}/authorize` (requires the prepared consent description)
- `/ui/connections` and TUI `/connections`

The API returns only redacted metadata; access tokens, refresh tokens and client secrets are never returned.

## Remaining release gates

- Native macOS Keychain and Windows Credential Manager adapters; key rotation/migration tests and recovery UX.
- Provider-specific integration tests for every advertised profile, including refresh/revocation/account identity; real GitHub, Google and Microsoft app registrations.
- Slack and Notion profiles if their PKCE, token-auth and response contracts can be supported safely.
- Full RFC 9728/8414 edge-case fixtures, CIMD support if required, issuer response parameter policy and formal MCP authorization conformance.
- Adversarial DNS rebinding, callback race, concurrent refresh/revoke, multiple accounts and multi-resource tests.
- Browser-based end-to-end acceptance and cross-platform builds/tests.
- Full `go test ./...`, `go vet ./...`, static analysis and release artifact builds under Go 1.24.2.

## Verification in this environment

Go 1.24.2 is required by `go.mod`; only Go 1.23.2 is installed and the environment cannot download toolchains or Go modules. The OAuth package was copied into an isolated Go 1.23 test module and its unit/integration tests passed with `-race`. The MCP and OAuth packages also passed isolated `-race` tests after the manager race fix. The complete repository test suite, `go vet`, cross-platform builds and real-provider/browser acceptance tests could not be run. Python SDK tests and static formatting are checked separately. These results are not a substitute for the full release gate.
