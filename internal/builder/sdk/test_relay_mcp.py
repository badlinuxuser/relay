import json, os, subprocess, sys, tempfile, time, textwrap, threading
from http.server import BaseHTTPRequestHandler, HTTPServer

SDK = os.path.dirname(os.path.abspath(__file__))
tmp = tempfile.mkdtemp()
tokfile = os.path.join(tmp, "t.token")

class H(BaseHTTPRequestHandler):
    def log_message(self,*a): pass
    def do_GET(self):
        if self.path.startswith("/ok"):
            body=json.dumps({"auth": self.headers.get("Authorization"), "q": self.path}).encode(); self.send_response(200); self.send_header("Content-Type","application/json")
        elif self.path.startswith("/unauth"):
            body=b"nope"; self.send_response(401)
        elif self.path.startswith("/redir"):
            body=b""; self.send_response(302); self.send_header("Location","http://127.0.0.2:9/x")
        else:
            body=b"not found"; self.send_response(404)
        self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)
srv = HTTPServer(("127.0.0.1",0),H); threading.Thread(target=srv.serve_forever,daemon=True).start()
base = "http://127.0.0.1:%d" % srv.server_port

code = textwrap.dedent('''
    from relay_mcp import Server, ToolError, http, secret
    srv = Server("demo")
    @srv.tool("add", "Add two numbers", {"a": "number: first", "b": "number"})
    def add(a, b): return a + b
    @srv.tool("greet", "Greet someone", {"name": "string", "times": "integer?: how many"})
    def greet(name, times=1): return " ".join(["hi " + name] * times)
    @srv.tool("mode", "Pick a mode", {"m": {"type": "string", "enum": ["a", "b"], "optional": True}, "flag": "boolean?"})
    def mode(m="a", flag=False): return {"m": m, "flag": flag}
    @srv.tool("boom", "Always fails", {})
    def boom(): return 1 / 0
    @srv.tool("fail", "Fails politely", {})
    def fail(): raise ToolError("nothing to do here; try add")
    @srv.tool("fetch", "GET a url", {"url": "string", "oauth": "boolean?"})
    def fetch(url, oauth=False): return http("GET", url, params={"x": 1}, oauth=oauth)
    @srv.tool("fetch_override", "OAuth auth header cannot be overridden", {"url": "string"})
    def fetch_override(url): return http("GET", url, oauth=True, headers={"Authorization": "Bearer attacker"})
    @srv.tool("key", "Read a secret", {})
    def key(): return secret("MY_KEY")
    @srv.tool("big", "Large output", {})
    def big(): return "x" * 200000
    @srv.tool("nothing", "Returns None", {})
    def nothing(): return None
    srv.run()
''')
open(os.path.join(tmp,"server.py"),"w").write(code)
env = {"PATH": os.environ["PATH"], "PYTHONPATH": SDK, "RELAY_OAUTH_TOKEN_FILE": tokfile}
p = subprocess.Popen([sys.executable, os.path.join(tmp,"server.py")], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env, text=True, bufsize=1)
n=[0]
def rpc(method, params=None):
    n[0]+=1; p.stdin.write(json.dumps({"jsonrpc":"2.0","id":n[0],"method":method,"params":params or {}})+"\n"); p.stdin.flush()
    r=json.loads(p.stdout.readline()); assert r["id"]==n[0]; return r
def call(_tool, **a):
    r=rpc("tools/call",{"name":_tool,"arguments":a})["result"]; return r.get("isError",False), r["content"][0]["text"]

