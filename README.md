<h1 align="center">Relay</h1>

<p align="center">
  <b>One terminal for every AI model: local and cloud, with a coding agent and a mascot that lives in your TUI.</b>
</p>

<p align="center"><i>Faster. Closer. Together.</i></p>

<p align="center">
  <img alt="Go 1.24.2+" src="https://img.shields.io/badge/Go-1.24.2%2B-00ADD8?logo=go&logoColor=white">
  <img alt="Status: pre-release" src="https://img.shields.io/badge/status-pre--release-orange">
  <img alt="Platforms" src="https://img.shields.io/badge/platform-Linux%20%7C%20Windows-lightgrey">
</p>

<!--
TODO: put a demo GIF here (record with `vhs` or asciinema + agg):
startup, ctrl+t mode switch with the colour wave, an answer, the agent running a tool.
<p align="center"><img src="docs/demo.gif" alt="Relay demo" width="800"></p>
-->

**Relay** is a terminal launcher and router for AI models. Local models (Ollama, llama.cpp, LM Studio, vLLM) and cloud APIs (Anthropic, OpenAI, Gemini, Grok, DeepSeek, OpenRouter) live in one interface, with a coding agent that can use MCP tools and an OpenAI-compatible gateway for your own scripts. It is a single Go binary.

> [!WARNING]
> **Built with AI.** Relay is developed with the help of AI. It is a young project in active development: there is no stable release yet, behaviour and configuration may change between commits, and bugs are likely. Please review the code before you trust it with your API keys, and read every agent command before you confirm it. Bug reports and feedback are very welcome!

## Why Relay

- **No model names in config files.** Relay asks each provider what you actually have access to and lists it in `/models`.
- **Four modes, one keystroke.** `small → medium → hard → maximum`: each mode is a chain of models with its own colour theme. Press `ctrl+t` to switch.
- **Doesn't stop at limits.** When a model fails (a limit, no credit, a server error) Relay offers the next model of the same provider, and tells you if the whole provider is down; several API keys per provider with automatic rotation, token and cost tracking.
- **Local and cloud side by side.** Start Ollama or llama.cpp from inside Relay with a readiness check, compare answers from several models with one command.
- **A coding agent with guardrails.** A model → tool → result loop with file, search, shell, web and MCP tools. Workspace file changes, shell commands, MCP calls and other high-impact actions require a fresh confirmation for each invocation.
- **Your own gateway.** The backend speaks the OpenAI API, so any other program can work through Relay.
- **A mascot.** It reacts to what is happening (tool call, error, answer, long silence) and is repainted by a wave when you change mode.

## Installation

Relay 0.2.1 is a pre-release and is currently installed by building it from source. It takes about a minute and works the same on Linux and Windows.

### Requirements

