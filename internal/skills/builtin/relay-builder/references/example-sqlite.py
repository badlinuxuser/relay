"""Example: a tool for a local database (SQLite), read only: the user can ask questions about their data.

Create it with create_mcp_server:  id "shopdb", kind "tool", env {"DB_PATH": "/home/me/shop.db"},
and a skill (required): when to use it, the names of the main tables, that only SELECT works,
that results are cut to 50 rows (ask for aggregates with GROUP BY / COUNT instead of everything).
The database is opened read-only, so even a wrong query cannot change anything.
"""
import os
import sqlite3

from relay_mcp import Server, ToolError

srv = Server("shopdb")
MAX_ROWS = 50


def connect():
    path = os.path.abspath(os.path.expanduser(os.environ.get("DB_PATH", "")))
    if not os.environ.get("DB_PATH") or not os.path.exists(path):
        raise ToolError("the database file was not found: set DB_PATH in the env of this server")
    con = sqlite3.connect("file:%s?mode=ro" % path, uri=True, timeout=5)
    con.execute("PRAGMA query_only = ON")
    return con


@srv.tool("list_tables", "List the tables of the database with the number of rows of each.", {})
def list_tables():
    con = connect()
    names = [r[0] for r in con.execute("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")]
    return "\n".join("%s (%d rows)" % (n, con.execute('SELECT COUNT(*) FROM "%s"' % n.replace('"', '""')).fetchone()[0]) for n in names) or "No tables."


@srv.tool("describe_table", "Show the columns of a table.", {"table": "string: a name from list_tables"})
def describe_table(table):
    con = connect()
    if not con.execute("SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?", (table,)).fetchone():
        raise ToolError("there is no table %r; call list_tables" % table)
    return "\n".join("%s %s%s" % (c[1], c[2], " (key)" if c[5] else "") for c in con.execute('PRAGMA table_info("%s")' % table.replace('"', '""')))


@srv.tool("query", "Run one SELECT query and show the rows (at most 50). Only reading is possible.",
          {"sql": "string: a single SELECT (or WITH ... SELECT) statement"})
def query(sql):
    s = sql.strip().rstrip(";").strip()
    if not s.lower().startswith(("select", "with")) or ";" in s:
        raise ToolError("only one SELECT statement is allowed")
    con = connect()
    try:
        cur = con.execute(s)
        rows = cur.fetchmany(MAX_ROWS + 1)
    except sqlite3.Error as e:
        raise ToolError("the query failed: %s" % e) from None
    head = [d[0] for d in cur.description or []]
    out = [" | ".join(head)] + [" | ".join("" if v is None else str(v) for v in r) for r in rows[:MAX_ROWS]]
    if len(rows) > MAX_ROWS:
        out.append("[cut at %d rows: add LIMIT, or use COUNT / GROUP BY]" % MAX_ROWS)
    return "\n".join(out)


srv.run()
