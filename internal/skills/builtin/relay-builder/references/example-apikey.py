"""Example: a connector for a service that uses a TOKEN or an API KEY, not OAuth: a Telegram bot.

A Telegram bot has a token from @BotFather. That is all it takes: there is no OAuth for bots, so no
"oauth" block. The token stays in an environment variable and is read with secret().

Create it with create_mcp_server:
  id "telegram", kind "connector", env {"TELEGRAM_BOT_TOKEN": "${TELEGRAM_BOT_TOKEN}"},
  and a skill (required): when to use these tools, that the user must write to the bot first so that
  get_updates shows their chat id, that send_message WRITES.
Tell the user: create the bot with @BotFather, `export TELEGRAM_BOT_TOKEN=...` in the shell profile,
and restart Relay (`relay stop`, then start it again) so that it sees the variable.
"""
import os

from relay_mcp import Server, ToolError, http, secret

srv = Server("telegram")
API = os.environ.get("TELEGRAM_API", "https://api.telegram.org")


def call(method, **params):
    token = secret("TELEGRAM_BOT_TOKEN", "Create a bot with @BotFather and export TELEGRAM_BOT_TOKEN, then restart Relay.")
    data = http("POST", "%s/bot%s/%s" % (API, token, method), body=params)
    if not isinstance(data, dict) or not data.get("ok"):
        raise ToolError("Telegram said no: %s" % (data.get("description") if isinstance(data, dict) else data))
    return data["result"]


@srv.tool("whoami", "Show which bot this is (name and username). Use it to check that the token works.", {})
def whoami():
    me = call("getMe")
    return "@%s (%s)" % (me.get("username"), me.get("first_name"))


@srv.tool("get_updates",
          "Show the latest messages that people sent to the bot, with the chat id of each. Use it to find the chat id of the user: they must write to the bot first.",
          {"limit": "integer?: how many (default 10, at most 30)"})
def get_updates(limit=10):
    items = call("getUpdates", limit=min(max(limit, 1), 30), timeout=0)
    lines = []
    for u in items:
        m = u.get("message") or u.get("channel_post")
        if m:
            who = (m.get("from") or {}).get("username") or m["chat"].get("title") or "?"
            lines.append("chat_id %s  from %s: %s" % (m["chat"]["id"], who, (m.get("text") or "(not text)")[:200]))
    return "\n".join(lines) or "No messages yet. Ask the user to write to the bot first."


@srv.tool("send_message", "WRITES to Telegram: send a text message from the bot to a chat.",
          {"chat_id": "string: the chat id (see get_updates)", "text": "string: the message, up to 4000 characters"})
def send_message(chat_id, text):
    if len(text) > 4000:
        raise ToolError("the text is longer than 4000 characters: split it into several messages")
    sent = call("sendMessage", chat_id=chat_id, text=text)
    return "sent (message %s)" % sent["message_id"]


srv.run()
