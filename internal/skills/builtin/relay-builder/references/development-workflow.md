# MCP development workflow (Relay 0.2)

Use this workflow for every new local MCP server and for substantial repairs.

## 1. Discover before designing

1. Load `relay-builder` and read `references/sdk.md`.
2. Call `list_extensions` and inspect existing MCP servers and Skills.
3. Reuse a suitable server. Do not create a second connector for the same service unless the user needs a genuinely different capability or isolation boundary.
4. Research the service's official API documentation with `web_fetch` or available web research. Confirm authentication, pagination, rate limits, write semantics and API versions. Do not guess endpoint paths or request fields.

## 2. Design the project

- Prefer a small set of focused tools with precise names, descriptions and JSON schemas.
- Keep `server.py` as the entry point; put substantial API clients, parsing, models and helpers in separate files.
- Keep read-only operations separate from operations that modify external state. Mark write effects clearly in tool descriptions.
- Use the Relay `relay_mcp` SDK. Do not implement the MCP wire protocol yourself.
- Use dependencies only when the Python standard library and the SDK are insufficient. Pin versions when practical.
- Credentials must come from environment variables using `secret("NAME")`, or Relay OAuth where the service requires user-account sign-in. Never embed secrets in source, Skills, arguments or logs.

## 3. Validate before installation

Call `validate_mcp_server` with the complete entry point, all project files, environment references and dependencies. This creates a temporary project, optionally builds a private virtual environment, checks Python syntax, starts the server through Relay's MCP runtime and verifies the MCP handshake and tool discovery. It does not register the server or write into the user's connectors directory.

If validation fails:

1. Read the complete error and last server output.
2. Re-check the official API documentation and SDK contract.
3. Fix the source or dependencies.
4. Run `validate_mcp_server` again.
5. Do not install until validation passes.

Validation proves startup and tool discovery, not that every API operation behaves correctly. After installation, test a safe read-only tool with realistic input and inspect its result. Never call a write/delete/send tool as a smoke test unless the user explicitly asked for that action and approved it.

## 4. Install and integrate

Only after validation succeeds, call `create_mcp_server` with the same project source and dependencies, plus the Skill specification. Relay asks for approval before executing the new code. The server is installed under its own connector folder; dependencies, if any, are installed in that server's private `.venv` and do not alter the global Python environment.

After installation:

- Check the discovered tool names and schemas.
- Ensure the Skill explains which tool to use for each task, argument formats, limits, write effects and setup requirements.
- Test one read-only tool with realistic input.
- Confirm the original user request is completed, not merely that the MCP starts.

## 5. Repair and maintain

For an existing server, call `read_server` before changing it. Preserve working behavior, make the smallest necessary change, validate the revised project with `validate_mcp_server`, then replace it using `create_mcp_server` with `replace: true` and the updated Skill. Do not remove an existing working server until its replacement has passed validation.