r=rpc("initialize",{"protocolVersion":"2025-06-18"})["result"]; assert r["serverInfo"]["name"]=="demo"
tools={t["name"]:t for t in rpc("tools/list")["result"]["tools"]}
assert tools["add"]["inputSchema"]["required"]==["a","b"] and tools["add"]["inputSchema"]["properties"]["a"]["description"]=="first"
assert tools["greet"]["inputSchema"]["required"]==["name"], tools["greet"]
assert tools["greet"]["inputSchema"]["properties"]["times"]=={"type":"integer","description":"how many"}
assert tools["mode"]["inputSchema"]["required"]==[] and "optional" not in tools["mode"]["inputSchema"]["properties"]["m"]
def chk(label, res, text=None, err=False, contains=None):
    assert res[0]==err,(label,res)
    if text is not None: assert res[1]==text,(label,res)
    if contains: assert contains in res[1],(label,res)
    print("ok  ",label)
chk("add", call("add",a=2,b="3.5"), "5.5")
chk("optional default", call("greet",name="Ann"), "hi Ann")
chk("optional given as string int", call("greet",name="Ann",times="2"), "hi Ann hi Ann")
chk("json result", call("mode",flag="true"), contains='"flag": true')
chk("missing required", call("add",a=1), err=True, contains="'b' is required")
chk("bad type", call("add",a="x",b=1), err=True, contains="must be a number")
chk("bad int", call("greet",name="A",times=1.5), err=True, contains="must be an integer")
chk("ToolError", call("fail"), err=True, text="nothing to do here; try add")
chk("internal error shows the cause", call("boom"), err=True, contains="ZeroDivisionError")
chk("None -> ok", call("nothing"), "ok")
r=call("big"); assert r[0]==False and "cut:" in r[1] and len(r[1])<100100; print("ok   big output cut")
chk("unknown tool", call("nope"), err=True, contains="unknown tool")
chk("secret missing", call("key"), err=True, contains="MY_KEY is not set")
res=call("fetch",url=base+"/ok"); assert not res[0] and '"auth": null' in res[1] and "x=1" in res[1], res; print("ok   http json + params, no token by default")
chk("http 404", call("fetch",url=base+"/missing"), err=True, contains="HTTP 404")
chk("redirect to another host refused", call("fetch",url=base+"/redir"), err=True, contains="HTTP 302")
chk("no token file yet", call("fetch",url=base+"/ok",oauth=True), err=True, contains="relay:auth-required")
open(tokfile,"w").write(json.dumps({"access_token":"tok-9","expires_at":time.time()+3600,"resource":base}))
res=call("fetch",url=base+"/ok",oauth=True); assert '"auth": "Bearer tok-9"' in res[1], res; print("ok   oauth token sent as bearer")
res=call("fetch_override",url=base+"/ok"); assert '"auth": "Bearer tok-9"' in res[1], res; print("ok   oauth authorization header cannot be overridden")
chk("oauth resource binding", call("fetch",url=base.replace("127.0.0.1","127.0.0.2")+"/ok",oauth=True), err=True, contains="bound to")
chk("oauth path traversal blocked", call("fetch",url=base+"/%2e%2e/admin",oauth=True), err=True, contains="bound to")
chk("oauth redirects refused", call("fetch",url=base+"/redir",oauth=True), err=True, contains="HTTP 302")
open(tokfile,"w").write(json.dumps({"access_token":"old","expires_at":time.time()-5,"resource":base}))
chk("expired token", call("fetch",url=base+"/ok",oauth=True), err=True, contains="relay:auth-required")
open(tokfile,"w").write(json.dumps({"access_token":"tok-9","expires_at":time.time()+3600,"resource":base}))
chk("401 with oauth -> marker", call("fetch",url=base+"/unauth",oauth=True), err=True, contains="relay:auth-required")
assert rpc("nope")["error"]["code"]==-32601
p.stdin.close(); assert p.wait(timeout=5)==0; print("ok   exits at end of input")

# definition errors are reported when the file is loaded
bad = subprocess.run([sys.executable,"-c","from relay_mcp import Server\ns=Server('x')\n@s.tool('a','d',{'p':'strng'})\ndef f(p): pass"],env=env,capture_output=True,text=True)
assert bad.returncode!=0 and "unknown type 'strng'" in bad.stderr; print("ok   a typo in a type is caught at load")
print("\nALL GOOD")
