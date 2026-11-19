# Relay backend protocol

Transport: HTTP/1.1 + JSON; streaming answers use Server-Sent Events (`data: <json>\n\n`).
Address and token: `<home>/daemon.json` (`{"addr":"127.0.0.1:8787","token":"...","pid":123}`).
Every request to `/api/*` and `/v1/*` (except `/api/health`) needs the header `Authorization: Bearer <token>`.
`<home>` is `$RELAY_HOME`, otherwise `<UserConfigDir>/relay`.

Errors: a 4xx/5xx status and the body `{"error":{"message":"..."}}`.

## MCP protocol target

Relay's MCP client targets protocol `2025-06-18` and accepts `2025-03-26` for legacy compatibility. It does not claim `2025-11-25` or `2026-07-28` protocol conformance. The HTTP OAuth authorization profile is MCP Authorization `2025-06-18`; see [ADR-0001](ADR-0001-LFI-CORE.md).

## Endpoints

| Method and path | Description |
|---|---|
| `GET /api/health` | no authorization: `{"ok":true,"version":"0.3.0"}`; a client restarts a backend of another version |
| `GET /api/status` | modes (`effective`: the chain that is really used), models, providers (number of keys and how many are usable), processes, local servers that answer now (`servers`), usage, presets, MCP (`mcp`: names of connected servers; `mcp_servers`: the servers without their tools; `mcp_error`), `skills` (installed, with `enabled` and `builtin`), and the user's `settings` |
| `GET /api/models` | every model to choose from: the ones the providers report (`ollama/…`, `ollama-cloud/…`, `gemini/…`), official providers first; a provider without access adds nothing (models from the config show only with `discover = "none"`); `providers` has the number found, `local`, `skipped` ("no key") and `error`. `?refresh=1` asks the providers again; adding or deleting a key clears the 60 s cache by itself |
| `GET /api/sessions` | list of sessions (no messages), with `tokens_in`, `tokens_out`, `cost` |
| `POST /api/sessions` | create: `{"mode":"small","workspace":"/path"}` (without `mode` the default mode of the settings is used) |
| `GET /api/sessions/{id}` | a session with its messages and its own `tokens_in`, `tokens_out`, `cost` (USD) |
| `DELETE /api/sessions/{id}` | delete a session (refused while it is answering) |
| `POST /api/sessions/prune` | `{"which":"empty"\|"all"}` → `{"deleted":n,"skipped":n}` |
| `GET /api/session-list` | sessions for the settings page: id, title, mode, updated, number of messages, tokens, cost |
| `POST /api/sessions/{id}/settings` | `{"mode","model","preset","agent","workspace"}`; omitted fields do not change (`workspace` is the folder the agent works in; it must exist) |
| `POST /api/sessions/{id}/messages` | `{"text":"..."}` → a stream of SSE events |
| `POST /api/approvals/{id}` | `{"decision":"allow\|always\|deny"}` (id from the `approval` event) |
| `POST /api/compare` | `{"text","models":["a","b"],"preset"}` → a stream of events with a `slot` field |
| `POST /api/processes/{name}/{start\|stop}` | manage local processes; the body of start may carry `{"args":[...]}` (added to the args of the config; without it the arguments saved in the settings are used); the answer is `{"processes":[...],"already":bool}` |
| `GET /api/settings` | `{"settings":{...},"modes":[...],"processes":[...]}` |
| `POST /api/settings` | change the settings; fields that are omitted keep their value; the answer is the same as for GET |
| `GET /api/connections` | redacted OAuth connection metadata (issuer, resource, scopes, status, expiry, account label when available, last use); never returns tokens or secrets |
| `DELETE /api/connections/{id}` | disconnect locally: delete access/refresh tokens while retaining provider/client configuration |
| `POST /api/connections/{id}/revoke` | attempt provider revocation when an endpoint is configured, then remove local tokens; returns `remote_revocation` |
| `DELETE /api/connections/{id}/forget` | delete the complete local OAuth record and access-token handoff file |
| `GET /api/oauth/providers` | built-in OAuth provider endpoint profiles and registration notes (GitHub, Google, Microsoft) |
| `GET /api/mcp` | MCP servers of `mcp.json` (and the old `[[mcp]]`): `id`, `name`, `type` (`stdio`\|`http`), `target`, `source`, `state` (`configured`, `connecting`, `connected`, `auth_required`, `disconnected`, `error`), `error`, `warning`, `trusted` (stdio: the exact command was approved), `tools` (name, description, disabled); `error` of the answer is a problem in `mcp.json` that is being ignored |
| `POST /api/mcp/{id}/{connect\|disconnect\|reconnect\|trust}` | waits for the result (up to ~90 s for a program that has to start); a failure is the error of the request; the answer is the same as for GET |
| `GET /api/mcp/{id}/authorization` | prepares and returns the exact provider/issuer/resource/requested-scope consent description; discovery is read-only and the result is cached briefly |
| `POST /api/mcp/{id}/authorize` | starts sign-in only after a recent consent description was prepared; answers `{"url": ...}` for the browser; callback reconnects the server |
| `GET /oauth/callback` | the address a service sends the browser back to (no token needed; the `state` of the flow protects it) |
| `GET /api/tools` | `builtin` (tools of the agent: name, description, approval), `local` and `connectors` (MCP servers by `kind`, with tools), `oauth_callback` |
| `GET /api/skills` | installed skills: `name`, `description`, `path`, `enabled`, license, compatibility, metadata; `problems` lists folders that are not valid skills |
| `POST /api/skills/{name}/{enable\|disable}` | switch a skill on or off (kept in `skills.json`); the answer is the same as for GET |
| `GET /api/keys` | providers and keys: `group` (`official`, `external`, `local`; sorted in this order), `stored` (id, hint = last 4 characters), `total`, `available`, `needs_key`, `key_optional` |
| `POST /api/keys` | add a key: `{"provider","key"}`; the full key is never returned |
| `DELETE /api/keys/{provider}/{id}` | delete a stored key |
| `GET /ui/help` | the help page (English HTML, no authorization); opened by `/help` |
| `GET /ui/connections` | the OAuth Connections page (HTML; API token is taken from `#token=...` in the URL) |
| `GET /ui/keys` | the API keys page (HTML, no authorization; it takes the token from `#token=...` in the URL) |
| `GET /ui/settings` | the settings page (HTML, same way as the keys page); opened by `/settings` |
| `POST /api/shutdown` | stop the backend |
| `GET /v1/models`, `POST /v1/chat/completions` | OpenAI-compatible gateway |

