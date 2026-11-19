# ADR-0001: LFI Core and MCP authorization profile

- **Status:** Accepted for Relay 0.3.0
- **Date:** 2026-11-18
- **Decision owners:** Relay maintainers

## Context

Relay 0.2.x already had an OAuth broker, PKCE, callback handling, MCP HTTP/stdio transports and an agent pause/resume path. The 0.3.0 plan requires one local-first authorization lifecycle shared by connected MCP servers and Builder-generated stdio connectors, without exposing refresh tokens or client secrets to model context or generated source.

The repository currently negotiates MCP protocol `2025-06-18` and accepts `2025-03-26` for compatibility. It does not implement the newer `2025-11-25` or stateless `2026-07-28` protocol revisions.

## Decisions

### 1. Reuse existing packages

Keep the existing package boundaries:

- `internal/oauth` owns OAuth lifecycle, discovery, PKCE, vault, callback, refresh and revocation.
- `internal/mcp` owns transport challenges, resource/target binding and connection lifecycle.
- `internal/agent` owns the safe pause/consent/resume interaction.
- `internal/builder` owns the stdio credential handoff contract and generated-source validation.
- `internal/server` and `internal/tui` expose Connections and consent UX.

Do not add a second identity manager or a second token vault.

### 2. Protocol target

- MCP transport target: `2025-06-18`.
- Legacy compatibility: `2025-03-26`.
- HTTP OAuth behavior: MCP Authorization profile `2025-06-18`, based on OAuth 2.1 draft-13, RFC 8414, RFC 7591, RFC 9728 and RFC 8707.
- Later MCP protocol/auth revisions are not claimed as supported. In particular, CIMD, OIDC discovery fallback, scope-challenge step-up and the stateless `2026-07-28` transport are release gaps.
- Normative reference: [MCP Authorization 2025-06-18](https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization).

### 3. Resource indicator and credential binding

For HTTP MCP, Relay always sends the canonical RFC 8707 `resource` indicator in both authorization and token requests, even if the authorization server metadata does not advertise a capability flag. The value is the MCP protected-resource identifier. Relay separately stores and checks the exact MCP endpoint URL, so a token is never forwarded to another target merely because it shares an origin.

Protected-resource metadata may identify a base URI. Relay accepts it only when it has the same origin and covers the MCP endpoint on a path-segment boundary. Ambiguous encoded path traversal, encoded separators and double-encoding are rejected.

For Builder stdio OAuth, MCP's HTTP authorization protocol is not applied. The server must declare the upstream API `resource`; the bundled SDK attaches the access token only to URLs covered by that resource, validates and pins DNS answers, and refuses redirects. The SDK does not expose raw token material to generated code.

### 4. Discovery and client registration

- Use RFC 9728 Protected Resource Metadata and RFC 8414 Authorization Server Metadata for the targeted profile.
- Require an exact issuer match and advertised PKCE S256 support.
- Use RFC 7591 Dynamic Client Registration only when a registration endpoint is advertised.
- Otherwise require an existing provider registration and user-supplied client ID/credentials. Relay does not invent provider credentials.
- Do not implement Client ID Metadata Documents or OIDC Discovery fallback in this release; these belong to a later protocol upgrade.

### 5. Consent and callback

- `AuthorizationDescription` performs read-only discovery and prepares a short-lived, fingerprint-bound consent record.
- A separate user approval is required before registration or browser launch.
- Agent, TUI, CLI and Connections UI all use the prepare/approve/start sequence.
- The callback is a loopback GET endpoint. State is random, single-use and bound to one live session; optional/required `iss` is validated before code exchange.

### 6. Vault and platform support

- OAuth records use AES-256-GCM with per-record ID as authenticated additional data.
- Linux uses Secret Service through `secret-tool` when available; otherwise the master key is kept in a local file with Unix 0600/0700 permissions.
- macOS Keychain and Windows Credential Manager adapters are not implemented in 0.3.0. Other platforms rely on user-profile ACLs for the local key-file fallback.
- The vault does not protect against malware running as the same OS user.

### 7. Retry semantics

The original tool operation is retried once only for a structured credential miss before execution or an explicit HTTP 401 rejection marked safe by the transport/SDK. Ambiguous network failures and arbitrary stdio error text are never translated into a retryable authentication state. A second unauthorized result is returned to the model without another prompt.

### 8. Provider profiles

Built-in profiles are limited to GitHub, Google and Microsoft. They define documented endpoints and token endpoint authentication methods but do not imply provider app registration or approval. Slack and Notion are not advertised until their PKCE S256 and token response contracts have dedicated implementation and real-provider tests.

## Consequences

- The current implementation is a deliberately scoped MCP `2025-06-18` client, not a claim of latest MCP `2026-07-28` conformance.
- Providers that do not support PKCE S256 or the supported token exchange contract are rejected rather than silently downgraded.
- Some providers require manual client registration and configuration.
- The full release gate still requires native macOS/Windows credential stores, provider/browser end-to-end tests, adversarial security tests and the complete Go 1.24.2 test suite.
