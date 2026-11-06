# PROJECT.md: notes on the Relay project (for Claude)

I read this file at the start of every working session on the repository. It holds the goals, the decisions
taken, the map of the code, the status and the plan. User documentation: `README.md`, `docs/PROTOCOL.md`.

## What we are building

Relay is a launcher and router for AI models in the terminal (Linux and Windows): local models and APIs in one
interface, coding agents, an OpenAI-compatible gateway, a mascot with emotions. The author speaks Russian, but
the product is for ordinary users, so the interface (TUI, CLI, the web pages), the example config and all
documentation are in English.

Tagline: "Faster. Closer. Together."

## Decisions taken (and why)

| Decision | Reason |
|---|---|
| Backend and interface are two processes but one executable, `relay` | The user asked for the split (any client can use the same API) and for "fully in the CLI": the backend is the subcommand `relay daemon`, the interface starts it in the background by itself (a detached process) |
| Everything in Go | One static binary, cross-builds for Windows and Linux without a runtime. Python was rejected by the user |
| `relay install` adds itself to PATH | The user wanted to run `relay` after building: Linux `~/.local/bin` + a line in the shell's rc file, Windows user PATH through PowerShell |
| Keys in `keys.json` (0600), entered on a page on localhost | Environment variables are awkward (unclear names, they do not survive sessions). Page `relay keys`; token in the `#fragment`, API with Bearer, Host check. Env stays optional (`api_key_env`) |
| The backend version is checked on connect | After the binary is updated the old background backend is stopped and a new one starts |
| Transport HTTP + SSE on `127.0.0.1` with a token | Works the same on Windows and Linux, easy to debug with `curl`; the token is needed because the agent has a shell |
| One `openai` adapter for all OpenAI-compatible servers | Ollama, llama.cpp, LM Studio, vLLM, OpenAI, OpenRouter, Gemini speak `/chat/completions`; plus a separate `anthropic` |
| Few dependencies | Backend: stdlib + `BurntSushi/toml`. TUI: bubbletea, lipgloss, bubbles |
| Config is TOML, keys are only names of env variables | Secrets do not sit in the file; `api_key_env` is a list for rotation |
| Sessions: one JSON file per session | Simpler than SQLite, no cgo or outside dependency. Tokens and cost are kept in the session file |
| Sprites: sharp 44x36 pixels, half blocks `▀` | Braille cells looked like a blur and the emotions were not visible. Half blocks give exact square pixels. The background under the mascot equals the theme background. Transparency by flood fill from the edges of the sheet |
| The rainbow for maximum is computed at runtime | The accent colours of the palette (green) are recoloured by hue = f(x, y, t) |
| Relay finds the models itself (`internal/catalog`), ids are `provider/model` | A list in the config goes stale and the user wants to see what really exists: what is installed in Ollama, Ollama cloud by key, the Gemini list. `Config.ModelByID` understands any `provider/model` of a configured provider, so router, gateway and sessions work unchanged |
| `ollama-cloud` is a separate provider with `key_from = "ollama"` | Ollama cloud is another address (`ollama.com`) with the same key; there is one key card. For old `config.toml` files the provider is added implicitly (`addImplicitProviders`), because the file is never rewritten. The same for the processes `ollama` and `llamacpp` (`addImplicitProcesses`) |
| The Ollama and Gemini lists are treated as complete (`Authoritative`) | A model that is not in the list is skipped in the mode chain; an uninstalled local model is replaced by 2 installed ones. If the list cannot be fetched nothing is hidden |
| Provider errors are shown only on the keys page | The terminal stays calm; a provider without access adds nothing to the model list |
| Reasoning is split from the answer in the provider layer (`provider/thinking.go`) | Models write it in different ways (`<thought>` tags in the text, `reasoning_content`, `reasoning`, Anthropic `thinking_delta`). The backend turns all of them into `thinking` events, and does not send them at all when the setting is off |
| Gemini `thought_signature` is kept with the tool call (`ToolCall.Extra`) | Without it Gemini answers HTTP 400 on the next agent step |
| The answer is typed out by the interface from a queue (`tui/stream.go`) | Networks deliver text in lumps; the queue makes it flow at the speed from the settings, and keeps the order against tool calls |
| Preferences live in `settings.json`, not in `config.toml` | `config.toml` is hand-edited and never rewritten; the settings page changes preferences at runtime and they apply within seconds |

## Modes and themes

