# relay_mcp: the helper for MCP servers

`create_mcp_server` puts this file next to your `server.py`, so you simply `from relay_mcp import ...`.
Python 3.8+, standard library only.

```python
from relay_mcp import Server, ToolError, http, secret, oauth_token, log

srv = Server("github")            # the name Relay shows; optional second argument: version

@srv.tool("get_repo", "Show facts about one repository", {
    "owner": "string: user or organisation",
    "repo": "string",
    "limit": "integer?: how many topics to show (default 5)",
})
def get_repo(owner, repo, limit=5):
    data = http("GET", f"https://api.github.com/repos/{owner}/{repo}", bearer=secret("GITHUB_TOKEN"))
    return {"name": data["full_name"], "stars": data["stargazers_count"]}

srv.run()                         # last line: serve until Relay stops the program
```

## Tools

`@srv.tool(name, description, params)`: name uses letters, digits, `_` and `-`. The description is
what the model reads to decide when to call the tool: say what it does, what it returns, and
whether it writes anything.

`params` is `{argument_name: spec}`:
- `"string"`, `"integer"`, `"number"`, `"boolean"`, `"array"`, `"object"`: a required argument
- `"integer?"`: optional (give the Python argument a default)
- `"string: some words"` / `"integer?: some words"`: with a description for the model
- a dict is a JSON Schema fragment, and `"optional": True` makes it optional:
  `{"type": "string", "enum": ["open", "closed"], "optional": True}`

Arguments arrive as keyword arguments, already converted to the declared type. Optional arguments
the model leaves out are not passed.

## Results and errors

- return text, a number, a list or a dict (shown as JSON); `None` means "ok"
- `raise ToolError("what went wrong and what to try")`: shown to the model as an error
- any other exception is reported as `internal error: Type: message` (the traceback goes to stderr)
- never `print()`: stdout is the protocol. Use `log("text")`, which writes to stderr

## Helpers

- `http(method, url, params=None, body=None, form=None, headers=None, bearer=None, oauth=False, timeout=20)`
  returns parsed JSON or text. `body` is a JSON body, `form` a form body, `bearer` a token,
  `oauth=True` the signed-in user's token. HTTP errors become `ToolError` with the status and the
  start of the answer. Redirects to another host are refused. With `oauth=True` an answer of
  401 makes Relay ask the user to sign in again.
- `secret("NAME")`: an environment variable of the server; a clear `ToolError` if it is empty.
  Variables are set in the `env` of `create_mcp_server`, with `"${NAME}"` to take the value from
  the user's own environment.
- `oauth_token()`: the access token as text (Relay keeps it fresh). Prefer `http(..., oauth=True)`.

## Environment and files

The program runs with the user's rights, in its own folder, with a small environment (PATH, HOME,
language and proxy settings, plus your `env`). Relay's API keys are not passed on. Keep state, if
any, in a file next to `server.py` (`os.path.dirname(__file__)`).
