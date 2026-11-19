"""relay_mcp: a tiny framework for MCP servers that Relay runs (stdio, standard library only).

You write the tools; this file speaks the protocol, checks the arguments, turns results into
text, talks HTTP safely and reads the sign-in token that Relay keeps up to date.

    from relay_mcp import Server, ToolError, http, secret

    srv = Server("notes")

    @srv.tool("add", "Add two numbers", {"a": "number", "b": "number"})
    def add(a, b):
        return a + b

    srv.run()

Parameters are {name: "type"} with the type string, integer, number, boolean, array or
object. "integer?" makes a parameter optional; "string: text" adds a description for the model
("integer?: how many, default 10"). A dict is a JSON Schema fragment ({"type": "string",
"enum": ["a", "b"], "optional": True}). Optional parameters that the model leaves out are not
passed, so give them Python defaults.

The result may be text, a number, a list or a dict (shown as JSON). Raise ToolError("what
went wrong, and what to try") for a problem the model should read; any other exception is
reported as an internal error. Never print to stdout (it is the protocol channel): use log().
"""
import json
import os
import sys
import ipaddress
import socket
import ssl
import http.client
import time
import traceback
import urllib.error
import urllib.parse
import urllib.request

__all__ = ["Server", "ToolError", "http", "secret", "log"]

# A tool error that starts with this tells Relay that the service refused the token: Relay
# forgets it, asks the user to sign in again and repeats the call.
AUTH_MARKER = "relay:auth-required"

PROTOCOL_VERSIONS = ("2025-06-18", "2025-03-26", "2024-11-05")
MAX_RESULT_CHARS = 100000
MAX_HTTP_BYTES = 5 * 1024 * 1024
TYPES = ("string", "integer", "number", "boolean", "array", "object")


class ToolError(Exception):
    """A problem that the model should read (wrong argument, the service said no, ...)."""


def log(*parts):
    """Write a line to stderr (Relay shows the last one when the server fails to start)."""
    print(*parts, file=sys.stderr, flush=True)


def secret(name, hint=""):
    """Read a secret from the environment of the server (set it with "env" in mcp.json)."""
    value = os.environ.get(name, "").strip()
    if not value:
        raise ToolError("%s is not set. %s" % (name, hint or "Add it to the \"env\" of this server in mcp.json, e.g. \"%s\": \"${%s}\"." % (name, name)))
    return value


def _oauth_context():
    """Load the access token and its resource binding from Relay's private token file."""
    path = os.environ.get("RELAY_OAUTH_TOKEN_FILE")
    if not path:
        raise ToolError("this server has no \"oauth\" block in mcp.json, so there is no token")
    try:
        with open(path, encoding="utf-8") as f:
            data = json.load(f)
    except (OSError, ValueError):
        raise ToolError(AUTH_MARKER + " the user has not signed in yet") from None
    expires = data.get("expires_at")
    if expires and float(expires) < time.time() + 15:
        raise ToolError(AUTH_MARKER + " the token has expired")
    token = data.get("access_token")
    if not token:
        raise ToolError(AUTH_MARKER + " there is no token")
    resource = data.get("resource")
    if not resource:
        raise ToolError("oauth.resource is missing; configure the API resource in mcp.json")
    return token, resource


def oauth_token():
    """Raw tokens are intentionally not exposed; use http(..., oauth=True)."""
    raise ToolError("raw OAuth tokens are not exposed to connector code; use http(..., oauth=True) so Relay can enforce resource binding")


def _resource_ip(resource, url):
    """Validate resource binding and return a DNS-validated IP to pin the OAuth request to."""
    r = urllib.parse.urlsplit(resource)
    u = urllib.parse.urlsplit(url)
    if r.scheme not in ("https", "http") or u.scheme != r.scheme or not r.hostname or not u.hostname:
        return None
    if r.username or r.password or u.username or u.password or r.fragment or u.fragment:
        return None
    if r.netloc.lower() != u.netloc.lower():
        return None
    paths = (r.path, u.path)
    decoded_paths = []
    for path in paths:
        lower = path.lower()
        if any(marker in lower for marker in ("%2f", "%5c", "%25")):
            return None
        decoded = urllib.parse.unquote(path)
        if "\\" in decoded or any(part in (".", "..") for part in decoded.split("/")):
            return None
        decoded_paths.append(decoded)
    rp, up = decoded_paths[0].rstrip("/"), decoded_paths[1].rstrip("/")
    if rp and up != rp and not up.startswith(rp + "/"):
        return None
    if r.query and r.query != u.query:
        return None
    host = u.hostname.lower()
    try:
        host_is_loopback = ipaddress.ip_address(host).is_loopback
    except ValueError:
        host_is_loopback = host in ("localhost", "ip6-localhost")
    try:
        infos = socket.getaddrinfo(u.hostname, u.port or (443 if u.scheme == "https" else 80), type=socket.SOCK_STREAM)
        addresses = list(dict.fromkeys(item[4][0].split("%")[0] for item in infos))
        if not addresses:
            return None
        for address in addresses:
            ip = ipaddress.ip_address(address)
            if ip.is_loopback:
                if not host_is_loopback:
                    return None
            elif not ip.is_global:
                return None
        # Pin the request to one of the validated answers to close the DNS-rebinding window.
        return addresses[0]
    except (OSError, ValueError):
        return None