| Mode | Sprite sheet | Interface colour | Background |
|---|---|---|---|
| small | `assets/sheets/green.png` | green `#67DD5D` | `#0C1510` |
| medium | `assets/sheets/purple.png` | purple `#A07CFF` | `#110E1F` |
| hard | `assets/sheets/red.png` | red `#FF4D6A` | `#180C11` |
| maximum | green.png + rainbow | the hue drifts with time | `#0F1014` |

The "mode = sheet colour" pairing was my choice (green is the lightest, red the heaviest); the author did not
specify it, so it can be changed in `THEMES` (`tools/sprites/build.py`) and `themeFor` (`internal/tui/theme.go`).
A mode also sets the model chain and the agent's `max_steps` (`[modes.*]` in the config).

## Mascot emotions

Frames: `calm`, `joy`, `surprise`, `noticed`, `tired` (with "Zz"). The action frames (run, jump, link, summon)
are not extracted from the sheets any more (unused and too tall); bring them back in `tools/sprites/build.py` if needed.

A mode change is animated (`internal/tui/view.go`: `transition`, `currentTheme`; `sprites.go`: `renderSprite`):
14 ticks of 120 ms, the theme colours blend smoothly (`blendTheme`), the mascot is repainted by a wave from left to
right with a bright line between the old and the new sheet. The logic is in `internal/tui/mascot.go`; events are
mapped in `internal/tui/model.go` (`handleEvent`):

| Event | Emotion |
|---|---|
| start, mode change, a successful request | joy |
| `tool_call`, `notice key_switch`, agent toggle | noticed |
| error, `approval`, a failed `tool_result`, `notice fallback`, waking up | surprise |
| `notice rate_limit` (8 s), `error rate_limit` (until the first success), idle > 2 min | tired |
| `notice no_key`, `error no_key` | surprise + the line "No key: /keys" |

## Map of the code

- `cmd/relay`: the single CLI. `main.go` (subcommand dispatch; without a subcommand the TUI, flags `--no-spawn`, `--keep`), `cli.go` (keys, status, stop, config), `install.go` (install/uninstall, PATH), `console_windows.go` / `console_other.go` (UTF-8 console on Windows).
- `internal/catalog`: finds the models of providers (Ollama `/api/tags`, Gemini `/v1beta/models`, `/v1/models`), 60 s cache, `Snapshot.Missing/Pick/Fallback` for the router, `Running` (local servers that answer).
- `internal/daemon`: backend start (`relay daemon`): token, `daemon.json`, clean shutdown, autostart of processes.
- `internal/keystore`: key storage (`keys.json`), implements `provider.Source`.
- `internal/settings`: preferences (`settings.json`).
- `internal/browser`: opens URLs in the browser.
- `internal/proto`: all DTOs and events of the protocol (the single source of truth for client and server).
- `internal/config`: TOML loading, validation, `example.toml` (embedded, created on first run).
- `internal/provider`: `Client` (`Stream` with a callback), `Keys` (keys from the store and env, rotation, rest after a limit or missing credit), `OpenAI`, `Anthropic`, `thinking.go` (splits reasoning from text), classification of errors (`RateLimited`, `Billing`, `Auth`, `NoKey`).
- `internal/router`: `Resolve` (model chain by mode and hint, skips providers without keys or credit), `StreamAsk` (the agent and chat: when a model fails, the next untried model of the SAME provider is offered: models of the config first, then those the catalog lists; max 8 tries; provider-wide errors (no key, bad key, no credit) stop at once; after all of them failed the error says so and points to `/keys`), `Stream` (gateway/compare: silent fallback along the mode chain, across providers), `attempt` (one model), usage accounting. A rate limit pauses a key for that model only (`Keys.GetFor`, `DoWithKeysFor`); no credit pauses the whole key.
- `internal/agent`: the step loop, approvals, limit on tool output, per-session token and cost accounting.
- `internal/tools`: `read_file`, `list_dir`, `grep`, `write_file`, `edit_file`, `run_shell`, `web_fetch`, MCP adapter. Paths are limited to the workspace (symlinks are not resolved: a known gap).
- `internal/mcp`: `config.go` (mcp.json: `File`/`ServerConfig`, `Parse` rejects the whole file on one bad entry, `Fingerprint`, safe `buildEnv`), `trust.go` (approvals of stdio commands in `mcp-trust.json`), `rpc.go` (JSON-RPC, `Transport`, `AuthError`), `stdio.go` (own process group, stderr tail), `http.go` (Streamable HTTP: JSON or SSE answers, `Mcp-Session-Id`, no redirects), `client.go` (handshake, tools, calls), `manager.go` (`Manager`: states, reload with the last good config, lazy reconnect, `Dial`).
- `internal/skills`: `frontmatter.go` (the small YAML subset, no dependency), `skill.go` (`Parse`/`ParseAs`, Agent Skills validation), `manager.go` (list, enable/disable in `skills.json`, `Prompt`, `readInside` that keeps paths in the skill folder), `install.go` (`Install`/`Remove`).
- `internal/watch`: polling watcher (`Poll`, `FileSig`, `DirSig`) for mcp.json, the approvals and skills/.
- `internal/runner`: process launcher, URL ready probe, stop (Interrupt, then Kill), attaches to servers that already run.
- `internal/session`: session storage (messages, tokens, cost), `Run` mutex (one request per session), `AutoTools` ("always" for the session), delete.
- `internal/server`: routes (Go 1.22 ServeMux with methods), SSE, approvals, compare, the `/v1` gateway, the keys API and page `ui/keys.html`, the help page `ui/help.html`, the settings API and page `ui/settings.html`, Host check.
- `internal/client`: HTTP client, backend autostart (`os.Executable() daemon`, a detached process: `proc_unix.go`, `proc_windows.go`), version check, SSE.
- `internal/tui`: `model.go` (state and Update), `commands.go` (slash commands), `view.go` (feed, side panel, input), `overlay.go` (lists above the input: sessions, running processes, clicks), `picker.go` (model list), `approval.go` (agent questions), `stream.go` (typing queue, thoughts, scroll keys, /copy, mouse, select mode), `sprites.go` (pixels → half blocks, rainbow, transition wave), `theme.go` (themes, blending), `mascot.go`.
- `tools/sprites/build.py`: PNG sheets → `internal/tui/sprites/*.json` + previews in `assets/sprites/`.

