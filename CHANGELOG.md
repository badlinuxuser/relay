# Changelog

All notable project changes are documented here. This file consolidates the former patch and release notes.

## [0.2.1] — Security hardening and Builder evolution

### Security
- Require a fresh user approval for every MCP tool invocation, including OAuth sign-in.
- Do not persist session-wide “Always allow” decisions for MCP calls; enforce one-time approval even if an automatic-approval entry already exists in session state.
- Require fresh approval for shell execution, workspace file writes/edits, MCP creation/validation and Skill creation.
- Improve MCP approval previews by identifying the server and tool, showing useful arguments, redacting common credential-shaped fields, and excluding raw MCP JSON arguments from `tool_call` and `approval` UI events.
- Clarify that a `tool_call` event records a model request, not proof that the tool executed. Check the approval decision and corresponding `tool_result`.

### Builder and MCP development
- Add `validate_mcp_server` to validate a draft in a temporary project directory without registering or permanently installing it.
- Validate Python syntax, start the draft through Relay's MCP runtime, perform the MCP handshake and discover available tools before installation.
- Add an iterative Builder workflow: propose, read the Builder guide and SDK reference, validate, inspect errors, revise, validate again, then install.
- Support optional PyPI dependencies in a server-private Python virtual environment.
- Restrict dependency specifications to package names and version constraints; reject URLs, VCS references, local paths and pip options.
- Save dependencies in `requirements.txt` and exclude `.venv` contents from server source inspection.

### Documentation and verification
- Update Builder instructions, SDK references, extension documentation, protocol documentation and the project map.
- Add tests for MCP draft validation and dependency specification validation.
- `gofmt`, static balance/duplicate checks and the Python SDK test script passed in the original development environment.
- The original environment had Go 1.23.2 while the module requires Go 1.24.2; network access was unavailable, so Go unit/integration tests were not run there.

### Security limitations
- Local stdio MCP processes run with the Relay user's operating-system permissions. This release does not provide OS-level sandboxing or container isolation.
- An OAuth stdio connector can read its own access-token file through `RELAY_OAUTH_TOKEN_FILE`; the refresh token remains in Relay's store.
- Credential redaction in approval previews is best-effort and based on field names. Do not put secrets in arbitrary argument fields or tool descriptions.

## [0.2.0] — MCP development workflow

- Extend the Builder from generating a single `server.py` to an iterative MCP development workflow: design, preflight validation, temporary startup, tool discovery, repair and installation with a Skill.
- Add `validate_mcp_server`, which creates a temporary project copy, checks Python syntax, starts it through Relay's MCP Manager, performs the handshake and discovers tools without writing to `mcp.json` or the permanent connectors directory.
- Update agent instructions, `propose_extension` and the built-in `relay-builder` Skill to use validate → diagnose → revise → revalidate → create.
- Support optional PyPI dependencies for MCP projects using a separate `.venv` per server. Reject URL/VCS/local-path dependencies and pip flags; write accepted dependencies to `requirements.txt`.
- Add the development workflow reference, update SDK/extension/README/protocol/project documentation, and add validation/dependency tests.
- Verification at the time: `gofmt`, Python SDK tests and static balance/duplicate checks passed. Go tests were not run because Go 1.24.2 and module downloads were unavailable in the development environment.

## [0.1.1] — Skills-first Builder and MCP lifecycle

- Make Skills the user-facing unit of functionality. The Builder inventories existing components and chooses a Skill-only implementation, reuses an existing MCP server, or creates a new MCP server together with a Skill.
- Treat MCP creation as an implementation detail of fulfilling a Skill request, including indirect requests.
- Improve approval UI labels so internal tool names are not exposed as user-facing capability names.
- Simplify Builder progress display while retaining the animated activity line and elapsed time.
- Watch Relay-managed MCP entry points as well as configuration/trust files; remove a managed server from the active list only when its entry point is confirmed deleted.
- Add `relay mcp info` and `relay mcp validate`; make `relay mcp remove` confirmation-based with `--yes` and `--keep-files` options and constrain source deletion to the managed connector directory.
- Update documentation and add tests for managed-server deletion detection, external command preservation and approval UI labels.
- Verification at the time: `gofmt` and focused MCP/Builder tests passed. Full Go tests and real external-service end-to-end tests were not completed in the original environment.

### Known scope exclusions recorded for 0.1.1
- No MTProto implementation for sending messages from a personal Telegram account; Telegram Bot API integration is a separate use case.
- A separate timeout issue in external browser-control/Playwright was not part of this patch.
- OAuth and real external-service flows were not fully end-to-end tested.
