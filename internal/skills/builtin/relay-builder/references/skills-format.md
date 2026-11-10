# The skill format (Agent Skills)

A skill is a folder named like the skill, with a `SKILL.md`:

```
release-notes/
  SKILL.md              required
  references/style.md   optional: read only when the instructions point to it
  scripts/collect.py    optional: never run automatically
```

```markdown
---
name: release-notes
description: Write release notes from a list of changes. Use when the user asks for release notes, a changelog or a summary of what changed.
---

# Release notes

1. Group the changes: Added, Changed, Fixed.
2. One line per change, present tense, no internal ticket numbers.
3. For the tone see references/style.md.
```

- `name`: up to 64 characters, lowercase letters, digits and single hyphens; equal to the folder name.
- `description`: up to 1024 characters; what the skill does and when to use it (this is what the model sees in the list).
- Optional header fields: `license`, `compatibility`, `metadata` (name: value lines), `allowed-tools` (informational in Relay).
- `create_skill` writes this for you from `name`, `description`, `instructions` and `files`.
