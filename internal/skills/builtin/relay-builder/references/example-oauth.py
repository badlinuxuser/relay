"""Example: a connector with OAuth sign-in: GitHub, read-only plus one writing tool.

Create it with create_mcp_server:
  id "github", kind "connector",
  oauth {"authorize_url": "https://github.com/login/oauth/authorize",
         "token_url": "https://github.com/login/oauth/access_token",
         "client_id": "<the client id of the user's OAuth app>",
         "client_secret": "${GITHUB_CLIENT_SECRET}",
         "scopes": ["repo"]}
The callback address of the OAuth app must be the one that list_extensions shows.
"""
from relay_mcp import Server, ToolError, http

srv = Server("github")
API = "https://api.github.com"
HEADERS = {"Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28"}


def check_name(value, what):
    if not value or not all(c.isalnum() or c in "-_." for c in value):
        raise ToolError("%s may contain only letters, digits, '-', '_' and '.'" % what)
    return value


@srv.tool("whoami", "Show which GitHub account the user is signed in with.", {})
def whoami():
    me = http("GET", API + "/user", headers=HEADERS, oauth=True)
    return "%s (%s)" % (me["login"], me.get("name") or "no name")


@srv.tool("list_my_repos", "List the repositories of the signed-in user, most recently updated first.",
          {"limit": "integer?: how many (default 10, at most 30)"})
def list_my_repos(limit=10):
    items = http("GET", API + "/user/repos", params={"sort": "updated", "per_page": min(max(limit, 1), 30)},
                 headers=HEADERS, oauth=True)
    return "\n".join("%s%s  %s" % (r["full_name"], " (private)" if r["private"] else "", r.get("description") or "")
                     for r in items) or "No repositories."


@srv.tool("list_issues", "List open issues of a repository (pull requests are not included).",
          {"owner": "string", "repo": "string", "limit": "integer?: default 10"})
def list_issues(owner, repo, limit=10):
    path = "/repos/%s/%s/issues" % (check_name(owner, "owner"), check_name(repo, "repo"))
    items = http("GET", API + path, params={"state": "open", "per_page": min(max(limit, 1), 30)},
                 headers=HEADERS, oauth=True)
    items = [i for i in items if "pull_request" not in i]  # this address mixes in pull requests
    return "\n".join("#%d %s (by %s)" % (i["number"], i["title"], i["user"]["login"]) for i in items) or "No open issues."


@srv.tool("create_issue", "WRITES to GitHub: create an issue in a repository.",
          {"owner": "string", "repo": "string", "title": "string", "body": "string?"})
def create_issue(owner, repo, title, body=""):
    path = "/repos/%s/%s/issues" % (check_name(owner, "owner"), check_name(repo, "repo"))
    issue = http("POST", API + path, body={"title": title, "body": body}, headers=HEADERS, oauth=True)
    return "created #%d: %s" % (issue["number"], issue["html_url"])


srv.run()
