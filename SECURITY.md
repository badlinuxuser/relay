# Relay security notes (0.2.1)

Relay is an agent runtime. Tools and MCP servers can perform actions outside the model, so tool execution is treated as a security boundary.

## 0.2.1 changes

- Every MCP tool invocation requires a fresh user approval, including OAuth sign-in. MCP approval is never remembered by the session's `Always allow` setting.
- `OnceOnly` is enforced even if a session already contains an automatic-approval entry for that tool name.
- Shell execution, workspace file writes/edits, MCP creation/validation, and Skill creation also require fresh approval on every invocation.
- MCP approval previews identify the server and tool, show useful arguments, and redact common credential-shaped fields (`token`, `secret`, `password`, `authorization`, `api_key`, `cookie`, `credential`, `session`). Raw MCP JSON arguments are not streamed in the `tool_call` or `approval` UI events.

## What an event means

The `tool_call` event is emitted when the model *requests* a tool call, before approval is requested. It is not proof that the tool ran. Execution occurs only after approval. Check the corresponding `approval` decision and `tool_result` event to determine whether Relay actually invoked the tool and what result it returned.

## Existing protections

- OAuth refresh tokens and client records are stored under the Relay auth directory; the directory is created with owner-only permissions on POSIX systems and files are written with mode `0600`.
- A stdio MCP command is fingerprinted and must be trusted before Relay starts it. Changing the command, arguments, environment, URL, headers, or OAuth configuration changes the fingerprint.
- Remote HTTP MCP redirects are not followed, preventing authorization headers from being forwarded to a redirect destination.

## Important limitations

- **A local stdio MCP is arbitrary code.** It runs as the same operating-system user as Relay. This release does not provide an OS-level sandbox, container isolation, filesystem allowlist, or network egress policy for MCP subprocesses. A malicious or compromised local MCP can access files and services that the user account can access.
- An OAuth stdio connector receives `RELAY_OAUTH_TOKEN_FILE` for its own access token. The connector process can read that token file by design. Relay does not pass the refresh token to the connector, but a malicious connector could still misuse or exfiltrate its access token.
- Remote MCP servers receive credentials needed for their configured connection. Only authorize servers and OAuth applications you trust, and request the narrowest available scopes.
- The built-in `run_shell` tool executes commands with the user's permissions. Per-call confirmation is not a substitute for reviewing the command.
- Credential redaction in approval previews is best-effort and key-name based. Do not put secrets in arbitrary argument fields or in tool descriptions.

## Recommended operating practice

1. Keep MCP servers disabled until their source and configuration have been reviewed.
2. Approve only the exact stdio command and source you intend to run.
3. Use dedicated test accounts and minimal OAuth scopes for initial connector tests.
4. Do not place personal-account session files, refresh tokens, or API keys in a project workspace or MCP source tree.
5. Review the server's declared tools before enabling it; disable account-changing tools you do not need.
6. Revoke OAuth grants and terminate suspicious service sessions if a connector behaves unexpectedly.

This document describes the protections present in 0.2.1; it is not a claim that Relay is sandboxed or safe to run untrusted MCP code.
