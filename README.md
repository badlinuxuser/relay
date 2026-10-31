# Relay

**Relay** is a terminal launcher and router for AI models: local models and cloud APIs in one interface,
a coding agent, and a mascot that lives in the interface.

> Faster. Closer. Together.

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

Relay is a single executable, `relay`. The interface and the backend run as two processes: the interface
starts the backend in the background by itself (`relay daemon`). The interface knows nothing about providers,
the backend does everything, so any other client can be written against the same API
(see [docs/PROTOCOL.md](docs/PROTOCOL.md)).

## Features

- **Providers:** the model makers' own APIs (Anthropic, OpenAI, Gemini, Grok, DeepSeek) and also Ollama
  (local and cloud), llama.cpp, LM Studio, vLLM and OpenRouter. Answers are streamed.
- **Models are found automatically.** Relay asks each provider what it has (what is installed in Ollama, what
  your Ollama cloud, Gemini, ... keys give access to) and lists it in `/models`. No model names to type in a
  config file. Providers without access add nothing to the list.
- **Modes small / medium / hard / maximum:** each sets a chain of models, the colour of the interface and the
  mascot (maximum: rainbow). Switch with `ctrl+t`.
- **Routing:** the mode's model chain with fallback on limits and errors; several keys per provider with
  automatic rotation; token and cost tracking.
- **Launcher:** start and stop local servers (`/start ollama`) with a readiness check.
- **Coding agent:** a "model → tool → result" loop: read, search, write and edit files, shell, web, and tools of
  MCP servers. Files are limited to the working folder; dangerous actions ask for confirmation.
- **Sessions:** history is saved to disk; change model or mode in the middle of a conversation; system prompt presets.
- **Compare:** `/compare model1 model2 text` sends one prompt to several models at once.
- **Gateway:** the backend exposes an OpenAI-compatible endpoint, so other programs can work through Relay.
- **Help in the browser:** `/help` opens a full guide; `/keys` opens the API keys page.

## Quick start

Requires Go 1.22+.

```bash
make install        # build and add to PATH (Linux and Windows)
relay               # run from any folder: the agent works in the current folder
```

Without make: `go build -o relay ./cmd/relay && ./relay install`. `relay install` copies the binary to
`~/.local/bin` (Linux) or `%LOCALAPPDATA%\Programs\relay` (Windows) and adds that folder to PATH
(`~/.bashrc`, `~/.zshrc`, fish, or the Windows user PATH). Open a new terminal afterwards.

Then, inside Relay:

1. `/keys` opens a page in your browser where you paste API keys (local models need none).
2. `/models` opens the model list: pick one with `↑` `↓` `enter`, or stay on `auto`.
3. Type a message. `ctrl+a` turns on agent mode.
4. `/help` opens the full guide.

### API keys

`relay keys` (or `/keys`) opens a page on localhost where you can add one or more keys for each provider.
Keys are stored in `keys.json` (owner-only permissions), persist between sessions and apply immediately.
After a key is saved, the page asks the provider which models the key opens and shows the number, or the
provider's error, so you know the key works. Environment variables are not needed (but supported as an option:
`api_key_env` in the config).

| Provider | Get a key |
|---|---|
| Anthropic | https://console.anthropic.com/settings/keys |
| OpenAI | https://platform.openai.com/api-keys |
| Gemini | https://aistudio.google.com/apikey |
| Grok (xAI) | https://console.x.ai |
| DeepSeek | https://platform.deepseek.com/api_keys |
| Ollama (cloud models) | https://ollama.com/settings/keys |
| OpenRouter | https://openrouter.ai/keys |

Local servers (Ollama, llama.cpp) need no key. A key saved for `ollama` also unlocks Ollama cloud models.

### Local models

Install Ollama, pull a model (`ollama pull granite4.1:3b`) and, if Ollama is not already running, type
`/start ollama`. The installed models show up in `/models` as `ollama/<name>`.

## Choosing a model: `/models`

Type `/models` and a list opens above the input line (`↑` `↓` select, `tab` fill, `enter` use, `esc` close).
Text after the command filters the list: `/models gemini`, `/models cloud`.

- Model ids are `<provider>/<model>`: `ollama/granite4.1:3b`, `ollama-cloud/gpt-oss:120b`, `gemini/gemini-2.5-flash`.
- **Order:** the model makers' own APIs first (Anthropic, OpenAI, Gemini, Grok, DeepSeek), then the rest
  (Ollama, Ollama cloud, llama.cpp, OpenRouter, ...).