One request runs in a session at a time (otherwise 409).
Cancel: close the HTTP connection of the stream and the run stops. When a tool needs OAuth, the event stream first emits a separate `approval` event with `tool: "oauth_connect"`; an `allow` or `always` decision starts this attempt only; neither is remembered. The later `auth` event carries the provider URL. OAuth approval is separate from approval of the tool's business action.

## Settings

`settings.json` in `<home>`; edited on `/ui/settings` or with `POST /api/settings`.

| Field | Values | Meaning |
|---|---|---|
| `show_thoughts` | bool | send the model's reasoning as `thinking` events. When false they are not sent at all |
| `stream_speed` | `instant`, `fast`, `normal`, `slow` | how fast the interface types out the answer (the backend always streams at network speed) |
| `sidebar` | `auto`, `hidden` | the side panel of the terminal interface |
| `mascot` | bool | draw the mascot |
| `text_width` | 0 or 40–240 | wrap the chat at this many columns (0 = full width) |
| `mouse` | bool | the interface captures the mouse; off means text can be selected freely |
| `default_mode` | mode name or "" | the mode of new sessions |
| `autostart` | `{name: bool}` | start the process with the backend (overrides `autostart` of config.toml) |
| `process_args` | `{name: "args"}` | arguments used when a process is started without any |
| `keep_backend` | bool | do not stop the backend when the interface quits |

## Stream events

The field `type`; the other fields depend on it (empty ones are not sent).

