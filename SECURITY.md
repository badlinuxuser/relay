# Relay security notes (0.3.1)

Relay is a local-first agent runtime. Tools and MCP servers can perform actions outside the model, so tool execution is treated as a security boundary. This document describes implemented controls and their limits; it is not a certification or a claim that Relay is safe to run untrusted code.

## 0.3.0 OAuth/LFI Core controls

- One Relay-owned OAuth Broker is shared by remote MCP and Builder-generated stdio connectors.
- Authorization Code + PKCE S256 is mandatory. Remote authorization-server metadata must advertise S256; custom static provider entries must explicitly declare `pkce_s256_supported: true` after checking provider documentation. Built-in profiles are available for GitHub, Google and Microsoft.
- A separate OAuth-start approval is shown before dynamic client registration or browser launch. The prompt includes issuer/provider, resource and explicitly requested scopes; provider-supported scopes are shown separately and are not automatically requested. OAuth consent never grants approval for a repository write, destructive operation or other tool action.
- Callback handling accepts only GET `/oauth/callback`, consumes state once, validates `iss` before processing either success or denial, and rejects late callbacks after cancellation or expiry.
- OAuth records are encrypted with AES-256-GCM. Linux Secret Service is used through `secret-tool` when available; a local master-key fallback is used otherwise. On Unix the fallback directory/key are 0700/0600. Other operating systems rely on user-profile ACLs; native macOS Keychain and Windows Credential Manager adapters are not implemented yet.
- OAuth credentials are bound to both the resource indicator and exact MCP target URL. Relay does not attach a stored bearer token to a different target. HTTP MCP always sends the canonical RFC 8707 `resource` parameter in authorization and token requests per the targeted MCP `2025-06-18` profile; Builder stdio uses an explicit provider-specific opt-in.
- OAuth metadata/token endpoints use a direct resolver-aware HTTP client. Private, loopback, link-local, multicast and reserved DNS addresses are rejected for remote OAuth endpoints. Redirects are not followed during OAuth discovery/token exchange or MCP HTTP requests.
- Token endpoint client authentication supports `client_secret_basic`, `client_secret_post` and `none`. Refresh is serialized within the daemon; successful refresh-token rotation is persisted.
- The Connections API/UI returns redacted metadata only. It supports explicit consent, sign-in, reconnect, local disconnect, provider revocation when an RFC 7009 endpoint is available, and local forget. A failed remote revocation still removes the local credential.
- Builder-generated MCP servers use the shared Relay access-token file contract. The bundled SDK restricts OAuth HTTP requests to the declared resource origin/path, validates and pins DNS answers, and refuses redirects. Source validation rejects common hard-coded credentials, raw token helper/file access and custom OAuth flows; these checks are heuristics, not a sandbox.

## Existing tool and MCP protections

- Every tool call asks for approval first, including MCP tools and starting an OAuth sign-in. `Always allow this tool in this session` is remembered per tool name (for MCP tools the name includes the connector) until the session ends; it never covers other tools. This now applies to the shell, file writes, building servers and skills too, so be careful with it. Starting an OAuth sign-in (`oauth_connect`) is asked again every time.
- `OnceOnly` is enforced even if a session already contains an automatic-approval entry for that tool name.
- Shell execution, workspace file writes/edits, MCP creation/validation, and Skill creation also require fresh approval on every invocation.
- MCP approval previews identify the server and tool, show useful arguments, and redact common credential-shaped fields. Raw MCP JSON arguments are not streamed in the `tool_call` or `approval` UI events.
- A stdio MCP command is fingerprinted and must be trusted before Relay starts it. Changing the command, arguments, environment, URL, headers, or OAuth configuration changes the fingerprint.

## What an event means

The `tool_call` event is emitted when the model *requests* a tool call, before approval is requested. It is not proof that the tool ran. Execution occurs only after approval. Check the corresponding `approval` decision and `tool_result` event to determine whether Relay actually invoked the tool and what result it returned.

## Important limitations

- **A local stdio MCP is arbitrary code.** It runs as the same operating-system user as Relay. This release does not provide an OS-level sandbox, container isolation, filesystem allowlist, or network egress policy for MCP subprocesses. A malicious or compromised local MCP can access files and services that the user account can access.
- An OAuth stdio connector receives `RELAY_OAUTH_TOKEN_FILE` for its own access token. The connector process can read that token file by design. Relay does not pass the refresh token or client secret to the connector, but a malicious connector could still misuse or exfiltrate its access token.
- The encrypted-file fallback does not protect against malware or another process running as the same OS user, because that process can read the master key. Loss of the key makes encrypted records unrecoverable. Native macOS/Windows credential-store adapters and key-rotation UX remain release gates.
- Legacy plaintext OAuth records are migrated on first load. Older remote HTTP records without the exact target-URL binding require one new sign-in before the token is used.
- GitHub, Google and Microsoft are the only built-in endpoint profiles. Slack and Notion are not advertised because their provider-specific PKCE/token contracts have not been verified for the required S256 flow. Custom providers must conform to the documented contract.
- Provider revocation is profile/metadata-dependent. A local disconnect or forget is not proof that a provider has revoked its copy of a token; use the provider's security page when remote revocation is unavailable or fails.
- Remote MCP servers receive credentials needed for their configured connection. Only authorize servers and OAuth applications you trust, and request the narrowest scopes available.
- The built-in `run_shell` tool executes commands with the user's permissions. Per-call confirmation is not a substitute for reviewing the command.
- Credential redaction and Builder source checks are best-effort heuristics, not a sandbox or formal proof that generated code is benign.
- Full Go 1.24.2 repository tests, vet, cross-platform builds, adversarial security suites and real-provider/browser end-to-end tests remain unverified in the offline implementation environment. See `docs/LFI_CORE.md` for the exact verification status.

## Recommended operating practice

1. Keep MCP servers disabled until their source and configuration have been reviewed.
2. Approve only the exact stdio command and source you intend to run.
3. Use dedicated test accounts and minimal OAuth scopes for initial connector tests.
4. Do not place personal-account session files, refresh tokens, client secrets, or API keys in a project workspace or MCP source tree.
5. Review the server's declared tools before enabling it; disable account-changing tools you do not need.
6. Use `/connections` to inspect and disconnect credentials. Revoke grants at the provider when needed, and terminate suspicious service sessions.