## Conventions

- Go 1.22+, `go vet` must be clean. No import cycles: `proto` knows only `provider` and `settings`.
- Bubble Tea: the model is a value; call mutators with a pointer receiver as a separate statement
  **before** `return m, ...` (the order of evaluating `m` and the call inside one return is not defined).
- Colours only through `theme.go`; the background is painted in `paintLine` (after every `\x1b[0m` the background is switched on again).
- New protocol events: add them to `proto.Event`, to the table in `docs/PROTOCOL.md`, and to `handleEvent`.
- New settings: add to `settings.Settings` (with a default and cleaning), to the table in `docs/PROTOCOL.md`, to `ui/settings.html`, and use them in the interface.
- All documentation, UI texts and examples are in English.

## Status

**v0.8 (extensions, uncompiled).** Written without a Go toolchain: `go mod tidy && go vet ./... && go test ./...` has NOT been run, expect a few compile errors to fix first. Done from the extensions roadmap (Phases 1, 2 and the file-based part of 4): `mcp.json` (+ `mcpServers` alias, old `[[mcp]]` still works with the full environment), stdio and Streamable HTTP, `Manager` with the six states, hot reload (polling, not fsnotify: no new dependencies), approvals for stdio commands by fingerprint (`relay mcp trust`), safe environment with `${VAR}` expansion, `disabled_tools`, Skills (Agent Skills, progressive disclosure through `load_skill` / `read_skill_file`, list in the agent system prompt, hot reload, enable/disable), `relay mcp ...` and `relay skill ...`, `GET /api/mcp`, `/api/skills`, an "extensions" block in the side panel, `/mcp`, `/skills`. Fallback reworked (see `internal/router` above); the agent keeps the model it switched to for the rest of the request.

Decisions of this step: the official MCP SDK was NOT used (no network to fetch it, and its API could not be checked); the transports sit behind `mcp.Transport`, so the SDK can replace them. `config.json` of the roadmap is not created: the settings stay in `config.toml`. Skills outside `~/.config/relay/skills` (project folders) are not read.

Not done yet (roadmap): Phase 3 OAuth (the `AuthError` carries `resource_metadata`; tokens go to `auth/`, 0700; `httpTransport.token` is the hook), Phase 5 the Extensions page of the web UI (the API exists), Phase 6 Git install / registry (`skill add github:...` says "not yet"), `notifications/tools/list_changed`, GET stream of Streamable HTTP, `relay extension ...`.