class _PinnedHTTPConnection(http.client.HTTPConnection):
    def __init__(self, host, port, pinned_ip, timeout):
        super().__init__(host, port, timeout=timeout)
        self._pinned_ip = pinned_ip

    def connect(self):
        self.sock = socket.create_connection((self._pinned_ip, self.port), self.timeout, self.source_address)


class _PinnedHTTPSConnection(http.client.HTTPSConnection):
    def __init__(self, host, port, pinned_ip, timeout):
        super().__init__(host, port, timeout=timeout, context=ssl.create_default_context())
        self._pinned_ip = pinned_ip

    def connect(self):
        sock = socket.create_connection((self._pinned_ip, self.port), self.timeout, self.source_address)
        if self._tunnel_host:
            self.sock = sock
            self._tunnel()
        self.sock = self._context.wrap_socket(sock, server_hostname=self.host)


def _oauth_request(method, url, data, headers, timeout, pinned_ip):
    u = urllib.parse.urlsplit(url)
    port = u.port or (443 if u.scheme == "https" else 80)
    cls = _PinnedHTTPSConnection if u.scheme == "https" else _PinnedHTTPConnection
    conn = cls(u.hostname, port, pinned_ip, timeout)
    path = urllib.parse.urlunsplit(("", "", u.path or "/", u.query, ""))
    req_headers = dict(headers)
    req_headers["Connection"] = "close"
    try:
        conn.request(method.upper(), path, body=data, headers=req_headers)
        resp = conn.getresponse()
        raw = resp.read(MAX_HTTP_BYTES + 1)
        ctype = resp.getheader("Content-Type", "")
        status, reason = resp.status, resp.reason
    except (OSError, socket.timeout, ssl.SSLError, http.client.HTTPException) as e:
        raise ToolError("cannot reach %s: %s" % (u.netloc, str(e)[:240])) from None
    finally:
        conn.close()
    if status == 401:
        raise ToolError(AUTH_MARKER + " HTTP 401 from %s (the token was refused)" % u.netloc)
    if 300 <= status < 400:
        raise ToolError("%s answered HTTP %d: OAuth redirects are refused" % (u.netloc, status))
    if status >= 400:
        snippet = raw[:600].decode("utf-8", "replace").strip()
        raise ToolError("%s answered HTTP %d: %s" % (u.netloc, status, snippet or reason))
    if len(raw) > MAX_HTTP_BYTES:
        raise ToolError("the answer of %s is larger than %d MB" % (u.netloc, MAX_HTTP_BYTES // (1024 * 1024)))
    text = raw.decode("utf-8", "replace")
    if "json" in ctype or text.lstrip().startswith(("{", "[")):
        try:
            return json.loads(text)
        except ValueError:
            pass
    return text


class _SameHost(urllib.request.HTTPRedirectHandler):
    """A redirect to another host is refused: the token must not follow it."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        old = urllib.parse.urlsplit(req.full_url)
        new = urllib.parse.urlsplit(newurl)
        if new.scheme != old.scheme or new.netloc.lower() != old.netloc.lower():
            raise urllib.error.HTTPError(newurl, code, "redirect to another origin refused", headers, fp)
        return super().redirect_request(req, fp, code, msg, headers, newurl)


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise urllib.error.HTTPError(newurl, code, "redirect refused for an OAuth request", headers, fp)


_OPENER = urllib.request.build_opener(_SameHost)
_OAUTH_OPENER = urllib.request.build_opener(_NoRedirect)


def http(method, url, *, params=None, body=None, form=None, headers=None, bearer=None, oauth=False, timeout=20):
    """Make an HTTP request and return the answer: parsed JSON, or text.

    params   query string values (dict)
    body     a JSON body (dict or list)
    form     a form body (dict)
    bearer   a token for the Authorization header
    oauth    True: use the signed-in user's token, bound to oauth.resource in mcp.json
    Problems become ToolError with the status and the start of the answer. An answer of
    401 with oauth=True makes Relay ask the user to sign in again.
    """
    if not url.startswith(("https://", "http://")):
        raise ToolError("the address must start with https://")
    if params:
        url += ("&" if "?" in url else "?") + urllib.parse.urlencode({k: v for k, v in params.items() if v is not None})
    data = None
    hdrs = {"Accept": "application/json", "User-Agent": "relay-connector"}
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        hdrs["Content-Type"] = "application/json"
    elif form is not None:
        data = urllib.parse.urlencode(form).encode("utf-8")
        hdrs["Content-Type"] = "application/x-www-form-urlencoded"
    pinned_ip = None
    if oauth:
        bearer, resource = _oauth_context()
        pinned_ip = _resource_ip(resource, url)
        if not pinned_ip:
            raise ToolError("OAuth token is bound to %s; refusing to send it to %s" % (resource, urllib.parse.urlsplit(url).netloc))
    hdrs.update(headers or {})
    if oauth:
        for key in (headers or {}):
            if key.lower() in ("host", "connection", "transfer-encoding", "proxy-authorization"):
                raise ToolError("the %s header cannot be overridden on an OAuth request" % key)
        hdrs["Authorization"] = "Bearer " + bearer
    elif bearer:
        hdrs["Authorization"] = "Bearer " + bearer
    req = urllib.request.Request(url, data=data, method=method.upper(), headers=hdrs)
    host = urllib.parse.urlsplit(url).netloc
    if oauth:
        return _oauth_request(method, url, data, hdrs, timeout, pinned_ip)
    try:
        with _OPENER.open(req, timeout=timeout) as resp:
            raw = resp.read(MAX_HTTP_BYTES + 1)
            ctype = resp.headers.get("Content-Type", "")
    except urllib.error.HTTPError as e:
        snippet = e.read(600).decode("utf-8", "replace").strip()
        if e.code == 401 and oauth:
            raise ToolError("%s %s answered 401 (the token was refused)" % (AUTH_MARKER, host)) from None
        raise ToolError("%s answered HTTP %d: %s" % (host, e.code, snippet or e.reason)) from None
    except urllib.error.URLError as e:
        raise ToolError("cannot reach %s: %s" % (host, e.reason)) from None
    except TimeoutError:
        raise ToolError("%s did not answer within %d seconds" % (host, timeout)) from None
    if len(raw) > MAX_HTTP_BYTES:
        raise ToolError("the answer of %s is larger than %d MB" % (host, MAX_HTTP_BYTES // (1024 * 1024)))
    text = raw.decode("utf-8", "replace")
    if "json" in ctype or text.lstrip().startswith(("{", "[")):
        try:
            return json.loads(text)
        except ValueError:
            pass
    return text


def _schema(params):
    """Turn the short parameter notation into a JSON Schema and the list of required names."""
    props, required = {}, []
    for name, spec in (params or {}).items():
        optional = False
        if isinstance(spec, str):
            typ, _, desc = spec.partition(":")
            typ = typ.strip()
            if typ.endswith("?"):
                typ, optional = typ[:-1].strip(), True
            if typ not in TYPES:
                raise ValueError("parameter %r: unknown type %r (use one of %s)" % (name, typ, ", ".join(TYPES)))
            prop = {"type": typ}
            if desc.strip():
                prop["description"] = desc.strip()
        elif isinstance(spec, dict):
            prop = dict(spec)
            optional = bool(prop.pop("optional", False))
            if "type" not in prop:
                raise ValueError("parameter %r: a dict needs a \"type\"" % name)
        else:
            raise ValueError("parameter %r: use a string like \"integer?\" or a dict" % name)
        props[name] = prop
        if not optional:
            required.append(name)
    return {"type": "object", "properties": props, "required": required}


def _coerce(name, value, typ):
    try:
        if typ == "string":
            return value if isinstance(value, str) else json.dumps(value) if isinstance(value, (dict, list)) else str(value)
        if typ == "integer":
            if isinstance(value, bool):
                raise ValueError
            if isinstance(value, float) and value != int(value):
                raise ValueError
            return int(value)
        if typ == "number":
            if isinstance(value, bool):
                raise ValueError
            return float(value)
        if typ == "boolean":
            if isinstance(value, bool):
                return value
            if str(value).lower() in ("true", "false"):
                return str(value).lower() == "true"
            raise ValueError
        if typ in ("array", "object"):
            if isinstance(value, str):
                value = json.loads(value)
            if not isinstance(value, list if typ == "array" else dict):
                raise ValueError
            return value
    except (ValueError, TypeError):
        raise ToolError("the argument %r must be %s %s" % (name, "an" if typ in ("integer", "array", "object") else "a", typ)) from None
    return value


def _text(result):
    if result is None:
        return "ok"
    if isinstance(result, bytes):
        result = result.decode("utf-8", "replace")
    if not isinstance(result, str):
        result = json.dumps(result, indent=2, ensure_ascii=False, default=str)
    if len(result) > MAX_RESULT_CHARS:
        result = result[:MAX_RESULT_CHARS] + "\n[... cut: %d more characters]" % (len(result) - MAX_RESULT_CHARS)
    return result


class Server:
    """An MCP server. Add tools with @server.tool(...), then call run()."""

    def __init__(self, name, version="1.0.0"):
        self.name, self.version = name, version
        self._tools = {}

    def tool(self, name, description, params=None):
        """Decorator: register a function as a tool."""
        if not name or not all(c.isalnum() or c in "_-" for c in name):
            raise ValueError("tool name %r: use letters, digits, '_' and '-'" % name)
        if name in self._tools:
            raise ValueError("tool %r is defined twice" % name)
        if not description or not description.strip():
            raise ValueError("tool %r needs a description: the model reads it to decide when to use the tool" % name)
        schema = _schema(params)

        def register(fn):
            self._tools[name] = {"description": description.strip(), "schema": schema, "fn": fn}
            return fn
        return register

    # ------------------------------------------------------------ protocol

    def _send(self, msg):
        sys.stdout.write(json.dumps(msg, ensure_ascii=False) + "\n")
        sys.stdout.flush()

    def _reply(self, id_, result=None, error=None):
        msg = {"jsonrpc": "2.0", "id": id_}
        if error is not None:
            msg["error"] = error
        else:
            msg["result"] = result
        self._send(msg)

    def _call(self, params):
        name = params.get("name")
        tool = self._tools.get(name)
        if tool is None:
            return {"content": [{"type": "text", "text": "unknown tool %r" % name}], "isError": True}
        try:
            args = params.get("arguments") or {}
            props, required = tool["schema"]["properties"], tool["schema"]["required"]
            for r in required:
                if args.get(r) is None:
                    raise ToolError("the argument %r is required" % r)
            call = {k: _coerce(k, v, props[k]["type"]) for k, v in args.items() if k in props and v is not None}
            return {"content": [{"type": "text", "text": _text(tool["fn"](**call))}]}
        except ToolError as e:
            return {"content": [{"type": "text", "text": str(e)}], "isError": True}
        except Exception as e:  # a bug in a tool must not stop the server
            traceback.print_exc(file=sys.stderr)
            return {"content": [{"type": "text", "text": "internal error: %s: %s" % (type(e).__name__, e)}], "isError": True}

    def _handle(self, msg):
        method, id_ = msg.get("method"), msg.get("id")
        if method is None or id_ is None:
            return  # a notification or an answer to us
        params = msg.get("params") or {}
        if method == "initialize":
            asked = params.get("protocolVersion")
            self._reply(id_, {
                "protocolVersion": asked if asked in PROTOCOL_VERSIONS else PROTOCOL_VERSIONS[0],
                "capabilities": {"tools": {}},
                "serverInfo": {"name": self.name, "version": self.version},
            })
        elif method == "ping":
            self._reply(id_, {})
        elif method == "tools/list":
            self._reply(id_, {"tools": [
                {"name": n, "description": t["description"], "inputSchema": t["schema"]} for n, t in self._tools.items()]})
        elif method == "tools/call":
            self._reply(id_, self._call(params))
        else:
            self._reply(id_, error={"code": -32601, "message": "method not found: %s" % method})

    def run(self):
        """Serve until Relay closes the input."""
        for stream in (sys.stdin, sys.stdout):
            if hasattr(stream, "reconfigure"):
                stream.reconfigure(encoding="utf-8")
        if not self._tools:
            log("%s: no tools are defined" % self.name)
        log("%s started with %d tools" % (self.name, len(self._tools)))
        while True:
            line = sys.stdin.readline()
            if not line:
                return
            line = line.strip()
            if not line:
                continue
            try:
                msg = json.loads(line)
            except ValueError:
                continue
            if isinstance(msg, dict):
                try:
                    self._handle(msg)
                except BrokenPipeError:
                    return
