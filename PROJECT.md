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
| Extensions are plain files, there is no database | `mcp.json` (servers) and `skills/<name>/SKILL.md` are the source of truth; the user can edit them by hand; Relay watches them by polling (no fsnotify: no new dependency) and applies changes within a second. A file with a mistake is ignored as a whole and the last good version stays |
| MCP: our own client, not the official SDK | The sandbox has no network to fetch the SDK and its API could not be checked. Transports are behind `mcp.Transport`, so the SDK can replace them. Streamable HTTP and stdio are done |
| A local MCP program runs only after the user approved that exact command (`mcp-trust.json`, by fingerprint) | A hand-edited `mcp.json` must not be able to start programs by itself. The program gets a small environment (PATH, HOME, language, proxy, certificates and the **desktop session variables** `DISPLAY`, `WAYLAND_DISPLAY`, `XAUTHORITY`, `DBUS_SESSION_BUS_ADDRESS` ...), never the keys of Relay; `${NAME}` in `env` takes a value from the environment of Relay |
| OAuth is done by Relay, the connector gets only the access token | `internal/oauth`: PKCE, loopback callback on the daemon, tokens in `auth/` (0600). The program reads `RELAY_OAUTH_TOKEN_FILE`; the refresh token and the client secret never reach it. Remote MCP servers are signed in by discovery (RFC 9728/8414/7591) |
| The agent can build its own tools; any model that calls tools can do it | Tools `internal/builder` + the skill `relay-builder` built into the binary (`go:embed`) + a small Python SDK `relay_mcp.py` (also embedded). The model writes only tool functions; the protocol, argument checking, HTTP and token reading are in the SDK. Python 3 on the computer is a requirement for this |
| Building has a fixed order that the code enforces | (1) `propose_extension` (the user must agree; a refusal **cancels the whole request**), (2) the model must have read the guide and `references/sdk.md` in this request, (3) `create_mcp_server` with a required `skill`. Without an agreed offer or without reading, the build tools answer "not yet" with no prompt. With the setting `suggest_tools` off, only the approval of `create_*` remains |
| "Always allow in this session" works for `create_*`, not for the offer and not for `remove_extension` | The author asked for it (repairs repeat `create_mcp_server`); the question "may I build this?" is asked every time (`tools.OnceOnly`) |
| Every server comes with a skill | A tool without a skill is used badly by models. `create_mcp_server` writes it after the server started and appends the exact list of tools; `metadata: {created-by, server}` ties it to the server |
| Relay tells the model where it runs | Models believed they were in a remote sandbox and refused to "touch the user's computer". The agent prompt has a "Where you run" paragraph and `list_extensions` starts with the OS, the desktop and Python |
| While the agent builds, one progress line replaces the reasoning, the calls of the model and what it says on the way | Setting `builder_view` (`progress` | `thoughts`). The line is the last part of the feed (right under the question): a moving label, the time, the stage `n/5`; calls of builder tools and of tools made during a build are hidden; text during a build is held and shown at the end only if it was the final answer |
| An answer cut by the output limit goes on by itself | `provider.EvFinish("length")`; the agent adds a hidden user message (`provider.InternalPrefix`) asking to go on, or to write the cut tool call again smaller. Anthropic's `stop_reason: max_tokens` was ignored before |
| The step limit is soft | `max_steps` is where a notice appears; the hard limit is 4x (max 120), then one more turn without tools to summarise; the same call 4 times in a row stops the agent |
| Long conversations are fitted to the context of the model before each request | `agent/context.go`: old tool results are shortened, then the oldest turns are left out (from a user message); the stored history is not changed. `context` in `config.toml` (default 128000) |
| The feed is drawn once per entry | `tui/perf.go` `feedCache`; control codes of terminals are removed from outside text (`cleanText`); `View`/`Update` recover from panics and write `crash.log` |
| Fallback of a failed model stays inside its provider (agent and chat) | Asking the user (allow / always / deny); the gateway and `/compare` still fall back silently along the chain |
| The version is 0.1.x | 0.1.0 is the first numbered release (earlier numbers 0.4 ... 0.9 were working names). The client restarts the background service only when the version string differs, so every build that changes the service gets a new patch number (0.1.1 ...) |


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

