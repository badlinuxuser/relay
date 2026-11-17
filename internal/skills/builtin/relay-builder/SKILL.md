---
name: relay-builder
description: Build new abilities for Relay yourself, and repair them. MCP servers (tools for this computer, connectors to outside services such as Telegram, Slack, mail, a calendar, GitHub, a smart-home hub, a database, any API) and skills (written procedures). Use it whenever the user needs something that none of your tools or skills can do, whether they ask for it directly ("make a tool", "connect my GitHub", "сделай инструмент") or only ask for the RESULT ("send this to my Telegram", "what is on my calendar tomorrow", "open YouTube and search for cats", "check the orders in our database", "do the release notes the way we always do"), and when a tool you built fails and must be fixed.
---

# Building skills and capabilities for Relay

The user asks for an outcome or a Skill, not for internal implementation pieces. **Skills are the user-facing capability; MCP servers are implementation details.** Decide whether the requested Skill can work with instructions alone, can reuse an installed MCP, or needs a new MCP. A Skill request does not mean “create only a text file”: if tools are necessary, create/reuse them and connect them to the Skill in the same task. Conversely, never create an MCP when clear instructions alone are sufficient.

You can extend Relay yourself, so do not ask the user to write code or edit files. Relay runs **on the user's own computer**: what you build runs there, with their rights, and can open their browser and applications, show notifications and use their files. You are not in a sandbox.

Your tools for it:

- `propose_extension`: ask the user whether to build (or fix) something. **Always first.**
- `list_extensions`: what exists, where it lives, what this computer has (desktop, Python), the OAuth callback address
- `create_mcp_server`: write an MCP server (a program) and plug it in, or plug in a remote one by address. **Every server comes with a skill.**
- `create_skill`: write a skill on its own
- `read_server`: read the code of a server that was built here (to repair it)
- `authorize_connector`, `remove_extension`

## 1. Recognise the need

The user does not have to ask for a "tool". Build when the task needs something you do not have and a small program (or a written procedure) would do:

| The user says | What is missing |
|---|---|
| "make a tool that ...", "connect my GitHub", "сделай коннектор", "создай скилл" | asked directly |
| "send this to my Telegram", "напиши мне в телеграм, когда закончишь", "post it to Slack", "reply to that e-mail" | a messenger or mail |
| "what is on my calendar tomorrow", "добавь встречу", "what are my open issues" | an account or service |
| "open YouTube and search for cats", "открой Firefox", "show a notification" | control of this computer |
| "turn off the lights", "check the orders in our database", "what does our API say about order 5" | a device, a database, an API |
| "do it the way we always do", "always use this checklist" | a **skill** (no code) |

Test: can I do it with my tools (files, shell, web) in a reasonable way? If not, offer to build. Do not say "I cannot" or "I have no tool for this" before you have considered this. Look at `references/patterns.md`: it has a recipe for the usual requests.

## 2. Ask first. Always.

Call `propose_extension` before creating or changing components. Describe the missing user-visible outcome in one short sentence. Choose a broad kind (`skill`, `tool`, `connector`, or `fix`) only as a hint; it does not restrict which components you may create after consent. Nothing is created before the user agrees. **If the user refuses, cancel the whole request.** After consent, do not ask again for permission to create the supporting MCP needed for the agreed capability. Ask at most one or two short questions only when a real design choice depends on the answer.

## 3. Inspect, plan, then build the complete capability

First call `list_extensions`. Inventory installed Skills and MCP servers, their state, available tools and existing Skill-to-server associations. Search the existing inventory by function, not just by exact name. Reuse a suitable connected server; do not create a duplicate merely because its id or tool names differ. If it exists but is broken, follow the repair procedure rather than adding a second copy.

Then choose the smallest implementation:
- Instructions/procedure/style only: create a Skill and no MCP.
- Existing MCP already provides the needed operations: create a Skill that documents and uses those tools; do not create another server.
- No existing MCP provides required operations: create one MCP server **with its matching Skill in the same `create_mcp_server` call**. This is the normal implementation for a Skill that needs new tools; do not create a separate empty Skill first.
- If several existing/new servers are genuinely needed, one Skill may explain when to use each. Avoid redundant servers and overlapping tools.

Before writing a local Python server, read `references/sdk.md` and the closest example: `example-apikey.py`, `example-public-api.py`, `example-desktop.py`, `example-sqlite.py`, `example-tools.py`, or `example-oauth.py`. For a remote MCP, read the MCP configuration reference. Use `read_server` before repairing code created by Relay.

## 4. Choose the simplest way to log in

An `oauth` block is only for services where the **user signs in with their own account** and the documentation describes OAuth 2.0. For everything else use an environment variable and `secret("NAME")`:

