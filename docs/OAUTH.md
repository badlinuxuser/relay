# OAuth in Relay 0.3.0 — LFI Core

## Security and user flow

1. An MCP tool call receives a structured HTTP `401` authentication challenge, or a Builder SDK tool explicitly reports that its Relay-managed token is missing/expired.
2. Relay reads the MCP server's OAuth configuration. For remote HTTP MCP, it performs protected-resource metadata discovery (RFC 9728) and authorization-server metadata discovery (RFC 8414) without following redirects.
3. Relay displays a separate OAuth-start approval. The prompt includes the provider/issuer, resource indicator and scopes reported by configuration or metadata. Declining cancels the authorization attempt. Approval of OAuth access does **not** approve the tool's business action.
4. Only after approval does Relay resolve the client in this order: saved client credentials from the encrypted local vault, an explicitly configured Client ID Metadata Document URL (CIMD, only when configured/advertised), RFC 7591 Dynamic Client Registration, then a user-supplied client from the Connections setup form. Relay generates PKCE S256 and a cryptographically random one-time state before preparing the authorization URL.
5. Relay opens the provider's official authorization page. The user authenticates there; Relay never asks for the provider password.
6. The loopback callback accepts only GET `/oauth/callback`, consumes the state once, validates `iss` before processing either success or denial, and exchanges the code with the PKCE verifier.
7. Relay stores the credential locally, reconnects the MCP server, and retries the interrupted operation once. HTTP 401 is treated as a pre-execution rejection. The Builder SDK emits its auth marker only for a missing/expired token or an HTTP 401 response from its OAuth-enabled request helper.

If the user declines, cancels, the session expires, the callback is replayed, or the token exchange fails, Relay returns a structured failure and does not retry the original operation. A second authorization failure is returned to the model; there is no authentication retry loop.

## Provider profiles

`mcp.json` may specify a provider profile to avoid copying well-known endpoints. Client IDs and secrets may be supplied in the Connections UI and are stored in the encrypted local OAuth vault; they do not need to be written to mcp.json. Providers that support CIMD or DCR can avoid manual client registration when a compatible public client document or registration endpoint is available. Relay does not own provider registrations and cannot create a client at providers that offer neither mechanism.

```json
{
  "servers": {
    "github": {
      "type": "http",
      "url": "https://mcp.example.com/mcp",
      "oauth": {
        "provider": "github",
        "resource": "https://mcp.example.com/mcp",
        "scopes": ["read:user"]
      }
    }
  }
}
```

The example intentionally omits `client_id` and `client_secret`: Relay loads the connector, shows `needs_setup` in Connections, and lets the user save the client credentials through the local UI.

Built-in endpoint profiles currently available:

| Profile | Authorization / token | Token client auth | Additional behavior | Registration |
|---|---|---|---|---|
| `github` | `github.com/login/oauth/authorize` / `github.com/login/oauth/access_token` | `client_secret_post` | PKCE S256; optional userinfo at `api.github.com/user` | User supplies an OAuth App client ID and secret through Connections; GitHub device flow is not implemented in this release |
| `google` | `accounts.google.com/o/oauth2/v2/auth` / `oauth2.googleapis.com/token` | `client_secret_post` | Offline access defaults; revocation endpoint; optional OIDC userinfo | User creates an OAuth client in Google Cloud |
| `microsoft` | Microsoft Entra `common` v2 authorize/token endpoints | `none` for a public desktop client | Optional Microsoft OIDC userinfo | User creates an Entra app registration; confidential apps may override method and supply a secret |

The Google profile preconfigures issuer `https://accounts.google.com`. GitHub's standard OAuth App flow does not advertise an issuer parameter. The Microsoft profile uses the `common` authority and does not hard-code a tenant issuer; if the response includes RFC 9207 `iss`, configure the exact tenant issuer and matching tenant-specific endpoints or Relay will reject the response.

Use `GET /api/oauth/providers` to retrieve the registry and registration notes. `token_endpoint_auth_method` accepts `client_secret_basic`, `client_secret_post` or `none`; dynamic registration uses the method advertised by authorization-server metadata and the method returned by the registration response. Profiles do not invent scopes: specify the least-privilege scopes required by the actual MCP resource. Protected-resource `scopes_supported` metadata is shown as provider-supported information but is **not** automatically requested; only explicit `oauth.scopes` values are sent in the authorization request. If no scopes are configured, the provider's own default-scope behavior applies and the consent prompt says so. Google and Microsoft userinfo labels are available only when the granted scopes permit the provider's userinfo endpoint. GitHub account labels may be unavailable when the user's email is private and the `name` field is empty.