| type | Fields | Meaning |
|---|---|---|
| `model` | `model` | the model started answering |
| `text` | `text` | a piece of the answer |
| `thinking` | `text` | a piece of the model's reasoning (not part of the answer); only when `show_thoughts` is on |
| `notice` | `text`, `kind` | service message: `fallback`, `rate_limit`, `key_switch`, `no_key`, `info` |
| `tool_call` | `tool`, `call_id`, `args`, `desc` | the agent calls a tool |
| `approval` | `tool`, `call_id`, `args`, `desc` | waits for a decision; `call_id` is the id for `/api/approvals/{id}`. `tool` = `oauth_connect` is the separate OAuth-start consent (even `always` approves only this attempt); `switch_model` asks whether Relay may move to another model of the same provider after the chosen one failed; other tools ask for the corresponding action approval |
| `auth` | `tool` (the server id), `text` (the address), `desc` | emitted only after OAuth-start approval; open `text` in a browser. The work goes on by itself when the sign-in is done, or fails when it is cancelled or takes more than 5 minutes |
| `progress` | `tool`, `text`, `stage`, `stages` | a tool reports what it is doing (the builder tools: "Checking the code", 2 of 4); the interface shows a progress bar |
| `tool_result` | `tool`, `result`, `is_error` | the result of a tool |
| `usage` | `model`, `in`, `out`, `cost` | usage (USD at the prices of the configuration); also added to the totals of the session |
| `error` | `text`, `kind` | `rate_limit`, `no_key` (the provider has no key), `provider`, `internal` |
| `done` | | the run is finished |

In `/api/compare` the events also carry `slot` (1, 2, ...), the number of the model in the request.

## Example

```bash
TOKEN=$(cat ~/.config/relay/token)
SID=$(curl -s -X POST localhost:8787/api/sessions -H "Authorization: Bearer $TOKEN" \
        -d '{"mode":"medium"}' | jq -r .id)
curl -N -X POST localhost:8787/api/sessions/$SID/messages \
     -H "Authorization: Bearer $TOKEN" -d '{"text":"Hello"}'
```

## OpenAI gateway

Compatible in format with `/v1/chat/completions` and `/v1/models`: streaming and plain modes,
`usage` in the answer. `model`: `relay-small|medium|hard|maximum`, `auto`, or a model id.
Not supported: `tools` and `function_calling` (requests with them are handled as plain chat).

## Protection

- While listening on loopback the server answers only to the Host `127.0.0.1`, `localhost`, `::1` (DNS rebinding).
- The pages `/ui/keys`, `/ui/settings` and `/ui/help` are static and contain no secrets; the CSP forbids outside resources.
- Keys are stored in `<home>/keys.json` (0600); the API returns only the last 4 characters.

## Extension filesystem behavior

`skills/` is scanned at startup and watched while Relay runs; a valid `SKILL.md` is required for a folder skill to appear. `mcp.json` is watched and remains the configuration source of truth. For MCP servers created by Relay, the entry point `<home>/connectors/<id>/server.py` is watched as well; when it is removed, the server is stopped and omitted from the active MCP list. External commands and remote MCP servers do not require a Relay-owned source folder. A malformed or temporarily unreadable `mcp.json` keeps the last known-good server set.

## Relay OAuth behavior and provider setup

Relay uses Authorization Code with PKCE (S256) for remote MCP authorization. For a remote
Streamable HTTP server it first reads the `WWW-Authenticate` resource-metadata challenge,
then follows OAuth Protected Resource Metadata and Authorization Server Metadata. If the
authorization server advertises an RFC 7591 `registration_endpoint`, Relay can register a
public client automatically. If it does not, the user must register an OAuth application with
the provider and configure its client ID. Relay cannot manufacture a provider-issued client ID.

For local stdio connectors created with Relay Builder, the `oauth` block uses the same Relay-owned Broker lifecycle, but it is not the remote HTTP MCP authorization protocol. `oauth.resource` is required and identifies the upstream API base URI. The connector receives only the current access-token file path through `RELAY_OAUTH_TOKEN_FILE`; refresh tokens and client secrets remain in Relay's encrypted vault. Connector code must use the SDK's `http(..., oauth=True)` helper; the SDK enforces the resource origin/path, validates and pins DNS answers, refuses redirects, and does not expose raw token material.

Optional OAuth configuration fields:

- `registration_url`: RFC 7591 dynamic client registration endpoint. Use only when the
  provider explicitly supports it. When configured, `client_id` may be omitted.
- `resource`: RFC 8707 resource indicator. For HTTP MCP, Relay always sends the canonical resource in authorization and token requests; if omitted, it defaults to the MCP URL. For stdio Builder connectors, it is required and identifies the upstream API base URI.
- `send_resource`: Builder stdio only; explicitly send RFC 8707 `resource` to the upstream provider when its documentation supports it. HTTP MCP always sends it per the targeted 2025-06-18 profile.
- `issuer` / `require_issuer`: expected authorization-server issuer and whether the callback must include RFC 9207 `iss`. If a callback includes `iss` but Relay has no expected issuer, it is rejected.