- `cmd/relay`: the single CLI. `main.go` (subcommand dispatch; without a subcommand the TUI, flags `--no-spawn`, `--keep`), `cli.go` (keys, status, stop, config), `ext_mcp.go` (`relay mcp list|add|remove|test|trust|connect|disconnect|reconnect|sdk`), `ext_skill.go` (`relay skill ...`), `ext_common.go`, `install.go` (install/uninstall, PATH), `console_*.go` (UTF-8 console on Windows).
- `internal/catalog`: finds the models of providers (Ollama `/api/tags`, Gemini `/v1beta/models`, `/v1/models`), 60 s cache, `Snapshot.Missing/Pick/Fallback`, `Running`.
- `internal/daemon`: backend start (`relay daemon`): token, `daemon.json`, `SetAddr` (the OAuth callback address), clean shutdown, autostart of processes.
- `internal/keystore`, `internal/settings` (`settings.json`: includes `builder_view`, `suggest_tools`), `internal/browser`, `internal/proto` (all DTOs and events: the single source of truth), `internal/config` (TOML, `example.toml`, `layout.go`: the extension folders).
- `internal/provider`: `Client` (`Stream` with a callback), `Keys` (rotation, rest after a limit: **per model** for a rate limit, whole key for no credit; `GetFor`, `DoWithKeysFor`), `OpenAI`, `Anthropic` (reports `EvFinish`, passes on a tool call cut by the limit), `thinking.go`, error classification. `InternalPrefix` marks messages that Relay adds for the model.
- `internal/router`: `Resolve`, `StreamAsk` (same-provider fallback with questions, `nextOfProvider`, max 8 tries), `Stream` (silent chain fallback for the gateway and compare), `attempt`, usage accounting, `MaxOutput` of a model is passed on.
- `internal/agent`: the step loop (`agent.go`): continuation after the output limit, soft step limit with a summary turn, loop detection, `registry()` built at every step, consent and order gates in `runTool` (`NeedsOffer`, `NeedsRead`, `CancelOnDeny`, `OnceOnly`, `Denier`, `BuildOffer`), the sign-in pause (`signIn`, event `auth`), progress events, prompts (`agentPrompt`, `suggestPrompt`, `environmentPrompt`); `context.go` (`fitContext`).
- `internal/tools`: `read_file`, `list_dir`, `grep`, `write_file`, `edit_file`, `run_shell`, `web_fetch`, the MCP adapter (`mcp.go`), the skill tools `load_skill` / `read_skill_file` (`skills.go`), `progress.go` (the optional interfaces above, `Progress` reporting). Paths are limited to the workspace (symlinks are not resolved: a known gap).
- `internal/mcp`: `config.go` (mcp.json, `Parse` rejects the whole file on one bad entry, kinds, `OAuthConfig`, `Fingerprint`, `buildEnv`), `trust.go`, `rpc.go` (JSON-RPC, `Transport`, `AuthError`, `AuthRequiredError`, `SignInTool`, `AuthMarker`), `stdio.go`, `http.go`, `client.go`, `manager.go` (`Manager`: states, reload with the last good config, lazy reconnect, `Dial`, sign-in: `SetOAuth`, `Authorize`, `NeedsSignIn`, `Forget`).
- `internal/oauth`: `oauth.go` (PKCE, token and refresh, `Store`), `discover.go`, `broker.go` (`Begin`, `HandleCallback`, `Token`). Depends on nothing else of Relay.
- `internal/skills`: `frontmatter.go` (a small YAML subset), `skill.go`, `manager.go` (list, enable/disable, `Prompt`: built-in descriptions are shown up to 700 characters, others 250), `install.go`, `builtin.go` + `builtin/relay-builder/` (the guide, `references/`: `sdk.md`, `patterns.md`, `mcp-json.md`, `relay-structure.md`, `skills-format.md`, and six working examples `example-*.py`).
- `internal/builder`: the tools of the agent that build: `propose_extension` (`propose.go`), `create_mcp_server` (`server.go`), `create_skill` (`skill.go`, `skillwrite.go`), `read_server`, `list_extensions`, `authorize_connector`, `remove_extension` (`manage.go`); `sdk/relay_mcp.py` (embedded SDK) and `sdk/test_relay_mcp.py` (a real test: run it with Python).
- `internal/watch`: polling watcher. `internal/runner`: process launcher. `internal/session`: storage, `Run` mutex, `AutoTools`, per-request flags `BuildOK`, `Cancelled`, `Read` (not stored).
- `internal/server`: routes, SSE, approvals, compare, `/v1` gateway, keys/settings/extensions APIs (`extensions.go`: mcp, skills, tools, authorize), pages `ui/keys.html`, `ui/settings.html`, `ui/help.html`, `/oauth/callback`.
- `internal/client`: HTTP client, backend autostart, version check, SSE.
- `internal/tui`: `model.go` (state, `Update` with recover), `commands.go`, `view.go` (feed, side panel, input), `perf.go` (feed cache, `cleanText`, clipping, crash log), `build.go` (the progress line, hidden calls, `trackBuild`), `ext.go` (/tools /connectors /skills /mcp, sign-in opening), `overlay.go`, `picker.go`, `approval.go`, `stream.go`, `sprites.go`, `theme.go`, `mascot.go`.
- `tools/sprites/build.py`: PNG sheets to sprites. `tools/check/`: static checks for sessions without Go (see its README).
- `docs/`: `PROTOCOL.md` (API and events), `EXTENSIONS.md` (the guide for making MCP servers and skills by hand).

