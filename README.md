<h1 align="center">Relay</h1>

<p align="center">
  <b>One terminal for every AI model: local and cloud, with a coding agent and a mascot that lives in your TUI.</b>
</p>

<p align="center"><i>Faster. Closer. Together.</i></p>

<p align="center">
  <img alt="Go 1.22+" src="https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go&logoColor=white">
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
- **Doesn't stop at limits.** Fallback along the model chain on errors and rate limits, several API keys per provider with automatic rotation, token and cost tracking.
- **Local and cloud side by side.** Start Ollama or llama.cpp from inside Relay with a readiness check, compare answers from several models with one command.
- **A coding agent with guardrails.** A "model → tool → result" loop with file, search, shell, web and MCP tools. Limited to the working folder; writes, shell and MCP always ask first.
- **Your own gateway.** The backend speaks the OpenAI API, so any other program can work through Relay.
- **A mascot.** It reacts to what is happening (tool call, error, answer, long silence) and is repainted by a wave when you change mode.

## Installation

There are no releases yet, so Relay is installed by building it from source. It takes about a minute and works the same on Linux and Windows.

### Requirements

| Tool | Needed for | Linux | Windows (PowerShell) |
|------|------------|-------|----------------------|
| **Go 1.22+** | building Relay | [go.dev/dl](https://go.dev/dl/) or your package manager | `winget install GoLang.Go` |
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

Press `ctrl+a` to turn agent mode on. The agent reads, searches, writes and edits files, runs shell commands, browses the web and calls tools of connected MCP servers (stdio).

- File access is limited to the working folder.
- Writing, shell and MCP calls always ask for confirmation.
- The shell is **not sandboxed**: read commands before you confirm them.

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
internal/mcp       MCP client (stdio)
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
- The agent is limited to the working folder; writing, shell and MCP always ask for confirmation.
- The shell is not sandboxed.

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

Relay is **pre-release software** (version 0.6). There is no stable version yet and stable releases are still a long way off, so expect rough edges and breaking changes. Notes, decisions and the plan are in [PROJECT.md](PROJECT.md) (in Russian).

Found a bug or have an idea? Please open an [issue](https://github.com/badlinuxuser/relay/issues).

## License

See [LICENSE](LICENSE).