## Streamable HTTP transport coverage

Relay's HTTP transport implements MCP Streamable HTTP request/response operation:

- JSON-RPC requests are sent as HTTP POST with `Accept: application/json, text/event-stream`.
- JSON responses and Server-Sent Events returned from a POST are parsed.
- `Mcp-Session-Id` and `MCP-Protocol-Version` are carried on subsequent requests.
- `notifications/initialized` and other notifications are sent as POST requests.
- HTTP 401 responses are converted into an authorization-required state; redirects are not
  followed, preventing bearer tokens and configured headers from being forwarded elsewhere.
- Sessions are ended with HTTP DELETE when the server issued a session ID.

The current implementation does not implement the optional client-initiated GET SSE stream
for server-initiated messages, nor the legacy HTTP+SSE transport. Treat those as unsupported
rather than assuming every HTTP-based MCP transport is interchangeable. A server using
Streamable HTTP should be configured with its exact MCP endpoint URL.


## OAuth security and lifecycle notes (0.3.0)

- Relay owns the authorization-code flow, PKCE verifier, state, callback, token exchange, refresh and credential lifecycle. Tokens are never inserted into model messages or tool descriptions.
- OAuth-start consent is a separate `approval` event (`tool: "oauth_connect"`) before dynamic registration or browser launch. The prompt shows provider/issuer, resource, explicitly requested scopes and provider-supported scopes separately. Supported scopes are not automatically requested. `always` is treated as a one-time approval for OAuth and is never remembered.
- OAuth records use AES-256-GCM. Linux Secret Service (`secret-tool`) stores the master key when available; otherwise the master key is kept in an owner-only local file. Legacy plaintext records are encrypted on first load. Builder stdio processes receive only the access-token handoff file.
- Discovery and token/authorization endpoints must use HTTPS. HTTP is accepted only for loopback development and callback endpoints. Endpoint URLs containing user-info or fragments are rejected; private, loopback, link-local and reserved DNS answers are rejected for remote OAuth endpoints.
- Redirects are not followed during metadata discovery or OAuth token exchange. MCP HTTP redirects are rejected so bearer credentials cannot be forwarded to another origin.
- When Protected Resource Metadata is found, its required `resource` URI must cover the MCP endpoint (same origin and path-segment boundary); legacy discovery without PRM falls back to the configured server URL. Authorization-server metadata must include an issuer and it must match the discovered issuer. An authorization response `iss`, when supplied, must match an expected issuer; it is rejected if Relay has none.
- Credentials are attached only when the exact MCP target URL matches the stored target and the resource identity is bound to that record. Discovery rejects a resource URI that does not cover the MCP endpoint. HTTP MCP sends the canonical RFC 8707 `resource` parameter in both authorization and token requests, regardless of an AS capability flag. A changed MCP URL/server ID cannot silently reuse a token for another target.
- OAuth state is single-use. Lifecycle transitions are validated; cancellation/expiry prevents a late callback from completing the session.
- Refresh is serialized within the daemon to avoid concurrent refresh-token rotation races. A successful sign-in causes the MCP manager to reconnect. The agent retries the interrupted tool once after a structured pre-execution auth-required result.
- `GET /api/oauth/providers` lists the implemented GitHub, Google and Microsoft profiles. `GET /api/connections`, consent-preparation, sign-in/reconnect, disconnect/revoke/forget endpoints, and `/ui/connections` manage redacted connection metadata.

Current limitations are intentional and must not be mistaken for full specification coverage:

- Native macOS Keychain and Windows Credential Manager adapters and master-key rotation are not implemented. The local-file key fallback does not defend against malware running as the same OS user.
- Slack and Notion are not advertised as built-in profiles; provider-specific PKCE/token-response contracts need dedicated implementation and real-provider tests.
- The Builder SDK passes an access-token file to a local stdio process. The handoff file is plaintext by design and owner-only on Unix; the connector process must read its access token.
- Userinfo account labels are optional and absent when scopes/provider endpoints do not expose an identity claim. ID tokens are not decoded without signature validation.
- CIMD and broader RFC 9728/8414 conformance fixtures remain release gates. Legacy HTTP+SSE and client-initiated GET SSE are unsupported; Streamable HTTP POST JSON/SSE responses are supported.
- Full Go 1.24.2 repository tests, vet, cross-platform builds and real browser/provider acceptance tests remain unverified in the offline implementation environment.