**v0.7 (later changes).** Fallback asks before moving to another model (agent/chat; `/compare` and the gateway still switch silently); an accepted switch becomes the session model; a Gemini limit no longer rules out its other models; llama.cpp: the model value picks `-m` / `-hf` / `-mu` itself and several binary names are tried; `/load` moves the session to the current folder; select mode hides the side panel. Select mode (`f2`, `/select`; a terminal cannot report Shift alone, and in-app drag-copy was tried and dropped): the screen is frozen (no ticks, no cursor blink, animations drawn as of the freeze moment) and the mouse goes back to the terminal, so a selection is not dropped; the settings page bug where every second change was lost (the controls held a stale settings object) is fixed; launcher settings of Ollama and llama.cpp (autostart, arguments, start/stop) moved to their cards on the keys page; sessions are a separate tab of the settings page; the interface polls status every ~2.4 s.

**v0.7.** Reasoning of models is split from the answer (`thinking` events, dimmed block in the feed, can be turned
off: then it is not sent at all); the answer is typed out smoothly at a chosen speed; `/settings` opens a page
(`settings.json`: thoughts, speed, side panel, mascot, text width, mouse, default mode, autostart and arguments of
processes, session deletion); `/thoughts`, `/mouse`, `/copy` (OSC 52); extra scroll keys (shift+↑↓, alt+↑↓, ctrl+u/d,
home/end) with a "scrolled up" hint; tokens and cost are stored per session; Gemini `thought_signature` is kept (no
more HTTP 400 in agent mode); providers without credit are skipped in the fallback chain and the final error lists
every attempt; no key field on the keys page for local servers; the side panel says "N local models".

**v0.6.** `processes` block in the side panel (only what runs, otherwise "Nothing running :)"); lists above the input
can be clicked: models, sessions (`/sessions`), running processes (`/stop`), the agent's approval (Allow once /
Always / Deny; keys y/a/n, ↑↓ enter); `/start` removed in favour of `/ollama` and `/llamacpp <model.gguf>`;
quick status polling after a start and a message when a process dies; UTF-8 console on Windows; a hint for stopping
Ollama without systemctl (tray icon).

**v0.5.** Providers Grok (xAI) and DeepSeek; official providers first (`Provider.IsOfficial`, by host); provider
errors only on the keys page; `/help` opens `/ui/help`; README in English; filter for mouse-report fragments in the input.

**v0.4.** `/model` became `/models` with a picker (`internal/tui/picker.go`); `internal/catalog` finds Ollama (local
and cloud), Gemini, Anthropic and OpenAI-compatible models; `GET /api/models`; the keys page shows the number of
models and checks the key.

**v0.1–v0.2.** First build and the author's feedback: sharp mascot, mode-change animation, keys through a page and a
store, one CLI with PATH installation, aliases `/small` `/medium` `/hard` `/max`.

Verification note: the development environment has no access to proxy.golang.org, so `go vet` and the tests run
against stubs of the third-party libraries (bubbletea, lipgloss, bubbles, toml); the full build with the real
libraries happens on the author's machine. Provider answer formats (`/api/tags`, `/v1beta/models`, reasoning fields)
come from documentation and the author's screenshots, not from live services.

## First steps of the next session

1. `go mod tidy && go vet ./... && go build ./... && go test ./...`, fix compile errors (v0.8 was written without a compiler; if the author sent the output, start from it).
2. `relay install`, then `relay keys` and `relay` (`/settings`, `/models`, a chat with a thinking model).
3. Check `pgup` / `shift+↑` scrolling and text selection (`/mouse off`) in the author's terminal (GNOME Console).
4. Windows: `relay install` (PowerShell, PATH), background start (`detach`), `rundll32` for the browser, UTF-8 console.

## Known gaps and ideas (plan)

- Tests: provider adapters against a fake SSE server, path resolution in tools, SSE parsing in the client.
- Context management: trimming long history and counting tokens (usage is only estimated as `len/4` when a provider reports none).
- Gateway: pass through `tools`/`tool_calls`; `/v1/embeddings`.
- Agent: parallel tools, cancelling the shell with `esc` while it runs, a diff preview before approval, multi-agent work (a planner on a strong model, workers on local ones), subagents ("summon": frame `summon`).
- Mascot: use the action frames (`run` while an answer is coming, `link` when connecting to a provider, `summon` for a subagent), blinking, lines by mood.
- Security: keys in the system keyring instead of `keys.json`, symlink limits in the workspace, a command allowlist.
- Settings page: adding providers and models (now only keys and preferences; models and modes live in `config.toml`), a button that checks a key, a usage summary.
- Interface: multi-line input, markdown and code highlighting, completion of commands and models.
- Providers: native Gemini and Ollama APIs (now through OpenAI compatibility), images.
- Distribution: releases for Linux and Windows (goreleaser), one-command install.
