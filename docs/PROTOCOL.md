# Relay backend protocol

Transport: HTTP/1.1 + JSON; streaming answers use Server-Sent Events (`data: <json>\n\n`).
Address and token: `<home>/daemon.json` (`{"addr":"127.0.0.1:8787","token":"...","pid":123}`).
Every request to `/api/*` and `/v1/*` (except `/api/health`) needs the header `Authorization: Bearer <token>`.
`<home>` is `$RELAY_HOME`, otherwise `<UserConfigDir>/relay`.

Errors: a 4xx/5xx status and the body `{"error":{"message":"..."}}`.

## Endpoints

| Method and path | Description |
|---|---|
| `GET /api/health` | no authorization: `{"ok":true,"version":"0.8.0"}`; a client restarts a backend of another version |
| `GET /api/status` | modes (`effective`: the chain that is really used), models, providers (number of keys and how many are usable), processes, local servers that answer now (`servers`), usage, presets, MCP (`mcp`: names of connected servers; `mcp_servers`: the servers without their tools; `mcp_error`), `skills` (installed, with `enabled`), and the user's `settings` |
| `GET /api/models` | every model to choose from: the ones the providers report (`ollama/…`, `ollama-cloud/…`, `gemini/…`), official providers first; a provider without access adds nothing (models from the config show only with `discover = "none"`); `providers` has the number found, `local`, `skipped` ("no key") and `error`. `?refresh=1` asks the providers again; adding or deleting a key clears the 60 s cache by itself |
| `GET /api/sessions` | list of sessions (no messages), with `tokens_in`, `tokens_out`, `cost` |
| `POST /api/sessions` | create: `{"mode":"small","workspace":"/path"}` (without `mode` the default mode of the settings is used) |
| `GET /api/sessions/{id}` | a session with its messages and its own `tokens_in`, `tokens_out`, `cost` (USD) |
| `DELETE /api/sessions/{id}` | delete a session (refused while it is answering) |
| `POST /api/sessions/prune` | `{"which":"empty"\|"all"}` → `{"deleted":n,"skipped":n}` |
| `GET /api/session-list` | sessions for the settings page: id, title, mode, updated, number of messages, tokens, cost |
| `POST /api/sessions/{id}/settings` | `{"mode","model","preset","agent","workspace"}`; omitted fields do not change (`workspace` is the folder the agent works in; it must exist) |
| `POST /api/sessions/{id}/messages` | `{"text":"..."}` → a stream of SSE events |
| `POST /api/approvals/{id}` | `{"decision":"allow\|always\|deny"}` (id from the `approval` event) |
| `POST /api/compare` | `{"text","models":["a","b"],"preset"}` → a stream of events with a `slot` field |
| `POST /api/processes/{name}/{start\|stop}` | manage local processes; the body of start may carry `{"args":[...]}` (added to the args of the config; without it the arguments saved in the settings are used); the answer is `{"processes":[...],"already":bool}` |
| `GET /api/settings` | `{"settings":{...},"modes":[...],"processes":[...]}` |
| `POST /api/settings` | change the settings; fields that are omitted keep their value; the answer is the same as for GET |
| `GET /api/mcp` | MCP servers of `mcp.json` (and the old `[[mcp]]`): `id`, `name`, `type` (`stdio`\|`http`), `target`, `source`, `state` (`configured`, `connecting`, `connected`, `auth_required`, `disconnected`, `error`), `error`, `warning`, `trusted` (stdio: the exact command was approved), `tools` (name, description, disabled); `error` of the answer is a problem in `mcp.json` that is being ignored |
| `POST /api/mcp/{id}/{connect\|disconnect\|reconnect\|trust}` | waits for the result (up to ~90 s for a program that has to start); a failure is the error of the request; the answer is the same as for GET |
| `GET /api/skills` | installed skills: `name`, `description`, `path`, `enabled`, license, compatibility, metadata; `problems` lists folders that are not valid skills |
| `POST /api/skills/{name}/{enable\|disable}` | switch a skill on or off (kept in `skills.json`); the answer is the same as for GET |
| `GET /api/keys` | providers and keys: `group` (`official`, `external`, `local`; sorted in this order), `stored` (id, hint = last 4 characters), `total`, `available`, `needs_key`, `key_optional` |
| `POST /api/keys` | add a key: `{"provider","key"}`; the full key is never returned |
| `DELETE /api/keys/{provider}/{id}` | delete a stored key |
| `GET /ui/help` | the help page (English HTML, no authorization); opened by `/help` |
| `GET /ui/keys` | the API keys page (HTML, no authorization; it takes the token from `#token=...` in the URL) |
| `GET /ui/settings` | the settings page (HTML, same way as the keys page); opened by `/settings` |
| `POST /api/shutdown` | stop the backend |
| `GET /v1/models`, `POST /v1/chat/completions` | OpenAI-compatible gateway |

