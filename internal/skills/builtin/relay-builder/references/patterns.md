# Recipes: what to build for typical requests

Always look for the SIMPLEST thing that does the job. Most requests need 2 to 5 small tools, standard library only.

| The user says (examples) | Build | How it logs in | Notes |
|---|---|---|---|
| "send this to my Telegram", "message me on Telegram when you finish" | connector: Telegram Bot API | bot token from @BotFather, in an environment variable. **No OAuth.** | `references/example-apikey.py`. The user must write to the bot first, so that `get_updates` shows the chat id. A personal account (reading private chats) needs MTProto and a library: say so honestly and offer the bot. |
| "post it to Slack / Discord / Teams" | connector | **incoming webhook URL** (one secret, simplest) or a bot token | A webhook is a URL you POST JSON to: `http("POST", secret("SLACK_WEBHOOK"), body={"text": ...})`. |
| "what is on my calendar tomorrow", "add a meeting" | connector | Google: OAuth (see `example-oauth.py`). Read-only: the secret ICS link of the calendar, no login at all | Time zones: ask which one, pass ISO dates. |
| "read my mail", "reply to this e-mail" | connector | IMAP/SMTP with an app password (standard library: `imaplib`, `smtplib`), or Gmail OAuth | Never send mail without the text and the recipient shown to the user (the approval does it). |
| "open youtube and search for ...", "open my browser", "start Firefox" | tool for this computer | none | `references/example-desktop.py`. **No browser automation**: an address opened in the user's browser is enough. |
| "show me a notification when ...", "take a screenshot" | tool for this computer | none | `notify-send` / `osascript`; a screenshot program writes a file: return its path. |
| "turn on the lights", "set the thermostat" | connector, local network | token of the hub (Home Assistant long-lived token, Hue key) in env | The hub has a REST API on the local network. |
| "what is in our database", "orders from yesterday" | tool | a read-only user or a read-only file | SQLite: `references/example-sqlite.py`. Postgres/MySQL need drivers (not standard): call the client program (`psql`, `mysql`) with a read-only user, arguments as a list, never a shell string. |
| "weather", "exchange rate", "news", "wikipedia" | connector | most have a free API with no key | `references/example-public-api.py`. |
| "check the status of my order in our shop" | connector | the API key of the shop in env | |
| "do the release notes the way we always do", "always answer in this format", "use our checklist" | **a skill only** (`create_skill`) | no code | The user wants a procedure, not an ability. |
| "the service has an MCP server at https://..." | `create_mcp_server` with `url` | the server asks by itself | No code. Still write a skill. |

## Design rules

- **Small tools, one job each.** 3 to 8 tools. Reading tools first; writing tools separate and marked "WRITES".
- **Tools run when called.** There is no background loop and no scheduler: "tell me when X happens" cannot be a tool that waits. Offer a `check_x` tool and say that the user asks again (or uses cron).
- **Arguments:** validate names, numbers, paths. A file tool stays inside one folder and refuses `..`. Pass program arguments as a list, never build a shell string from the model's text.
- **Limits:** a `limit` argument (default 10) on lists; cut long texts; return what is needed.
- **State** (a cursor, a cache) goes into a file next to `server.py` (`os.path.dirname(os.path.abspath(__file__))`).
- **Long jobs:** return quickly; start the job in the background (`subprocess.Popen(..., start_new_session=True)`) and give a `status` tool.
- **Honesty:** if the standard library cannot do it (browser automation, MTProto, a heavy driver), say what is possible instead; do not build something that cannot work.

## When a tool you built does not work

Do not give up and do not say that you cannot: repair it (with the user's permission).
1. Read the error. Common causes: a wrong assumption about this computer (no such program, a path), a missing variable (the user must export it and restart Relay), something printed to stdout, a timeout, an API that works differently than you thought.
2. `propose_extension` with kind `fix`, one sentence about what is broken. If the user refuses, the request is cancelled.
3. `read_server` to see the current code, change it, `create_mcp_server` again with `replace: true` (and the skill again).
4. Test with the same call that failed.