Slack and Notion are not advertised as built-in profiles in this release. Their provider-specific response/authentication contracts need dedicated implementation and real-provider tests before Relay can claim support. A provider can still be used through explicit OAuth configuration if it conforms to the implemented authorization-code/PKCE and token-response contract. Custom static providers must include `"pkce_s256_supported": true` in their `oauth` block only after confirming S256 support in the provider's own documentation. Relay refuses custom static OAuth flows without that declaration. For discovered remote servers, authorization-server metadata must explicitly advertise `code_challenge_methods_supported: ["S256"]`; missing or weaker PKCE support is rejected.

## Discovery and validation

- Protected Resource Metadata is read from the `resource_metadata` challenge parameter or the standard well-known candidates.
- If Protected Resource Metadata is found, its required `resource` identifier must cover the MCP endpoint (same origin and a path-segment boundary). Legacy discovery without PRM falls back to the configured server URL.
- Authorization Server Metadata must include an issuer and it must match the discovered issuer. If an authorization response includes `iss`, it must match an expected issuer; Relay rejects it when no expected issuer is configured.
- Authorization and token endpoints must be absolute HTTPS URLs; HTTP is allowed only for loopback test/callback endpoints.
- URL user-info, fragments, opaque URLs, whitespace/control characters, private/non-routable HTTPS IP literals and redirects are rejected.
- OAuth discovery/token HTTP requests use a direct resolver-aware dialer. DNS answers containing private, loopback, link-local or multicast addresses are rejected (loopback is allowed only for loopback test endpoints).
- MCP HTTP redirects are not followed, so bearer tokens and custom headers are not forwarded to a different origin.
- The stored resource indicator and exact MCP target URL are checked before Relay attaches a bearer token. Reusing an MCP server ID for a different resource or target does not reuse its token. For HTTP MCP authorization, Relay always sends the canonical RFC 8707 `resource` indicator in both authorization and token requests, as required by the targeted MCP 2025-06-18 authorization profile, regardless of an authorization-server capability flag. For Builder stdio OAuth (which is not the MCP HTTP authorization flow), `send_resource: true` is an explicit provider-specific opt-in; local resource binding is enforced either way.
- State and authorization codes are single-use. A callback cannot complete a flow that has already been cancelled, expired or consumed.

## Token storage and threat model

OAuth records (access token, refresh token, client metadata and client secret) are serialized and encrypted with AES-256-GCM. On Linux, Relay uses Secret Service via `secret-tool` when it is installed and unlocked. If Secret Service is unavailable, Relay stores a random 256-bit master key in `<home>/auth/master.key` (0600 file and 0700 directory on Unix; other platforms rely on the user-profile ACL). Other operating systems currently use this encrypted-file fallback; native macOS Keychain and Windows Credential Manager adapters are not implemented in this release.

This protects the record file against accidental disclosure and copied ciphertext without the key. The local-file fallback does **not** protect against malware or another process running as the same OS user, because that process can read the key. Protect the Relay home directory and backups. The Builder process receives only `<id>.token`, a deliberately narrow owner-only access-token handoff file; it does not receive refresh tokens or client secrets. The handoff file is not encrypted because the SDK must read it directly.

Legacy plaintext `<id>.json` records are read and transparently encrypted on first load. Older HTTP records without the exact target-URL binding are shown as `reauthorization_required`; sign in once again so Relay can bind the token to the current MCP endpoint. Keep a backup before upgrading. If the master key is deleted or lost, encrypted credentials cannot be recovered; reconnect the affected providers.

## Client setup in Connections

OAuth connectors may now be loaded without `client_id` or `registration_url`. Relay reports `needs_setup` instead of rejecting the entire `mcp.json`. In `/ui/connections`, the user can enter a Client ID, optional Client Secret and token endpoint authentication method. `POST /api/connections/{id}/client` stores these values inside the encrypted OAuth record; the secret is write-only and is never returned by the Connections API. Values in the vault override values in `mcp.json`.

For dynamically discovered HTTP MCP servers, the same form can save a client for the discovered authorization server. The server's OAuth discovery still happens before consent and the stored client is applied to the discovered endpoints.

`cimd_url` is an explicit opt-in URL for a Client ID Metadata Document. Relay uses this URL as `client_id` only when the authorization server advertises `client_id_metadata_document_supported`; static provider configuration can explicitly opt in by setting `cimd_url`. The document must be hosted on a stable HTTPS origin and its `client_id` value must exactly equal its URL. Relay does not host a project-wide client document in this build because the project domain/ownership has not been selected.

## Connections API and UI

