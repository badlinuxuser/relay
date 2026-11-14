"""Example: a tool server that works on this computer: simple notes kept as text files.

Create it with create_mcp_server:  id "notes", kind "tool", env {"NOTES_DIR": "${HOME}/notes"},
and a skill: {"description": "Keep notes ... Use when the user wants to save or find a note.",
              "instructions": "list_notes first; names are single words; write_note replaces unless append is true ..."}
"""
import os

from relay_mcp import Server, ToolError

srv = Server("notes")
ROOT = os.path.abspath(os.path.expanduser(os.environ.get("NOTES_DIR") or "~/notes"))


def path_of(name):
    """The file of a note; refuses anything that would leave the notes folder."""
    if not name or "/" in name or "\\" in name or name.startswith("."):
        raise ToolError("a note name is a simple word like 'ideas' (no folders, no dots at the start)")
    return os.path.join(ROOT, name + ".txt")


@srv.tool("list_notes", "List the names of all notes.", {})
def list_notes():
    if not os.path.isdir(ROOT):
        return "No notes yet."
    names = sorted(f[:-4] for f in os.listdir(ROOT) if f.endswith(".txt"))
    return "\n".join(names) or "No notes yet."


@srv.tool("read_note", "Read one note.", {"name": "string: the note name from list_notes"})
def read_note(name):
    try:
        with open(path_of(name), encoding="utf-8") as f:
            return f.read()
    except FileNotFoundError:
        raise ToolError("there is no note %r; call list_notes to see the names" % name) from None


@srv.tool("write_note", "WRITES: create a note, or replace the text of an existing one.",
          {"name": "string", "text": "string", "append": "boolean?: add to the end instead of replacing"})
def write_note(name, text, append=False):
    os.makedirs(ROOT, exist_ok=True)
    with open(path_of(name), "a" if append else "w", encoding="utf-8") as f:
        f.write(text + ("\n" if append else ""))
    return "saved %s" % name


@srv.tool("search_notes", "Find the notes that contain a word.", {"word": "string", "limit": "integer?: default 10"})
def search_notes(word, limit=10):
    if not os.path.isdir(ROOT):
        return "No notes yet."
    hits = []
    for f in sorted(os.listdir(ROOT)):
        if f.endswith(".txt"):
            with open(os.path.join(ROOT, f), encoding="utf-8") as fh:
                if word.lower() in fh.read().lower():
                    hits.append(f[:-4])
    return hits[:limit] or "Nothing found."


srv.run()
