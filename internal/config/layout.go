package config

import (
	"os"
	"path/filepath"
)

// The files and folders that make up the extensions: plain files, no database.
//
//	<home>/mcp.json     MCP servers (the source of truth, edit by hand or with `relay mcp`)
//	<home>/skills/      one folder per skill, each with a SKILL.md
//	<home>/auth/        OAuth tokens and other credentials (owner only)

func MCPPath(home string) string      { return filepath.Join(home, "mcp.json") }
func SkillsDir(home string) string    { return filepath.Join(home, "skills") }
func AuthDir(home string) string      { return filepath.Join(home, "auth") }
func SkillsState(home string) string  { return filepath.Join(home, "skills.json") }
func MCPTrustPath(home string) string { return filepath.Join(home, "mcp-trust.json") }

const emptyMCP = "{\n  \"servers\": {}\n}\n"

// EnsureLayout creates what is missing: mcp.json (empty), skills/ and auth/ (owner only).
func EnsureLayout(home string) error {
	if err := os.MkdirAll(SkillsDir(home), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(AuthDir(home), 0o700); err != nil {
		return err
	}
	_ = os.Chmod(AuthDir(home), 0o700) // no effect on Windows, which is fine
	p := MCPPath(home)
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return os.WriteFile(p, []byte(emptyMCP), 0o600)
	}
	return nil
}
