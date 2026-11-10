---
name: relay-builder
description: Build new abilities for Relay yourself. Create MCP servers (tools that work on this computer, and connectors to outside services such as GitHub, Google or a company API, with browser sign-in through OAuth) and skills (written procedures). Use it when the user wants Relay to connect a service, needs a tool that does not exist yet, wants to automate something with their own program, or asks for a skill.
---

# Building tools, connectors and skills for Relay

You can extend Relay yourself, so do not ask the user to write code or edit files. You have five tools for it:

- `create_mcp_server`: write an MCP server (a program) and plug it into Relay, or plug in a remote one by address
- `create_skill`: write a skill (instructions for a kind of task)
- `list_extensions`: see what is installed, where it lives, and the exact sign-in callback address
- `authorize_connector`: sign the user in to a connector (opens the browser and waits)
- `remove_extension`: remove a server or a skill you made

## What to build

| The user wants | Build |
|---|---|
| Relay to do something on this computer (read a folder, run a program, query a local database) | a **tool**: `kind: "tool"` |
| Relay to work with an outside service (GitHub, Google Calendar, Notion, a company API) | a **connector**: `kind: "connector"` |
| Relay to follow a procedure or a style, no new ability needed | a **skill** |

A connector needs credentials. There are two ways, ask which one fits:
1. An **API key or personal token**. The user puts it in an environment variable; the server reads it with `secret("NAME")`. Never ask the user to paste a secret into the chat.
2. **OAuth sign-in**. The user signs in in the browser; Relay stores and refreshes the token. See "Connectors with OAuth" below.

If the service already offers a remote MCP server (an https address), plug that in with `create_mcp_server` and `url`: no code is needed, and Relay signs the user in by itself if the server asks for it.

## Workflow

1. **Understand the goal.** Ask at most one or two short questions, only if the answer changes what you build: which service, read-only or also writing, key or OAuth.
2. **Look first.** Call `list_extensions`: something suitable may exist, and it shows the folders and the callback address.
3. **Design small.** 3 to 8 tools. One job per tool, names like `list_issues`, `get_file`. Start with reading tools; make writing tools separate and say so in their description ("WRITES: creates an issue"). The model that uses the tools later decides from the descriptions alone, so make them precise.
4. **Read the references you need** with `read_skill_file` (skill `relay-builder`):
   - `references/sdk.md`: the whole API of the `relay_mcp` helper (short)
   - `references/example-tools.py`: a complete tool server
   - `references/example-oauth.py`: a complete connector with OAuth
   - `references/mcp-json.md`, `references/relay-structure.md`: how Relay is organised
5. **Write `server.py`** with `relay_mcp` (never write the MCP protocol yourself) and call `create_mcp_server`. The user is asked to approve it, because the program will run on their computer. The approval box shows only a summary (the web addresses it contacts, whether it runs programs or writes files), not the code: when the user wants to read the code first, show it in your answer before you call the tool.
6. **Read the answer.** It says whether the server started and lists its tools, or why it failed (a syntax error, a crash with its last message). Fix `server.py` and call `create_mcp_server` again with `replace: true`.
7. **Test it.** The new tools are available in your next step, named `mcp__<id>__<tool>`. Call a harmless read-only one with real input and check the result. A connector that needs a sign-in opens the browser by itself on the first call.
8. **Tell the user** what you built, which tools it has, what it needs from them (a variable to set, an OAuth app to register), and that `/tools`, `/connectors` and `/skills` show it.

## Rules for what you write

- **Standard library only.** Relay installs no packages. If a package is truly needed, say so and let the user decide.
- **Secrets never go into code or into the chat.** Use `env: {"NAME": "${NAME}"}` so that the value comes from the user's own environment, and read it with `secret("NAME")`. The OAuth `client_secret` is the same: `"${NAME_CLIENT_SECRET}"`.
- **Keep tools narrow.** A tool that can only do what its description says is safer than a general one. Check arguments; for a file tool, stay inside a folder you name and refuse `..`.
- **Return what is needed, not everything.** Shorten long lists (`limit` argument, default 10), cut long texts. The helper stops a result at 100000 characters, but the model reads every character.
- **Errors say what to do.** `raise ToolError("repository not found; check owner and repo")` beats a stack trace.
- **No output to stdout.** It is the protocol channel. Use `log("...")`.
- **Set timeouts** on everything that waits (`http()` already has 20 seconds).
- **Be honest about writing.** Tools that change something outside must say so. The user approves each call, and your description is what they read.
- Do not run `server.py` yourself to test it: `create_mcp_server` starts it and reports. Do not edit `mcp.json` yourself: the tools do it correctly, and they record the user's approval.

## Connectors with OAuth

Relay does the whole sign-in: the browser page, PKCE, the callback, storing the token and refreshing it. Your program never sees the refresh token or the client secret; it only calls `http(..., oauth=True)` or `oauth_token()` and gets a valid access token.

Steps:
1. Call `list_extensions` and note the **callback address** (like `http://127.0.0.1:8787/oauth/callback`).
2. Tell the user, briefly, to register an OAuth app in the developer settings of the service with exactly that callback address, and to give you the **client id** (it is not a secret). The **client secret**, if the service has one, they put in an environment variable of their own (for example `GITHUB_CLIENT_SECRET`); you only write `"${GITHUB_CLIENT_SECRET}"`.
3. Call `create_mcp_server` with `kind: "connector"` and an `oauth` block:
   `{"authorize_url": "...", "token_url": "...", "client_id": "...", "client_secret": "${NAME}", "scopes": ["..."], "params": {...}}`.
   Endpoints of common services are in `references/mcp-json.md`; check the service's own documentation when unsure.
4. In the code use `http("GET", url, oauth=True)`. When the service answers 401, `http` makes Relay ask the user to sign in again by itself.
5. Call `authorize_connector` with the id, or simply call one of the tools: when the user has not signed in, Relay pauses, opens the sign-in page in the browser, waits for the user, and then repeats your call.

Not supported yet: services that want the client secret in an HTTP Basic header at the token address.

## Writing a skill

Use `create_skill` when the user wants Relay to follow a procedure, a style or house rules.
- `name`: lowercase letters, digits and single hyphens (`release-notes`).
- `description`: what the skill does **and when to use it**, in one or two sentences. Relay shows only this line in its list, and the model decides from it whether to load the skill, so name the situations ("Use when the user asks for release notes or a changelog").
- `instructions`: Markdown the model will follow. Be concrete and short: steps, formats, examples. Under about 300 lines. Put long material in `files` (`references/...md`), mention it in the instructions, and the model will read it only when needed.
- Never put secrets in a skill. Scripts in `scripts/` are never run automatically; if a step needs one, write the exact command in the instructions (the user is asked before it runs).
- A skill is advice, not a rule of the system: it cannot give permissions or override what the user says.

## After you finish

Say in two or three sentences what exists now and how to use it. If you made something that needs a one-time action from the user (set a variable, register an OAuth app, sign in), say exactly what and where.