- `GET /api/connections` — redacted metadata only: server ID, issuer, resource, scopes, status, expiry, account label (when userinfo is available), last use, and the local callback URI.
- `POST /api/connections/{id}/client` — write-only setup endpoint for `client_id`, optional `client_secret` and `token_endpoint_auth_method`; values are stored in the encrypted vault and never echoed back.
- `GET /api/mcp/{id}/authorization` and `POST /api/mcp/{id}/authorize` — prepare the exact consent description, then start authorization only after the prepared description is approved.
- `DELETE /api/connections/{id}` — remove local tokens while retaining provider/client configuration.
- `POST /api/connections/{id}/revoke` — attempt RFC 7009 revocation when a revocation endpoint is configured, then remove the local token even if remote revocation fails. The response indicates whether remote revocation succeeded.
- `DELETE /api/connections/{id}/forget` — delete the entire local connection record and token handoff file.
- `/ui/connections` (TUI shortcut `/connections`) — inspect, approve/sign in, reconnect, disconnect, revoke and forget connections.

Provider revocation support is profile-dependent. A local disconnect is not proof that the provider has revoked its copy of a token; use the provider's account-security page when remote revocation is unavailable or fails.

## Builder contract

Generated MCP servers must declare OAuth metadata in the server configuration and use `http(..., oauth=True)` from the bundled Relay SDK. Relay injects the access-token file at the process boundary. Generated source must not contain hard-coded credentials or implement a parallel browser login/token vault. The Builder validator rejects common hard-coded token patterns, inline token/client-secret assignments, raw token-helper/token-file access and obvious custom authorization-code flows. These source checks are defensive heuristics, not a sandbox or proof that arbitrary generated code is benign.

Example:

```python
from relay_mcp import Server, ToolError, http

srv = Server("example")

@srv.tool("list_items", "List items", {})
def list_items():
    result = http("GET", "https://api.example.com/items", oauth=True)
    if isinstance(result, dict) and result.get("error"):
        raise ToolError(str(result["error"]))
    return result

srv.run()
```

Never put credentials in generated source, command-line arguments, logs, or model-visible tool output. Use environment references for non-OAuth secrets.

## Transport support

- **Streamable HTTP:** POST JSON-RPC, JSON or SSE POST responses, MCP protocol-version header, session ID lifecycle, 401 challenge parsing, OAuth resource discovery, bearer attachment and redirect rejection.
- **stdio:** explicit Builder OAuth declaration and Relay access-token file contract. Arbitrary stderr/stdout text is not interpreted as an OAuth challenge; only the SDK's marker is accepted, and only for a server with an OAuth block.
- **Not supported:** legacy HTTP+SSE and client-initiated GET SSE.

See [MCP_TRANSPORTS.md](MCP_TRANSPORTS.md) and [PROTOCOL.md](PROTOCOL.md).

## Troubleshooting

- **No client ID:** open `/connections`, follow the provider's registration instructions, copy the exact redirect URI, and save the Client ID (and secret only if required). Credentials are stored in the encrypted local vault. Alternatively configure a compatible `cimd_url` or use a discovered DCR endpoint.
- **No registration endpoint:** automatic registration is unavailable; manual app registration is required.
- **Unknown provider profile:** use one of `github`, `google`, or `microsoft`, or provide explicit endpoints.
- **PKCE unsupported:** Relay requires S256 and refuses an authorization server that explicitly advertises only other methods.
- **Resource mismatch:** verify `oauth.resource` and the MCP URL/resource metadata; Relay intentionally refuses cross-resource token reuse.
- **Sign-in expired/cancelled:** start the authorization again; browser sessions expire after five minutes.
- **Refresh refused:** sign in again. Network failures keep the existing refresh token so the user can retry later.
- **Connection still unauthorized:** confirm that the provider granted the scopes actually required by the MCP server and that the configured resource/audience is correct.


## Provider and protocol references

The endpoint profiles are based on the providers' published documentation; these references establish the documented endpoints and PKCE parameters, not a claim that Relay has completed live account-registration tests:

- GitHub OAuth Apps: [authorization code flow and PKCE](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps), [public-client guidance](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/best-practices-for-creating-an-oauth-app).
- Google: [installed-app OAuth and PKCE S256](https://developers.google.com/identity/protocols/oauth2/native-app), [authorization-code flow](https://developers.google.com/identity/oauth2/web/guides/how-user-authz-works).
- Microsoft: [authorization-code flow and PKCE](https://learn.microsoft.com/en-us/entra/identity-platform/v2-oauth2-auth-code-flow), [OIDC userinfo endpoint](https://learn.microsoft.com/en-us/entra/identity-platform/v2-protocols-oidc).
- Standards: [RFC 7636 (PKCE)](https://www.rfc-editor.org/rfc/rfc7636), [RFC 8414 (authorization-server metadata)](https://www.rfc-editor.org/rfc/rfc8414), [RFC 9728 (protected-resource metadata)](https://www.rfc-editor.org/rfc/rfc9728), [RFC 8707 (resource indicators)](https://www.rfc-editor.org/rfc/rfc8707), [RFC 7009 (token revocation)](https://www.rfc-editor.org/rfc/rfc7009).