| Tool | Needed for | Linux | Windows (PowerShell) |
|------|------------|-------|----------------------|
| **Go 1.24.2+** | building Relay | [go.dev/dl](https://go.dev/dl/) or your package manager | `winget install GoLang.Go` |
| **Git** | downloading the code | `sudo apt install git` | `winget install Git.Git` |
| **make** *(optional)* | the one-command install | see below | see below |

Check that Go is ready: `go version`. After installing anything on Windows, **open a new terminal** so that the updated `PATH` is picked up.

### Option 1: with `make`

**Linux.** `make` is often preinstalled. If not:

```sh
sudo apt install make       # Debian, Ubuntu
sudo dnf install make       # Fedora
sudo pacman -S make         # Arch
```

**Windows.** Install `make` with any one of these package managers:

```powershell
winget install ezwinports.make -e     # winget (built into Windows 10/11)
choco install make                    # Chocolatey
scoop install make                    # Scoop
```

Then open a new terminal and check it with `make --version`. If `make` complains about a missing shell, install [Git for Windows](https://git-scm.com/download/win) as well: its `sh.exe` is what `make` uses to run recipes.

**Then, on both systems:**

```sh
git clone https://github.com/badlinuxuser/relay.git
cd relay
make install
```

`make install` builds Relay and adds it to your `PATH`. Open a new terminal and run `relay`.

### Option 2: with `go` only

No `make` needed.

**Linux:**

```sh
git clone https://github.com/badlinuxuser/relay.git
cd relay
go build -o relay ./cmd/relay
./relay install
```

**Windows (PowerShell):**

```powershell
git clone https://github.com/badlinuxuser/relay.git
cd relay
go build -o relay.exe ./cmd/relay
.\relay.exe install
```

`relay install` copies the binary to `~/.local/bin` (Linux) or `%LOCALAPPDATA%\Programs\relay` (Windows) and adds that folder to your `PATH` (`~/.bashrc`, `~/.zshrc`, fish, or the Windows user `PATH`). Open a new terminal afterwards.

**Even shorter, without cloning** (Linux and Windows):

```sh
go install github.com/badlinuxuser/relay/cmd/relay@latest
```

The binary lands in `$(go env GOPATH)/bin` (`~/go/bin` on Linux, `%USERPROFILE%\go\bin` on Windows). Make sure that folder is on your `PATH`.

### Updating and removing

To update, run `git pull` in the cloned folder and repeat the install command. To remove Relay from your `PATH`, run `relay uninstall`.

## Quick start

Run `relay` from any folder (the agent works in the current folder), then:

1. `/keys` opens a page in your browser where you paste API keys. Local models need none.
2. `/models` opens the model list: pick one with `↑` `↓` `enter`, or stay on `auto`.
3. Type a message. `ctrl+a` turns on agent mode.
4. `/help` opens the full guide in the browser.

### Local models

Install [Ollama](https://ollama.com), pull a model (`ollama pull granite4.1:3b`) and, if it isn't already running, type `/ollama`. Installed models appear in `/models` as `ollama/<name>`. For llama.cpp: `/llamacpp path/to/model.gguf`.

Relay never stops servers it did not start. If Ollama was started by its tray app, quit it from the tray icon.

### API keys

`relay keys` (or `/keys`) opens a page on localhost where you add one or more keys per provider. After saving, the page asks the provider which models the key opens and shows the number (or the provider's error), so you know the key works. Keys are stored in `keys.json` (owner-only permissions). Environment variables also work via `api_key_env` in the config.

| Provider              | Get a key                                       |
|-----------------------|-------------------------------------------------|
| Anthropic             | https://console.anthropic.com/settings/keys     |
| OpenAI                | https://platform.openai.com/api-keys            |
| Gemini                | https://aistudio.google.com/apikey              |
| Grok (xAI)            | https://console.x.ai                            |
| DeepSeek              | https://platform.deepseek.com/api_keys          |
| Ollama (cloud models) | https://ollama.com/settings/keys                |
| OpenRouter            | https://openrouter.ai/keys                      |

## Modes

| Mode      | Theme   | Command   |
|-----------|---------|-----------|
| `small`   | green   | `/small`  |
| `medium`  | purple  | `/medium` |
| `hard`    | red     | `/hard`   |
| `maximum` | rainbow | `/max`    |

Each mode defines a chain of models. If the first one fails or hits a limit, Relay moves to the next. Models of a local server that aren't installed are replaced by the ones you do have.

## Choosing a model

Type `/models` and a list opens above the input line (`↑` `↓` select, `tab` fill, `enter` use, `esc` close). Text after the command filters it: `/models gemini`, `/models cloud`.

- Model ids look like `<provider>/<model>`: `ollama/granite4.1:3b`, `ollama-cloud/gpt-oss:120b`, `gemini/gemini-2.5-flash`.
- Only what you can use is shown: a provider without a key, with a rejected key, or a server that isn't running adds nothing.
- `auto` returns the choice to the current mode.
- Automatically found models have no price, so their cost shows as $0; tokens are still counted.

## Coding agent

Press `ctrl+a` to turn agent mode on. The agent reads, searches, writes and edits files, runs shell commands, browses the web and calls tools of the MCP servers you connected and uses your skills (see below).

- File access is limited to the working folder.
- Every MCP tool invocation requires a fresh confirmation; session-wide “Always allow” does not apply to MCP.
- Shell commands, workspace writes/edits, MCP creation/validation and Skill creation also require fresh confirmation for each invocation.
- The shell and local stdio MCP processes are **not sandboxed** and run with your operating-system user permissions. Review commands and connector source before approving them. See [SECURITY.md](SECURITY.md).

## MCP servers and skills

Relay can use any MCP server and any skill with any model, local or cloud. Everything is plain files in the Relay config folder (`relay config` shows where): `mcp.json` for servers, `skills/` for skills.

```
relay mcp add files --command npx -- -y @modelcontextprotocol/server-filesystem /home/me/projects
relay mcp add docs --url https://example.com/mcp
relay mcp list                    # state of every server
relay mcp info files              # details of one server
relay mcp validate                # check every server definition and Relay-created source file
relay mcp test files              # connect once and show the tools
relay mcp remove files            # asks before removing; use --yes for scripts
relay mcp trust files             # approve a program that you added by editing mcp.json

relay skill add ./my-skill        # a folder with a SKILL.md (the open Agent Skills format)
relay skill list | info | validate | enable | disable | remove
```

- `mcp.json` can be edited by hand; Relay applies the change without a restart, and keeps the last good version if you make a mistake. Relay-created server entry points are watched too: deleting one removes that server from the active list. The Claude Desktop / Cursor key `mcpServers` is read too.
- A local server is a program that Relay would run for you, so it runs **only after you approved that exact command** (`relay mcp add` asks; for a hand-edited entry run `relay mcp trust <id>`). It gets a small environment (no API keys of Relay); put what it needs into `"env"`, and write `${NAME}` to take a value from your own environment instead of storing a secret in the file.
- Every MCP tool call still asks for confirmation in the agent. Single tools can be switched off with `"disabled_tools"`.
- Skills show up in the agent as a short list; the agent loads a skill only when the task matches it, and a skill's scripts are never run automatically.
- Not yet: a page in the settings UI for adding servers by hand, installing from Git, services that want the client secret in an HTTP Basic header at the token address.

### Let the model build them

Relay can extend itself: turn the agent on (`ctrl+a`) and ask, for example, *"connect my GitHub"* or *"make a tool that keeps notes in a folder"*. This works with **any model that can call tools**, local or cloud. Relay has the tools for it built in (`create_mcp_server`, `create_skill`, `list_extensions`, `authorize_connector`, `remove_extension`) and a built-in skill, `relay-builder`, that explains to the model how Relay is organised and how to write a server with a small Python helper (`relay_mcp.py`, which Relay puts next to every server it creates). Nothing to install: it is inside the program. The builder first proposes the extension, reads its built-in guide and SDK reference, validates local MCP projects in a temporary directory using syntax checks plus Relay runtime handshake/tool discovery, and only then installs the server. Optional PyPI dependencies are installed into a private `.venv` per server; URLs, VCS references, local paths and pip options are rejected. Python 3 is required for building and running Python MCP servers.

A server the model builds is a **tool** (works on this computer) or a **connector** (works with an outside service); `/tools`, `/connectors` and `/skills` list them.

- **A skill always comes with it.** `create_mcp_server` refuses to work without a skill that tells the model when and how to use the new tools; Relay adds the exact list of the tools to it, and removes the skill together with the server.
- **It always asks first.** If you ask for something that no tool or skill can do, the agent asks whether to build one: *"Relay has no connector for this: send messages to your Telegram. Build one now?"* This works for direct requests (*"make a tool that ..."*) and indirect ones (*"send this to my Telegram"*, *"what is on my calendar tomorrow"*, *"open YouTube and search for cats"*). **If you refuse, the whole request is cancelled.** Then the agent must read how Relay works before writing anything. Running the resulting MCP server requires a separate approval; every MCP tool invocation also requires its own fresh approval. Session-wide “Always allow” is not available for MCP calls. You can switch the offers off in the settings (*Offer to build what is missing*).
- **It repairs what it built.** If a tool of this kind fails because of its own code or setup, the agent asks for permission to fix it, reads its code (`read_server`), and replaces it, instead of giving up.
- **One line instead of endless thoughts.** While the agent builds, you see a single line right under your question: what is going on, the time and the stage. The model's reasoning, its calls and what it says on the way are not shown; the final answer is. If you prefer to see everything, set *While the agent builds* to *Thoughts* in the settings, or use `/progress off`.

To make servers and skills by hand see **[docs/EXTENSIONS.md](docs/EXTENSIONS.md)**: the files, every field of `mcp.json`, writing a server in Python (`relay mcp sdk`) or in any language, OAuth, skills, and a checklist.

### OAuth sign-in

A connector may use OAuth. Relay does the whole sign-in: it opens the page in your browser, receives the answer on its own callback address (`http://127.0.0.1:<port>/oauth/callback`, shown by `/connectors`; register exactly this address in the OAuth app of the service), stores the token in the `auth` folder (owner only) and refreshes it. A connector program never sees the refresh token or the client secret, only the current access token. Remote MCP servers that follow the MCP authorization specification (discovery, dynamic client registration, PKCE) need no setup.

If the model calls a tool of a connector and you have not signed in, the answer **pauses**, Relay opens the sign-in page, and the work continues after you have signed in (esc cancels). `/connectors auth <id>` or `relay mcp connect <id>` signs in without waiting for the model. The port of the callback is the port Relay listens on: keep `listen` fixed in the config if a service needs a fixed callback address.

## Compare models

```
/compare model1 model2 your prompt here
```

Sends one prompt to several models at once.

## OpenAI-compatible gateway

```sh
TOKEN=$(cat ~/.config/relay/token)     # Windows: %AppData%\relay\token
curl http://127.0.0.1:8787/v1/chat/completions \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"model":"relay-medium","messages":[{"role":"user","content":"Hello"}]}'
```

`model` can be `relay-small|medium|hard|maximum`, `auto`, a model id from the config, or any `<provider>/<model>` from `/models`. `GET /v1/models` lists them all.

## Commands and keys

| Key                          | Action                                                  |
|------------------------------|---------------------------------------------------------|
| `enter`                      | send the message                                        |
| `ctrl+t`                     | next mode                                               |
| `ctrl+a`                     | agent mode on / off                                     |
| `ctrl+n`                     | new session                                             |
| `esc`                        | stop the running request / close the model list         |
| `pgup` / `pgdn`, mouse wheel | scroll                                                  |
| `ctrl+c`                     | quit                                                    |

Everything in lists is clickable: models, sessions, running servers, agent approval.

Slash commands: `/help`, `/keys`, `/models`, `/mode` (`/small` `/medium` `/hard` `/max`), `/agent`, `/preset`, `/compare`, `/new`, `/sessions`, `/load`, `/ollama`, `/llamacpp`, `/stop`, `/clear`, `/quit`.

| CLI                           | What it does                                          |
|-------------------------------|-------------------------------------------------------|
| `relay`                       | the terminal interface (the backend starts by itself) |
| `relay keys`                  | page for adding and storing API keys                  |
| `relay status`                | providers, keys, processes, usage                     |
| `relay stop`                  | stop the background backend                           |
| `relay daemon`                | the backend in this terminal (for debugging)          |
| `relay install` / `uninstall` | add to PATH or remove                                 |
| `relay config [--example]`    | config path, or an example config                     |

Data lives in `~/.config/relay/` (Linux) or `%AppData%\relay\` (Windows): `config.toml`, `keys.json`, `sessions/`, `usage.json`, `token`.

## How it works

```
┌────────────┐   HTTP + SSE    ┌───────────────────────────────────────────────┐
│  relay     │ ◄─────────────► │  relay daemon (same binary, background)       │
│  (TUI)     │   localhost     │  router · agent · processes · MCP · /v1 API   │
└────────────┘                 └───────┬───────────┬───────────┬───────────────┘
  any other client                     │           │           │
  (web, GUI, scripts)              Ollama /     Anthropic    OpenAI-compatible
                                  llama.cpp /   (keys with   (OpenAI, Gemini, Grok,
                                  LM Studio     rotation)    DeepSeek, OpenRouter ...)
```

Relay is a single executable. The interface starts the backend in the background by itself (`relay daemon`). The interface knows nothing about providers; the backend does everything, so any other client can be written against the same API. See [docs/PROTOCOL.md](docs/PROTOCOL.md).

<details>
<summary>Project layout</summary>

```
cmd/relay          the only executable: TUI, daemon, keys, status, stop, install, config
internal/daemon    backend startup (relay daemon)
internal/config    configuration (TOML), provider and model lookup
internal/catalog   finds the models each provider has, cache
internal/keystore  API key storage (keys.json)
internal/provider  openai and anthropic adapters, key rotation
internal/router    modes, model chains, fallback, usage accounting
internal/agent     the agent loop
internal/tools     agent tools (files, shell, web, MCP)
internal/mcp       MCP: manager for mcp.json, stdio and Streamable HTTP clients, approvals
internal/skills    Agent Skills: SKILL.md parser, manager, installer
internal/watch     change detection for mcp.json and skills/
internal/runner    launcher for local processes
internal/session   conversation history
internal/server    HTTP API, SSE, /v1 gateway, keys page, help page
internal/client    API client, background backend startup
internal/browser   opens URLs in the browser
internal/tui       interface, model list, mascot, themes
tools/sprites      sprite converter
assets/sheets      source sprite sheets
```

</details>

## Security

- The API listens on `127.0.0.1` only, requires a token (`<home>/token`, mode 0600) and checks the `Host` header (DNS rebinding protection). The keys page receives the token from the URL fragment, which is never sent to the server.
- Keys in `keys.json` are stored as plain text (mode 0600); the interface and API show only the last 4 characters.
- Workspace file writes/edits and shell commands require fresh confirmation. Every MCP invocation requires fresh confirmation; session-wide “Always allow” is not applied to MCP.
- Local stdio MCP processes and shell commands are not OS-sandboxed and run with the current user’s permissions. Review source and commands before approval; see [SECURITY.md](SECURITY.md).

## The mascot

Five emotions from a sprite sheet, four colour themes. Sprites are built from `assets/sheets/*.png` by `tools/sprites/build.py` (`make sprites`): crisp 44x36 pixels, two pixels per terminal cell (the `▀` character). The rainbow theme is computed from the green sheet at runtime.

| Event                                  | Emotion          |
|----------------------------------------|------------------|
| idle                                   | calm             |
| tool call, key switch                  | noticed          |
| error, approval needed, sudden wake-up | surprise         |
| answer received, mode change, start    | joy              |
| key limit reached, long inactivity     | tired (sleeping) |

## Status

Relay 0.2.1 hardens tool approvals and adds iterative MCP development: the agent can validate a temporary MCP project through Relay's real runtime before installing it, and can use optional dependencies in a private Python environment per server.

Relay is **pre-release software** (version 0.2.1). There is no stable version yet and stable releases are still a long way off, so expect rough edges and breaking changes. Project architecture and development decisions are in [PROJECT.md](PROJECT.md). Consolidated release history is in [CHANGELOG.md](CHANGELOG.md). Security boundaries and limitations are documented in [SECURITY.md](SECURITY.md).

Found a bug or have an idea? Please open an [issue](https://github.com/badlinuxuser/relay/issues).

## License

See [LICENSE](LICENSE).
