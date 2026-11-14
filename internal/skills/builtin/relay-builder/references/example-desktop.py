"""Example: a tool that controls THIS computer's desktop: open a site, search in the browser, start an
application, show a notification.

Relay runs on the user's own computer, so a program that it starts can open the user's browser and
applications. (The session variables DISPLAY / WAYLAND_DISPLAY reach the program.) Do the SIMPLEST
thing: "open Firefox and search YouTube" needs no browser automation (no Selenium, no headless
mode): it is just an address, opened with the user's own browser.

Create it with create_mcp_server:  id "desktop", kind "tool", no env, no oauth,
and a skill (required): "Use when the user wants something opened or searched on screen: a site, a
search on YouTube or Google, an application. open_search covers the usual search sites; for any other
site use open_url. open_app starts a program by its name, e.g. firefox."
"""
import os
import shutil
import subprocess
import sys
import urllib.parse
import webbrowser

from relay_mcp import Server, ToolError

srv = Server("desktop")

SEARCH = {
    "youtube": "https://www.youtube.com/results?search_query=",
    "google": "https://www.google.com/search?q=",
    "duckduckgo": "https://duckduckgo.com/?q=",
    "wikipedia": "https://en.wikipedia.org/w/index.php?search=",
    "maps": "https://www.google.com/maps/search/",
}


def open_address(url):
    """Open an address in the user's default browser (or the one named in BROWSER)."""
    if not url.lower().startswith(("http://", "https://")):
        raise ToolError("only http:// and https:// addresses can be opened")
    if not webbrowser.open(url):
        # a fallback for desktops where Python finds no browser
        tool = shutil.which("xdg-open") or shutil.which("open")
        if not tool:
            raise ToolError("no browser could be started on this computer (no graphical session?)")
        subprocess.Popen([tool, url], stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                         start_new_session=True)


@srv.tool("open_url", "Open a web address in the user's browser.", {"url": "string: a full address starting with https://"})
def open_url(url):
    open_address(url)
    return "opened " + url


@srv.tool("open_search", "Search a site in the user's browser: opens the page with the results.",
          {"site": "string: youtube, google, duckduckgo, wikipedia or maps", "query": "string: what to search for"})
def open_search(site, query):
    base = SEARCH.get(site.lower())
    if not base:
        raise ToolError("unknown site %r; use one of: %s, or open_url with an address" % (site, ", ".join(SEARCH)))
    url = base + urllib.parse.quote(query, safe="")
    open_address(url)
    return "opened the %s results for %r" % (site, query)


@srv.tool("open_app", "Start an application of the user by its name (for example firefox, code, gimp). No shell is used.",
          {"name": "string: the program name as it is typed in a terminal", "args": "array?: arguments, as a list of strings"})
def open_app(name, args=None):
    if "/" in name or "\\" in name or name.startswith("-"):
        raise ToolError("give the program name only (like firefox), not a path")
    path = shutil.which(name)
    if not path:
        raise ToolError("the program %r was not found on this computer; ask the user for the right name" % name)
    argv = [path] + [str(a) for a in (args or [])]
    proc = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                            start_new_session=True)  # it keeps running after this tool call
    return "started %s (process %d)" % (name, proc.pid)


@srv.tool("notify", "Show a desktop notification to the user.", {"title": "string", "text": "string"})
def notify(title, text):
    if sys.platform == "darwin":
        script = 'display notification "%s" with title "%s"' % (text.replace('"', "'"), title.replace('"', "'"))
        cmd = ["osascript", "-e", script]
    else:
        cmd = ["notify-send", title, text]
    if not shutil.which(cmd[0]):
        raise ToolError("%s is not installed, so no notification can be shown" % cmd[0])
    subprocess.run(cmd, check=False, timeout=10)
    return "shown"


srv.run()
