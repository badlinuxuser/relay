# Extending Relay by hand: MCP servers and skills

Relay can build its own tools (turn the agent on and ask: *"connect my GitHub"*). This document is for
the other way: you write or install them yourself. It explains the principle, the files, and every
setting. Everything is plain files; there is no database and nothing to register anywhere else.

- [The idea](#the-idea)
- [Where things live](#where-things-live)
- [When the agent builds for you](#when-the-agent-builds-for-you)
- [MCP servers](#mcp-servers)
  - [mcp.json](#mcpjson)
  - [Adding a server](#adding-a-server)
  - [Writing a server with relay_mcp (Python)](#writing-a-server-with-relay_mcp-python)
  - [Writing a server in another language](#writing-a-server-in-another-language)
  - [Remote servers](#remote-servers)
  - [Secrets and the environment](#secrets-and-the-environment)
  - [OAuth: signing the user in](#oauth-signing-the-user-in)
  - [Testing and debugging](#testing-and-debugging)
- [Skills](#skills)
  - [The format](#the-format)
  - [How Relay uses a skill](#how-relay-uses-a-skill)
  - [Installing, switching off, removing](#installing-switching-off-removing)
  - [A skill for every server](#a-skill-for-every-server)
- [A checklist for a good connector](#a-checklist-for-a-good-connector)
- [Security: what Relay does and does not do](#security-what-relay-does-and-does-not-do)
- [Troubleshooting](#troubleshooting)

## The idea

Two things extend Relay, and both work with **any model**, local or cloud, as long as it can call tools:

| | What it is | What it gives the model |
|---|---|---|
| **MCP server** | A program (or a remote service) that speaks the Model Context Protocol | **Tools**: things the model can do (read a folder, list GitHub issues, create a calendar event) |
| **Skill** | A folder with a `SKILL.md` | **Instructions**: how to do a kind of task, when to use which tool |

A tool without a skill is a tool that models use badly, so the two belong together: write the server, then write a
short skill that says when and how to use its tools. (When Relay builds a server itself, it always writes the skill too.)

MCP servers come in two **kinds**, which only decide where Relay lists them:

- **tool** (`/tools`): works on this computer: files, programs, a database on localhost;
- **connector** (`/connectors`): works with an outside service: GitHub, Google, a company API, any remote MCP server.

## When the agent builds for you

Turn the agent on (`ctrl+a`) and ask: directly (*"make a tool that keeps notes"*, *"connect my GitHub"*) or only for the result
(*"send this to my Telegram"*, *"what is on my calendar tomorrow"*, *"open YouTube and search for cats"*). Relay runs on **your**
computer, so what the agent builds runs here, with your rights, and can open your browser and applications. The order is fixed:

1. **The offer.** The agent asks: *"Relay has no connector for this: ... Build one now?"* **If you refuse, the whole request is
   cancelled.** (Switch the offers off in the settings, *Offer to build what is missing*: then only step 4 remains.)
2. **Reading.** Before it writes anything the agent has to read the built-in skill `relay-builder` and, for a program, its SDK
   description. The build tools refuse to work until it has.
3. **Writing.** One call creates the program, checks it, starts it, and writes **a skill** for it (required). While this goes on you
   see a single line under your question instead of the model's thoughts (`/progress off` shows everything).
4. **Your approval of the program.** The question shows a summary found by reading the code: the web addresses it contacts, whether it
   runs programs, whether it writes files. You may answer "always" for this question in a session; the offer in step 1 is asked every time.
5. **Repair.** If the tool fails because of its own code or setup, the agent asks (an offer of kind *fix*), reads the code
   (`read_server`), and replaces the server. You can read the code yourself in `~/.config/relay/connectors/<id>/server.py`.

The agent chooses the simplest way to log in: a bot token or an API key goes into an environment variable (you `export` it and restart
Relay: `relay stop`); OAuth only for services where you sign in with your own account. Everything it builds is listed by `/tools`,
`/connectors` and `/skills`, and can be edited or removed by hand like anything you made yourself.

## Where things live

All files are in the Relay folder (`relay config` prints where; on Linux it is `~/.config/relay`):

```
~/.config/relay/
├── config.toml            models, modes, providers
├── mcp.json               the MCP servers (the source of truth: edit it by hand if you like)
├── mcp-trust.json         which local programs you approved (written by Relay)
├── connectors/<id>/       code of the servers that the agent (or you) put there
│   ├── server.py
│   └── relay_mcp.py       the helper (Python), copied here by Relay
├── skills/<name>/         one folder per skill
│   ├── SKILL.md
│   └── references/ …
├── skills.json            which skills are switched off
└── auth/                  OAuth tokens (owner-only files; never edit)
```

Relay watches `mcp.json`, `mcp-trust.json` and `skills/`. **A change applies within a second, without a restart.**
If `mcp.json` has a mistake, Relay ignores the whole file and keeps the last good version (see `relay mcp list`).

## MCP servers

### mcp.json

```json
{
  "servers": {
    "notes": {
      "name": "Notes",
      "kind": "tool",
      "type": "stdio",
      "command": "python3",
      "args": ["/home/me/.config/relay/connectors/notes/server.py"],
      "env": { "NOTES_DIR": "${HOME}/notes" },
      "disabled_tools": ["write_note"]
    },
    "github": {
      "name": "GitHub",
      "kind": "connector",
      "type": "stdio",
      "command": "python3",
      "args": ["/home/me/.config/relay/connectors/github/server.py"],
      "oauth": {
        "authorize_url": "https://github.com/login/oauth/authorize",
        "token_url": "https://github.com/login/oauth/access_token",
        "client_id": "Iv1.0123456789abcdef",
        "client_secret": "${GITHUB_CLIENT_SECRET}",
        "scopes": ["repo"]
      }
    },
    "docs": {
      "name": "Company docs",
      "type": "http",
      "url": "https://mcp.example.com/mcp",
      "headers": { "X-Api-Key": "${DOCS_KEY}" }
    }
  }
}
```

The key of each entry (`notes`, `github`, `docs`) is its **id**: 1–32 letters, digits, `-` or `_`. It becomes part of the
tool names (`mcp__notes__read_note`).

| Field | Meaning |
|---|---|
| `name` | Display name. Default: the id. |
| `kind` | `tool` or `connector`. Default: `connector` for a remote server or a server with `oauth`, else `tool`. |
| `type` | `stdio` (a program on this computer) or `http` (a remote server, "Streamable HTTP"). Guessed from `command` / `url` if left out. |
| `command`, `args` | `stdio`: the program and its arguments (`args` is a list; no shell is involved). |
| `env` | `stdio`: extra environment variables. `${NAME}` takes the value from your own environment. |
| `url`, `headers` | `http`: the address and extra headers (values may use `${NAME}`). Credentials do not go into the URL. |
| `oauth` | Sign-in settings, see [OAuth](#oauth-signing-the-user-in). |
| `disabled_tools` | Names of tools that are switched off. |
| `disabled` | `true`: the entry stays but the server is not started. |

The format of Claude Desktop and Cursor (`"mcpServers": {…}`) is read too, so you can copy entries between tools. Relay
writes `"servers"`.

The old `[[mcp]]` section of `config.toml` still works (those servers get Relay's whole environment, as they always did).
New servers belong in `mcp.json`.

### Adding a server

With the CLI:

```
# a local program; everything after "--" is passed to it as it is
relay mcp add files --command npx -- -y @modelcontextprotocol/server-filesystem /home/me/projects

# a remote server
relay mcp add docs --url https://mcp.example.com/mcp --header 'X-Api-Key: ${DOCS_KEY}'

relay mcp list          # state of every server
relay mcp test files    # connect once and show the tools
relay mcp remove files
```

Or edit `mcp.json`. **A local program runs only after you approved that exact command**, because it runs with your
rights. `relay mcp add` shows the command and asks; for an entry that you wrote by hand run `relay mcp trust <id>`.
The approval belongs to the command, the arguments, the environment and the OAuth settings: change one, and Relay asks again.
(Remote servers do not run anything on your computer, so they need no approval; every call of a tool asks you anyway.)

States, as shown by `relay mcp list` and `/connectors`: `configured` (not started, or waiting for approval), `connecting`,
`connected`, `auth_required` (you have to sign in), `disconnected`, `error` (with the reason).

### Writing a server with relay_mcp (Python)

`relay_mcp.py` is a small helper (standard library only, Python 3.8+) that speaks the protocol for you, so a server is
just its tools. Relay has it built in; get a copy next to your own code with:

```
relay mcp sdk ~/my-server        # writes relay_mcp.py into that folder
```

A complete server, `~/my-server/server.py`:

```python
from relay_mcp import Server, ToolError, http, secret

srv = Server("weather")

@srv.tool("get_weather", "Current weather in a city. Returns temperature and a short description.", {
    "city": "string: the name of the city, e.g. Berlin",
    "units": "string?: metric or imperial, default metric",
})
def get_weather(city, units="metric"):
    data = http("GET", "https://wttr.in/" + city, params={"format": "j1"})   # parsed JSON
    now = data["current_condition"][0]
    t = now["temp_C"] if units == "metric" else now["temp_F"]
    return "%s°%s, %s" % (t, "C" if units == "metric" else "F", now["weatherDesc"][0]["value"])

srv.run()   # the last line: serve until Relay stops the program
```

Plug it in and try it:

```
relay mcp add weather --command python3 --yes -- ~/my-server/server.py
relay mcp test weather
```

The API in short (the full text is in the built-in skill: `/skills`, or `internal/builder/sdk/relay_mcp.py`):

- `@srv.tool(name, description, params)`: `params` is `{argument: "type"}` with the types `string`, `integer`, `number`,
  `boolean`, `array`, `object`. `"integer?"` makes it optional (give the Python argument a default).
  `"string: some words"` adds a description for the model. A dict is a JSON Schema fragment
  (`{"type": "string", "enum": ["a", "b"], "optional": True}`).
- return text, a number, a list or a dict (shown as JSON); `None` means "ok";
- `raise ToolError("what went wrong and what to try")` for a problem the model should read;
- `http(method, url, params=…, body=…, form=…, headers=…, bearer=…, oauth=False)` returns parsed JSON or text, with
  timeouts and clear errors, and refuses redirects to another host;
- `secret("NAME")` reads an environment variable of the server (with a clear error when it is empty);
- `oauth_token()` / `http(..., oauth=True)` use the signed-in user's token;
- `log("text")` writes to stderr. **Never `print()`**: stdout is the protocol.

### Writing a server in another language

An MCP server over stdio is a program that reads **one JSON message per line** on stdin and writes one per line on stdout
(JSON-RPC 2.0). stderr is for logs: Relay shows the last line when the program fails to start. When stdin closes, exit.

The whole conversation of a minimal server (`>` is what Relay sends, `<` what you answer):

```
> {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"relay","version":"0.1.0"}}}
< {"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"demo","version":"1.0.0"}}}
> {"jsonrpc":"2.0","method":"notifications/initialized"}
> {"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}
< {"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"add","description":"Add two numbers.","inputSchema":{"type":"object","properties":{"a":{"type":"number"},"b":{"type":"number"}},"required":["a","b"]}}]}}
> {"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"add","arguments":{"a":2,"b":3}}}
< {"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"5"}]}}
```

Rules:
- answer `initialize` with a protocol version you support (Relay understands `2025-06-18`, `2025-03-26`, `2024-11-05`) and
  `"capabilities":{"tools":{}}`;
- notifications (no `id`) get no answer; answer `ping` with `{}`; answer an unknown method with error `-32601`;
- a tool that fails *in a way the model should read* answers normally with `"isError": true` and the text in `content`
  (not a JSON-RPC error: that is for protocol mistakes);
- the description of a tool and of its arguments is all the model knows about it: write them for the model.

Then register it like any program: `"command": "node", "args": ["/path/server.js"]`.

### Remote servers

A remote server speaks "Streamable HTTP": Relay POSTs JSON-RPC to the URL and gets JSON or an event stream back.
Put the address (and `headers`, if it wants a key) into the entry. If the server asks the user to sign in, Relay finds out
how by itself (the MCP authorization specification: discovery, dynamic client registration, PKCE) and opens the browser
(`/connectors auth <id>` or `relay mcp connect <id>`). Redirects are never followed, so a key cannot be sent to another host.

### Secrets and the environment

- A local program gets **a small environment**: `PATH`, `HOME`, language and proxy settings, certificate paths, and the variables of your
  desktop session (`DISPLAY`, `WAYLAND_DISPLAY`, `XAUTHORITY`, `DBUS_SESSION_BUS_ADDRESS`, so that it can open a window or a browser). Relay's own
  API keys are **not** passed on. Anything else it needs goes into `env`.
- Never write a secret into `mcp.json`, into code, or into the chat. Write `"env": {"GITHUB_TOKEN": "${GITHUB_TOKEN}"}`
  and export the variable in your shell: the value is taken from there when the program starts.
- Read it in the program with `secret("GITHUB_TOKEN")` (Python) or the normal way of your language.

### OAuth: signing the user in

Use an `oauth` block when the service has a normal REST API with OAuth sign-in (most do). Relay then does everything:
the browser page, the check of the answer, the token, and refreshing it. Your program never sees the refresh token or the
client secret. It reads the **access token** from the file named in the environment variable `RELAY_OAUTH_TOKEN_FILE`
(JSON: `{"access_token": "…", "expires_at": 1767225600}`); Relay refreshes the token before every call.

| `oauth` field | |
|---|---|
| `authorize_url`, `token_url` | The addresses of the service (https only). |
| `client_id` | The id of **your OAuth app** at the service (not a secret). |
| `client_secret` | If the service gives one: write `"${ENV_NAME}"`. |
| `scopes` | List of scopes. |
| `params` | Extra parameters of the authorize address, e.g. `{"access_type": "offline"}` for Google refresh tokens. |

Steps:
1. Register an OAuth app in the developer settings of the service. As the **callback (redirect) address** give exactly the
   one that `/connectors` prints (`http://127.0.0.1:<port>/oauth/callback`; the port is the port Relay listens on, so keep
   `listen` fixed in `config.toml` if the service wants a fixed address).
2. Put `client_id` in `mcp.json`, and the secret into an environment variable.
3. Sign in: `/connectors auth <id>` or `relay mcp connect <id>`. Or do nothing: the first time the model calls a tool of
   the connector, the answer **pauses**, the page opens, and the work goes on after you signed in.
4. In the program use `http(..., oauth=True)`. If the service answers 401, `http` tells Relay (the message starts with
   `relay:auth-required`): Relay drops the token and asks you to sign in again. Other languages can do the same: start the
   text of the tool error with `relay:auth-required`.

Not supported yet: services that want the client secret in an HTTP Basic header at the token address.

### Testing and debugging

- `relay mcp test <id>` connects once and lists the tools (it asks before running a program you have not approved).
- `relay mcp list` shows the state and, for a failed server, the reason (the last line the program wrote to stderr).
- Run the program by hand and type the messages from [above](#writing-a-server-in-another-language): it answers on stdout.
- `disabled_tools` switches single tools off without removing the server.
- Interface errors are written to `crash.log` in the Relay folder.

## Skills

### The format

A skill is a folder named like the skill, with a `SKILL.md`. It is the open **Agent Skills** format, so skills written for
other tools work here and the other way round.

```
release-notes/
├── SKILL.md              required
├── references/style.md   optional: read only when the instructions point to it
└── scripts/collect.py    optional: never run automatically
```

```markdown
---
name: release-notes
description: Write release notes from a list of changes. Use when the user asks for release notes, a changelog or a summary of what changed.
---

# Release notes

1. Group the changes: Added, Changed, Fixed.
2. One line per change, in the present tense, without ticket numbers.
3. For the tone see references/style.md.
```

| Header field | |
|---|---|
| `name` | Required. Up to 64 characters: lowercase letters, digits, single hyphens. **Must equal the folder name.** |
| `description` | Required. Up to 1024 characters. What the skill does **and when to use it**. |
| `license`, `compatibility` | Optional text. |
| `metadata` | Optional `key: value` lines. |
| `allowed-tools` | Optional; informational in Relay (it never lets a skill skip a question). |

Check a skill with `relay skill validate [name | folder]`.

### How Relay uses a skill

Skills are loaded in steps, so that many of them do not fill the context:

1. In agent mode the system prompt lists only **the name and the description** of every enabled skill. The description is
   therefore the most important line you write: it is how the model decides to use the skill.
2. When a task matches, the model calls `load_skill`, and gets the full text of `SKILL.md`.
3. The files that the text mentions (`references/…`, `assets/…`, `scripts/…`) are read with `read_skill_file`, only when
   needed. They must be text and stay inside the skill folder.

A skill is **advice, not a rule**: it cannot give the model permissions or override what the user says, and scripts in it are
never run automatically (if a step needs one, write the exact command; the user is asked before it runs).

Tips: 10–40 lines in `SKILL.md`; put long material in `references/`; be concrete (steps, formats, examples); no secrets.

### Installing, switching off, removing

```
relay skill add ./release-notes      # copy a folder in (checks it first)
relay skill list
relay skill info release-notes
relay skill disable release-notes    # the agent no longer sees it
relay skill enable release-notes
relay skill remove release-notes
```

You can also just copy the folder into `~/.config/relay/skills/`; it appears within seconds. The skills that come with Relay
(`relay-builder`, which teaches the agent to build tools) can be switched off but not removed; a skill of the same name in the
folder replaces a built-in one.

### A skill for every server

For each server write a skill that says **when to use its tools and how**. Name it like the server and put it in `skills/`:

```markdown
---
name: github
description: Read GitHub issues and pull requests and create issues. Use when the user asks about their repositories, issues, pull requests, or wants to report a bug on GitHub.
---

# GitHub

- To see what needs attention, call `mcp__github__list_issues` (open issues only; pull requests are not included),
  then `mcp__github__get_issue` for the ones that matter.
- `owner` and `repo` are the two parts of `owner/repo`. If the user gives only a repo name, ask whose it is.
- `mcp__github__create_issue` writes to GitHub: show the title and text to the user first.
- Private repositories need the user to have signed in (`/connectors auth github`).
```

When the agent builds a server it writes this skill itself, marks it with `metadata: {created-by: relay-builder, server: <id>}`,
adds the list of the tools, and removes the skill together with the server. A skill you write by hand has no such link:
remove it yourself.

## A checklist for a good connector

- Few tools (3–8), one job each, names like `list_issues`, `get_file`. Reading tools first; **writing tools separate** and
  marked as such in the description ("WRITES: creates an issue").
- Descriptions say what the tool does, what it returns, and what it does not.
- Arguments are checked (names, numbers, paths); a file tool stays inside one folder and refuses `..`.
- Results are short: a `limit` argument, cut long texts. The model reads every character you return.
- Errors say what to do next (`ToolError("repository not found; check owner and repo")`).
- Timeouts on everything that waits.
- No secrets in code; credentials from `env`, the token from `oauth`.
- A skill that says when to use it.

## Security: what Relay does and does not do

- A local program runs **only after you approved that exact command**, with your rights (so write or read programs you trust).
- A program gets a **small environment** (see above; it includes your desktop session), never Relay's API keys.
- **Every call of an MCP tool asks you first** (you can say "always" for a tool in one session). The question "may the agent build
  this?" is asked every time, and "always" is not remembered for it or for removing things.
- The approval box for a new program shows a summary found by reading its code (the web addresses it contacts, whether it runs
  programs, whether it writes or deletes files). It is a hint, not a guarantee: read the code when it matters
  (`~/.config/relay/connectors/<id>/server.py`).
- OAuth: the check of the `state`, PKCE, https-only addresses, no redirects to other hosts; tokens are in files that only you can
  read (not in the system keychain); a connector only ever gets the access token.
- Skills cannot run anything, and cannot override your instructions.
- Relay's own web address (`127.0.0.1`) accepts requests only with its token; the sign-in callback is protected by the `state`
  of the flow.

## Troubleshooting

| Problem | What to do |
|---|---|
| `waits for your approval` | `relay mcp trust <id>` (it shows the command first). |
| `"python3" was not found` | Install Python 3, or give the full path in `command`. |
| `the program exited: …` | That is the last line the program wrote to stderr. Run it by hand to see more. |
| `auth_required` / `sign in` | `/connectors auth <id>`. If the browser does not open, Relay prints the address. |
| The service says "redirect URI mismatch" | The callback of the OAuth app must be exactly the address `/connectors` prints (same port). |
| `mcp.json has a problem` | Relay ignores the file until it is valid; `relay mcp list` prints the error. |
| A skill does not show up | `relay skill validate`: the name must equal the folder name, and `description` is required. |
| The model does not use my tool | Improve the description of the tool and the skill: say **when** to use it. |
| A tool cannot open a browser or a window | The program needs your desktop session. Relay starts its background service from the terminal where you run it: if the service was started elsewhere (ssh, autostart) it has no `DISPLAY`; run `relay stop`, then `relay` in a terminal of the graphical session. |
| A token variable is "not set" although you exported it | A running Relay does not see variables set later: `relay stop`, then start Relay in the shell where the variable is set. |
| The interface is slow or shows garbage | Look at `crash.log`; update Relay and tell us. |
