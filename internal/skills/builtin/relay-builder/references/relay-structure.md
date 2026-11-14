# How Relay is organised (what you need to know to extend it)

Relay is one program: a terminal interface and a background service that talk over local HTTP.
The service routes requests to models (cloud or local), runs the agent loop, and keeps MCP servers.

- **Agent mode** (`ctrl+a` or /agent): the model gets tools and works in steps: it calls a tool,
  reads the result, continues. Built-in tools: `read_file`, `grep`, `write_file`, `edit_file`,
  `run_shell`, `web_fetch`, the skill tools and the builder tools (`propose_extension`, `create_mcp_server`, `create_skill`, `list_extensions`, `authorize_connector`, `remove_extension`). Writing, shell and every
  MCP call ask the user first (they may choose "always" for a tool in a session).
- **MCP servers** are programs (or remote services) that offer more tools. Their tools appear with
  names `mcp__<server id>__<tool>`. Relay starts, stops and reconnects them, and watches `mcp.json`:
  a change applies without a restart.
- **Tools, connectors, skills** are what the user sees with `/tools`, `/connectors`, `/skills`.
- **While you build**, the user sees a progress bar (stages: guide, design, writing and starting, skill, trying) instead of
  your reasoning, unless they chose to see the thoughts. Every server comes with a skill.
- **Skills** are folders with a `SKILL.md`. The list (names and descriptions) is in the system
  prompt; the model loads one with `load_skill` and reads its extra files with `read_skill_file`.
  This skill (`relay-builder`) is built into Relay.
- **Folders** (the exact paths are in the answer of `list_extensions`): `<home>/mcp.json`,
  `<home>/connectors/<id>/` (the code that `create_mcp_server` writes), `<home>/skills/<name>/`,
  `<home>/auth/` (tokens, owner only; programs never get anything but their own access token).
- **Sign-in**: `GET <callback>` is served by the Relay service. A tool call that needs a sign-in
  pauses the answer, opens the browser, waits, and repeats the call after the user signed in.
- **The user's commands**: `/tools`, `/connectors` (`/connectors auth <id>` signs in), `/skills`;
  in a terminal `relay mcp ...` and `relay skill ...`.