## Conventions

- Go 1.22+ (go.mod says 1.24), `go vet` must be clean. No import cycles: `proto` knows only `provider` and `settings`; `oauth` knows nothing of the rest.
- Bubble Tea: the model is a value; call mutators with a pointer receiver as a separate statement **before** `return m, ...`.
- Colours only through `theme.go`; the background is painted in `paintLine`.
- Anything shown to the user must pass `cleanText` if it comes from a tool or a model (control codes break the screen).
- New protocol events: add them to `proto.Event`, the table in `docs/PROTOCOL.md`, and `handleEvent`. New settings: `settings.Settings` (default and cleaning), `docs/PROTOCOL.md`, `ui/settings.html`, and the interface.
- A new builder or MCP-related tool that creates code must implement `OnceOnly` only if the question has to be asked every time; a tool that builds must implement `NeedsOffer`, `NeedsRead`, `CancelOnDeny`.
- When the builder skill changes, run the example servers again against the SDK (see the tests in `internal/builder`, and the Python test).
- All documentation, UI texts and examples are in English; the conversation with the author is in Russian.
- **No Go in the sandbox**: the code is written without a compiler. Run `tools/check/*` before handing code over, and say plainly that it was not compiled.

## Status

**0.1.1 (= 0.1.0 plus the fixes of the last session), in the hands of the author for testing. The code was never compiled by Claude**: the author compiles and reports errors (the only compile error so far was a type declared twice, `skillArgs`; everything else built). Tests were never run by Claude either: expect failures in them first.

What exists:
- Everything of the launcher: modes, themes, mascot, providers, catalog, keys page, settings page, sessions, agent with tools and approvals, gateway, compare.
- Extensions: `mcp.json` (stdio, Streamable HTTP, kinds, `disabled_tools`, `${VAR}`), approvals by fingerprint, hot reload, Skills (Agent Skills; progressive disclosure; built-in `relay-builder`), OAuth (remote discovery and `oauth` blocks, pause of the agent and sign-in in the browser, refresh), CLI `relay mcp` / `relay skill`, `/tools`, `/connectors`, `/skills`, API endpoints.
- The agent builds its own tools: see Decisions (consent, order, skill, repair with `read_server` and `propose_extension` kind `fix`).
- Fixes of the last session: "Limit reached" (output limit and step limit), long conversations (context fitting), interface lag and garbage (feed cache, control codes, clipping, recover + `crash.log`).