One request runs in a session at a time (otherwise 409).
Cancel: close the HTTP connection of the stream and the run stops.

## Settings

`settings.json` in `<home>`; edited on `/ui/settings` or with `POST /api/settings`.

| Field | Values | Meaning |
|---|---|---|
| `show_thoughts` | bool | send the model's reasoning as `thinking` events. When false they are not sent at all |
| `stream_speed` | `instant`, `fast`, `normal`, `slow` | how fast the interface types out the answer (the backend always streams at network speed) |
| `sidebar` | `auto`, `hidden` | the side panel of the terminal interface |
| `mascot` | bool | draw the mascot |
| `text_width` | 0 or 40–240 | wrap the chat at this many columns (0 = full width) |
| `mouse` | bool | the interface captures the mouse; off means text can be selected freely |
| `default_mode` | mode name or "" | the mode of new sessions |
| `autostart` | `{name: bool}` | start the process with the backend (overrides `autostart` of config.toml) |
| `process_args` | `{name: "args"}` | arguments used when a process is started without any |
| `keep_backend` | bool | do not stop the backend when the interface quits |

## Stream events

The field `type`; the other fields depend on it (empty ones are not sent).

| type | Fields | Meaning |
|---|---|---|
| `model` | `model` | the model started answering |
| `text` | `text` | a piece of the answer |
| `thinking` | `text` | a piece of the model's reasoning (not part of the answer); only when `show_thoughts` is on |
| `notice` | `text`, `kind` | service message: `fallback`, `rate_limit`, `key_switch`, `no_key`, `info` |
| `tool_call` | `tool`, `call_id`, `args`, `desc` | the agent calls a tool |
| `approval` | `tool`, `call_id`, `args`, `desc` | waits for a decision; `call_id` is the id for `/api/approvals/{id}`. `tool` = `switch_model` asks whether Relay may move to another model after the chosen one failed (`allow` = switch and keep the new model, `always` = do not ask again in this session, `deny` = end the request with the error) |
| `tool_result` | `tool`, `result`, `is_error` | the result of a tool |
| `usage` | `model`, `in`, `out`, `cost` | usage (USD at the prices of the configuration); also added to the totals of the session |
| `error` | `text`, `kind` | `rate_limit`, `no_key` (the provider has no key), `provider`, `internal` |
| `done` | | the run is finished |

In `/api/compare` the events also carry `slot` (1, 2, ...), the number of the model in the request.

## Example

```bash
TOKEN=$(cat ~/.config/relay/token)
SID=$(curl -s -X POST localhost:8787/api/sessions -H "Authorization: Bearer $TOKEN" \
        -d '{"mode":"medium"}' | jq -r .id)
curl -N -X POST localhost:8787/api/sessions/$SID/messages \
     -H "Authorization: Bearer $TOKEN" -d '{"text":"Hello"}'
```

## OpenAI gateway

Compatible in format with `/v1/chat/completions` and `/v1/models`: streaming and plain modes,
`usage` in the answer. `model`: `relay-small|medium|hard|maximum`, `auto`, or a model id.
Not supported: `tools` and `function_calling` (requests with them are handled as plain chat).

## Protection

- While listening on loopback the server answers only to the Host `127.0.0.1`, `localhost`, `::1` (DNS rebinding).
- The pages `/ui/keys`, `/ui/settings` and `/ui/help` are static and contain no secrets; the CSP forbids outside resources.
- Keys are stored in `<home>/keys.json` (0600); the API returns only the last 4 characters.