- **Only what you can use:** a provider without a key, with a rejected key, or a server that is not running
  adds nothing. API errors are not shown in the terminal; the keys page shows them next to the provider.
- `auto` returns the choice to the current mode. The list is cached for a minute and refreshed immediately
  when a key is added or deleted.
- A model of a local server in the mode chain that is not installed (for example `local-small` in the default
  config) is replaced by the models you have installed. Models found automatically have no price, so their
  cost shows as $0; tokens are still counted.

## CLI commands

| Command | What it does |
|---|---|
| `relay` | the terminal interface (the backend starts by itself) |
| `relay keys` | page for adding and storing API keys |
| `relay status` | providers, keys, processes, usage |
| `relay stop` | stop the background backend |
| `relay daemon` | the backend in this terminal (for debugging) |
| `relay install` / `uninstall` | add to PATH or remove |
| `relay config [--example]` | config path, or an example config |

Data files: Linux `~/.config/relay/`, Windows `%AppData%\relay\` (`config.toml`, `keys.json`, `sessions/`,
`usage.json`, `token`). Cross-build: `make dist` (Linux and Windows).

## Controls

| Key | Action |
|---|---|
| `enter` | send the message |
| `ctrl+t` | next mode (small → medium → hard → maximum) |
| `ctrl+a` | agent mode on / off |
| `ctrl+n` | new session |
| `esc` | stop the running request (closes the model list when it is open) |
| `pgup` / `pgdn`, mouse wheel | scroll |
| `ctrl+c` | quit |

Commands: `/help`, `/keys`, `/models`, `/mode` (or `/small` `/medium` `/hard` `/max`), `/agent`, `/preset`,
`/compare`, `/new`, `/sessions`, `/load`, `/start`, `/stop`, `/clear`, `/quit`. Details are in `/help`.

## Mascot

Five emotions from a sprite sheet and four colour themes (small: green, medium: purple, hard: red,
maximum: rainbow).

| Event | Emotion |
|---|---|
| idle | calm |
| tool call, key switch | noticed |
| error, approval needed, sudden wake-up | surprise |
| answer received, mode change, start | joy |
| key limit reached, long inactivity | tired (sleeping) |

A mode change is animated: the interface colours blend smoothly and the mascot is "repainted" by a wave with
a bright dividing line. Sprites are built from `assets/sheets/*.png` by `tools/sprites/build.py`
(`make sprites`): crisp 44x36 pixels, two pixels per terminal cell (the `▀` character). The rainbow theme is
computed from the green sheet at runtime.

## OpenAI-compatible gateway

```bash
TOKEN=$(cat ~/.config/relay/token)     # Windows: %AppData%\relay\token
curl http://127.0.0.1:8787/v1/chat/completions \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"model":"relay-medium","messages":[{"role":"user","content":"Hello"}]}'
```

`model`: `relay-small|medium|hard|maximum`, `auto`, a model id from the config, or any `<provider>/<model>`
from `/models`. `GET /v1/models` lists them all.

## Security

- The API listens on `127.0.0.1` only, requires a token (`<home>/token`, mode 0600) and checks the Host
  header (DNS rebinding protection). The keys page gets the token from the URL fragment, which is never sent
  to the server.
- Keys in `keys.json` are stored as plain text (mode 0600); the interface and API show only the last 4 characters.
- The agent is limited to the working folder; writing, shell and MCP always ask for confirmation.
- The shell is not sandboxed: read commands before you confirm them.

## Layout

```
cmd/relay          the only executable: TUI, daemon, keys, status, stop, install, config
internal/daemon    backend startup (relay daemon)
internal/config    configuration (TOML), provider and model lookup
internal/catalog   finds the models each provider has (Ollama, Gemini, ...), cache
internal/keystore  API key storage (keys.json)
internal/provider  openai and anthropic adapters, key rotation
internal/router    modes, model chains, fallback, usage accounting
internal/agent     the agent loop
internal/tools     agent tools (files, shell, web, MCP)
internal/mcp       MCP client (stdio)
internal/runner    launcher for local processes
internal/session   conversation history
internal/server    HTTP API, SSE, /v1 gateway, keys page (ui/keys.html), help page (ui/help.html)
internal/client    API client, background backend startup
internal/browser   opens URLs in the browser
internal/tui       interface, model list, mascot, themes
tools/sprites      sprite converter
assets/sheets      source sprite sheets
```

## Status

Version 0.5. Notes, decisions and the plan are in [PROJECT.md](PROJECT.md) (in Russian).
