# mcp.json (for reference: the tools write it for you)

`create_mcp_server` and `remove_extension` edit this file; do not edit it yourself.

```json
{
  "servers": {
    "notes": {
      "name": "Notes", "kind": "tool", "type": "stdio",
      "command": "python3", "args": ["/home/me/.config/relay/connectors/notes/server.py"],
      "env": {"NOTES_DIR": "${HOME}/notes"},
      "disabled_tools": ["write_note"]
    },
    "github": {
      "name": "GitHub", "kind": "connector", "type": "stdio",
      "command": "python3", "args": [".../connectors/github/server.py"],
      "oauth": {"authorize_url": "https://github.com/login/oauth/authorize",
                "token_url": "https://github.com/login/oauth/access_token",
                "client_id": "Iv1.abc", "client_secret": "${GITHUB_CLIENT_SECRET}", "scopes": ["repo"]}
    },
    "docs": {"name": "Docs", "kind": "connector", "type": "http", "url": "https://example.com/mcp"}
  }
}
```

- `kind`: `tool` works on this computer (shown by /tools); `connector` works with an outside service
  (shown by /connectors). A remote server or one with an `oauth` block is a connector by default.
- `type`: `stdio` (a program) or `http` (a remote server, Streamable HTTP).
- `env`: extra variables for the program. `${NAME}` takes the value from the user's environment.
  The program gets only a small environment: Relay's own API keys are not passed on.
- `oauth`: how to sign the user in. For a remote `http` server that follows the MCP authorization
  specification this block is not needed.
- `disabled_tools`: tools that are switched off.
- A program runs only after the user approved exactly this command; `create_mcp_server` records the
  approval of the user who approved the tool call.

States (shown by /connectors): `configured`, `connecting`, `connected`, `auth_required`
(sign-in needed), `disconnected`, `error`.

## Authorize and token addresses of common services (check the service's documentation if unsure)

| Service | authorize_url | token_url | notes |
|---|---|---|---|
| GitHub (OAuth app) | https://github.com/login/oauth/authorize | https://github.com/login/oauth/access_token | scopes like `repo`, `read:user`; tokens do not expire unless enabled |
| Google | https://accounts.google.com/o/oauth2/v2/auth | https://oauth2.googleapis.com/token | `params`: `{"access_type": "offline", "prompt": "consent"}` to get a refresh token; scopes are full URLs |
| Spotify | https://accounts.spotify.com/authorize | https://accounts.spotify.com/api/token | |

The OAuth app must list the callback address that `list_extensions` shows. The address is stable only
while Relay keeps its configured listen port.