What the author reported in the last session and how it was handled:
1. The progress bar looked bad: removed. One line under the question: a label that shimmers, the time, the stage; the model's calls are hidden while it builds; interim text is dropped, the final answer is shown.
2. The model did not offer to build, it said "I cannot": the prompt now forbids that (`suggestPrompt` is placed before the skills and lists direct and indirect requests); `propose_extension` is described as the thing to call instead; the skill description lists indirect triggers (and is shown in full).
3. A refusal must cancel the whole request: `CancelOnDeny`; building cannot start without an agreed offer (`NeedsOffer`).
4. "Always allow" for `create_mcp_server` did not work (it was `OnceOnly`): it works now; the offer stays once-only.
5. The model built before reading the guide: `NeedsRead` makes the build tools refuse until `relay-builder` and (for a program) `references/sdk.md` were read in the request.
6. A tool opened no window and the model said it cannot control the computer: the sandbox variables `DISPLAY` etc. were missing from the small environment (a bug of ours) and the model believed it was in a sandbox. Both fixed; example `example-desktop.py` and the rule "the simplest thing" (no browser automation).
7. A broken tool was abandoned: the repair path (`propose_extension` kind `fix`, `read_server`, `create_mcp_server` with `replace`), with the user's permission.
8. A Telegram connector got an OAuth block although a bot uses a token: the skill has a table "choose the simplest way to log in"; `create_mcp_server` refuses an `oauth` block whose code never uses the token (and the reverse); `example-apikey.py` is the Telegram bot.

Not done / ideas (see also the plan): a web page to add servers by hand; Node and Go SDKs (only Python now); Basic client authentication at token endpoints; `tools/list_changed`; a store for secrets (now the user exports a variable and restarts Relay; the answer of `create_mcp_server` says so); a scheduler (tools run only when called).

History (older work, short): v0.8 extensions (mcp.json, skills, CLI, hot reload); v0.9 builder tools, OAuth, `/tools` `/connectors`; v0.7 reasoning split from the answer, typing queue, settings page, select mode, ask before switching models; v0.6 processes panel, clickable lists, approvals box; v0.5 Grok and DeepSeek, official providers first; v0.4 model picker and catalog; v0.1-v0.2 first build.

Verification note: the sandbox has no access to proxy.golang.org, so `go vet` and the tests cannot run there at all. Provider answer formats (`/api/tags`, `/v1beta/models`, reasoning fields) come from documentation and the author's screenshots, not from live services. The OAuth flow was tested only against fake servers.

## First steps of the next session

1. If the author sent compiler or test output, start from it. Otherwise ask for `go vet ./... && go build ./... && go test ./...`.
2. Check the behaviours that only a person can see (the author's terminal is GNOME Console): the progress line under the question, hidden calls, the final answer after a build, `/tools` `/connectors` `/skills`, sign-in in the browser, a long conversation (no lag), `crash.log` stays empty.
3. Try the builder with several models (a strong one and a weak local one): "connect my Telegram" (token, no OAuth), "open YouTube and search for cats" (desktop), an indirect request ("send this to my Telegram"), a refusal at the offer, "always allow", a deliberately broken tool and its repair.
4. Windows: `relay install`, background start, the browser, UTF-8 console; Python for the builder (`python` vs `python3`).

## Known gaps and ideas (plan)

- Tests: provider adapters (OpenAI finish and Anthropic cut call are covered; the rest is not), path resolution in tools, SSE parsing in the client, TUI interaction.
- The token count is estimated as `len/4` when a provider reports none; `context` per model is needed for small local models.
- Gateway: pass through `tools`/`tool_calls`; `/v1/embeddings`.
- Agent: parallel tools, cancelling the shell with `esc` while it runs, a diff preview before approval, multi-agent work, subagents.
- Builder: Node/Go SDKs; a secret store; Git install of skills and servers (`relay skill add github:...`), a registry; the Extensions page of the web UI; showing the code of a new program in the approval box (now only a summary).
- Mascot: use the action frames (`run`, `link`, `summon`), blinking, lines by mood; a mood for building.
- Security: keys and tokens in the system keyring instead of files; symlink limits in the workspace (`tools.resolve`); a command allowlist.
- Settings page: adding providers and models, a button that checks a key, a usage summary.
- Interface: multi-line input, markdown and code highlighting, completion of models.
- Providers: native Gemini and Ollama APIs, images.
- Distribution: releases for Linux and Windows (goreleaser), one-command install.
