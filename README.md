# Relay

Relay is a terminal-native AI workstation for working with local and remote language models from a single interface. It combines a TUI client (`relay`) with a background daemon (`relayd`) that handles routing, model selection, sessions, tool execution, and an OpenAI-compatible API gateway.

The project is designed for developers who want to:

- use local models and cloud APIs in one place
- switch between model tiers quickly in the terminal
- give an AI coding agent access to files, shell commands, and MCP tools
- expose the same backend to other clients through an OpenAI-like API

The name reflects the goal: a relay between your terminal, your models, and your tooling.

> “Faster. Closer. Together.”

```text
┌──────────────┐   HTTP + SSE    ┌─────────────────────────────────────────────┐
│ relay        │ ◄─────────────► │ relayd                                     │
│ (TUI)        │                 │ router · agent · runners · MCP · /v1 gateway │
└──────────────┘                 └──────────────┬──────────────────────────────┘
   terminal UI                           │
                                         │
                                 ┌───────┴────────┬─────────────┬──────────────┐
                                 │                │             │              │
                            Ollama / llama.cpp  Anthropic  OpenAI-compatible
                            LM Studio / vLLM      (keys with rotation)   OpenAI, OpenRouter, Gemini
```

## Why Relay?

Relay is built for the common workflow where developers want to move quickly between:

- local inference for privacy and speed
- remote models for better reasoning or larger context
- AI-assisted coding in a constrained workspace
- external tools and scripts that can talk to the same backend

The backend and UI are intentionally split. The TUI does not talk directly to providers; it sends requests to the daemon, which is responsible for routing, session management, and tool execution.

## Features

- Model routing across local and remote providers
- Modes: `small`, `medium`, `hard`, and `maximum`
- OpenAI-compatible gateway at `/v1/chat/completions`
- Agentic coding loop with file read/write/edit, grep, shell, web fetch, and MCP support
- Session persistence on disk
- Automatic API key rotation and fallback behavior
- Local process launcher for services such as Ollama
- Terminal mascot / themed TUI experience

## Supported providers

Relay is designed to work with multiple backends, including:

- Ollama
- llama.cpp
- LM Studio
- vLLM
- OpenAI
- OpenRouter
- Gemini
- Anthropic

## Quick start

### Requirements

- Go 1.22+
- Optional: Ollama or another local model server

### Build

```bash
git clone https://github.com/ifuckingjoke/relay.git
cd relay
make build
```

This builds both binaries:

- `bin/relayd`
- `bin/relay`

### Run

```bash
make run
```

This launches the TUI. The daemon starts automatically if needed.

### Optional: run daemon only

```bash
make daemon
```

### Local model example

If you use Ollama, install a model and then start it from the app or via shell:

```bash
ollama pull qwen2.5-coder:7b
```

Then in the Relay UI, use a command such as:

```text
/start ollama
```

## Configuration

On first run, Relay creates a config file in the platform-specific app config directory.

- Linux: `~/.config/relay/`
- Windows: `%AppData%\relay\`

API keys are expected as environment variables. Example:

```bash
export ANTHROPIC_API_KEY=...
export ANTHROPIC_API_KEY_2=...   # fallback / rotation key
```

The daemon also keeps a local token for local API access.

## OpenAI-compatible gateway

Relay exposes an OpenAI-style endpoint for compatibility with tools and scripts.

```bash
TOKEN=$(cat ~/.config/relay/token)
curl http://127.0.0.1:8787/v1/chat/completions \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "relay-medium",
    "messages": [{"role": "user", "content": "Hello from Relay"}]
  }'
```

Available model aliases usually include values like:

- `relay-small`
- `relay-medium`
- `relay-hard`
- `relay-maximum`
- `auto`

## Common controls

| Key | Action |
| --- | --- |
| `Enter` | send message |
| `Ctrl+T` | switch mode |
| `Ctrl+A` | toggle agent mode |
| `Ctrl+N` | new session |
| `Esc` | stop current action |
| `PgUp` / `PgDn` | scroll |
| `Ctrl+C` | quit |

Useful slash commands include:

- `/mode`
- `/model`
- `/agent`
- `/preset`
- `/compare`
- `/new`
- `/sessions`
- `/load`
- `/start`
- `/stop`
- `/clear`
- `/help`
- `/quit`

## Project layout

```text
cmd/relayd         backend daemon
cmd/relay          terminal UI
internal/config    config loading and TOML setup
internal/provider  provider adapters and key rotation
internal/router    routing logic and model fallback
internal/agent     agent execution loop
internal/tools     file, shell, web, and MCP tools
internal/mcp       MCP client
internal/runner    process launcher
internal/session   session history and persistence
internal/server     HTTP API and SSE server
internal/client     API client for clients/UI
internal/tui       TUI, view, commands, mascot, themes
assets/sheets      sprite sheets
tools/sprites      sprite generation helpers
```

## Security notes

Relay is a developer tool and should be treated as such.

- the daemon binds to local-only interfaces and expects a token
- agent actions are restricted to the workspace
- file writes, shell commands, and external tools may require approval
- shell execution is not sandboxed; review commands carefully before approving

## Status

This project is an experimental terminal AI tool. It is intentionally lightweight and practical rather than polished enterprise software. It is designed to be hackable and easy to extend.

## Contributing

Pull requests and experiments are welcome. If you want to improve routing, provider integrations, the TUI, or the agent workflow, this repo is structured to make that possible with relatively little ceremony.

## Note

This is a vibe-code project, built with Claude as an AI-assisted development workflow. It is a creative, experimental codebase and should be treated as such rather than as a production-grade commercial product.