| Service | Use |
|---|---|
| Telegram bot | the bot token from @BotFather in env. **Telegram bots have no OAuth.** A personal Telegram account needs MTProto (a library): say so, offer the bot |
| Slack, Discord, Teams | a webhook URL or a bot token in env |
| GitHub, Notion, most SaaS and APIs | a token or an API key in env (OAuth only if the user wants to sign in with their account) |
| Google, Microsoft, Spotify | OAuth |
| files, SQLite, programs, the desktop, public APIs | nothing |

Relay refuses an `oauth` block whose code never uses the token, and the other way round. The secret itself never goes into code, into the chat or into `mcp.json`: `env: {"NAME": "${NAME}"}`. Tell the user to `export NAME=...` in the shell profile and **restart Relay** (`relay stop`, then start it again): a running Relay does not see variables set later.

## 5. Write it small and call `create_mcp_server` when tools are needed

- `server.py` with `relay_mcp` (never write the protocol yourself), up to about 200 lines; more goes into `files`. The output of one reply is limited: if it is cut, Relay asks you to go on or to write the cut call again smaller.
- Do the SIMPLEST thing. "Open Firefox and search YouTube" is an address opened in the user's browser, not Selenium or a headless browser.
- Standard library only (Relay installs no packages).
- 3 to 8 tools, one job each; reading tools first, writing tools separate and marked "WRITES".
- Errors say what to do: `raise ToolError("... check owner and repo")`. No `print()`: use `log()`.
- Validate arguments; return short results (a `limit`); timeouts on everything.
- The user is asked to approve it, because the program will run on their computer. The approval shows only a summary (the addresses it contacts, programs, file writes), not the code: when the user wants to read it first, show it in your answer before the call.

### The skill that goes with a server

`create_mcp_server` refuses without a `skill`. It tells the model that uses the tools later when and how to use them. Write `skill: {description, instructions}`:
- `description`: what the tools are for **and when to use them** (1 or 2 sentences; this is the line in the list of skills; name the situations).
- `instructions`: Markdown, 10 to 40 lines: which tool for which job, how to fill the arguments (formats, where an id comes from), limits and pitfalls, which tools write, what the user must set up once. No secrets. The list of tools is added by Relay.

## 6. Check the answer, test, finish

After creation, call `list_extensions` again and verify the server is registered, connected (or clearly reports what setup is missing), and that its Skill is present and associated. Use a read-only tool with realistic input to test it; do not perform an irreversible write just as a test. A connector requiring sign-in opens the browser on first use (or use `authorize_connector`). If validation or startup fails, read the error, repair the same component and replace it; do not leave a broken duplicate. Finally finish the user's original task, then briefly say what capability was added/reused and any one-time setup the user must do.

## 7. When something you built does not work

Do not give up and do not say you cannot. Read the error. If the cause is the code or the setup (a wrong assumption, a missing module, a variable), call `propose_extension` with kind `fix` (one sentence: what is broken). If the user agrees: `read_server` to see the code, repair it, `create_mcp_server` again with `replace: true`, and run the same call again. If they refuse, the request is cancelled. See the end of `references/patterns.md`.

## Connectors with OAuth

Relay does the whole sign-in (browser page, PKCE, callback, token, refresh). Your program never sees the refresh token or the client secret: it calls `http(..., oauth=True)` and gets a valid access token.
1. `list_extensions` gives the **callback address**.
2. Tell the user to register an OAuth app at the service with exactly that callback address, and to give you the **client id** (not a secret); a client secret goes into their own environment variable (`"${NAME}"`).
3. `create_mcp_server` with `kind: "connector"` and `oauth: {authorize_url, token_url, client_id, client_secret, scopes, params}`; endpoints of common services are in `references/mcp-json.md`.
4. `authorize_connector`, or simply call a tool: Relay pauses, opens the page, waits, and repeats your call.
Not supported yet: services that want the client secret in an HTTP Basic header at the token address.

## Writing a skill on its own

`create_skill` when the user wants Relay to follow a procedure, a style or house rules.
- `name`: lowercase letters, digits and single hyphens (`release-notes`).
- `description`: what it does **and when to use it**; only this line is in the list, so name the situations.
- `instructions`: concrete steps, formats, examples; under about 300 lines. Long material goes into extra files (for example a style guide in a `references` folder), which are read only when needed.
- No secrets. Scripts in `scripts/` are never run automatically: write the exact command in the instructions.
- A skill is advice, not a rule of the system: it cannot give permissions or override the user.

## References

`patterns.md` (recipes, design rules, repairs), `sdk.md` (the helper), the six `example-*.py`, `mcp-json.md`, `relay-structure.md`, `skills-format.md`.
