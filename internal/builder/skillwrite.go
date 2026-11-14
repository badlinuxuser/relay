package builder

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"relay/internal/skills"
)

// skillSpec describes a skill that is about to be written.
type skillSpec struct {
	Name         string
	Description  string
	Instructions string
	Files        map[string]string
	Meta         map[string]string // metadata of the SKILL.md header
	Replace      bool
	Server       string // the MCP server this skill belongs to ("" for a skill of its own)
}

// Metadata keys of the skills that the builder writes.
const (
	metaCreatedBy = "created-by"
	metaServer    = "server"
	createdBy     = "relay-builder"
)

func skillMarkdown(name, desc, body string, meta map[string]string) string {
	var b strings.Builder
	b.WriteString("---\nname: " + name + "\ndescription: " + strconv.Quote(desc) + "\n")
	if len(meta) > 0 {
		keys := make([]string, 0, len(meta))
		for k := range meta {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("metadata:\n")
		for _, k := range keys {
			b.WriteString("  " + k + ": " + strconv.Quote(meta[k]) + "\n")
		}
	}
	b.WriteString("---\n\n" + strings.TrimSpace(body) + "\n")
	return b.String()
}

// deriveSkillName makes a valid skill name from a server id: lower case, hyphens.
func deriveSkillName(id string) string {
	n := strings.ToLower(strings.ReplaceAll(id, "_", "-"))
	n = strings.Trim(n, "-")
	for strings.Contains(n, "--") {
		n = strings.ReplaceAll(n, "--", "-")
	}
	return n
}

// checkSkill validates a spec before anything is written.
func (b *Builder) checkSkill(sp *skillSpec) error {
	sp.Name = strings.TrimSpace(sp.Name)
	sp.Description = strings.Join(strings.Fields(sp.Description), " ")
	if !skills.ValidName(sp.Name) {
		return fmt.Errorf("the skill name %q is not valid: use lowercase letters, digits and single hyphens (like release-notes)", sp.Name)
	}
	if utf8.RuneCountInString(sp.Description) < 10 {
		return errors.New("the skill description is too short: say what the skill does and when to use it")
	}
	if strings.TrimSpace(sp.Instructions) == "" {
		return errors.New("the skill instructions are empty")
	}
	if len(sp.Files) > maxFiles || len(sp.Instructions) > maxFileSize {
		return fmt.Errorf("the skill is too big: at most %d KB of instructions and %d extra files", maxFileSize/1024, maxFiles)
	}
	for name := range sp.Files {
		if _, err := cleanRel(name, skills.File); err != nil {
			return err
		}
	}
	if s, ok := b.Skills.Get(sp.Name); ok {
		switch {
		case s.Builtin:
			return fmt.Errorf("%q is a built-in skill; choose another name", sp.Name)
		case sp.Server != "" && s.Metadata[metaServer] == sp.Server:
			// the skill of this very server: it is replaced together with the server
		case !sp.Replace:
			return fmt.Errorf("a skill %q already exists; call again with replace=true to replace it", sp.Name)
		case sp.Server != "":
			return fmt.Errorf("a skill %q exists and belongs to something else; give this skill another name", sp.Name)
		}
	}
	if _, err := os.Lstat(filepath.Join(b.Skills.Dir(), sp.Name)); err == nil {
		if s, ok := b.Skills.Get(sp.Name); !ok || (!sp.Replace && !(sp.Server != "" && s.Metadata[metaServer] == sp.Server)) {
			return fmt.Errorf("a folder %q already exists in the skills folder", sp.Name)
		}
	}
	return nil
}

// writeSkill writes the skill into the skills folder and reloads the list.
func (b *Builder) writeSkill(sp skillSpec) (string, int, error) {
	if err := b.checkSkill(&sp); err != nil {
		return "", 0, err
	}
	meta := map[string]string{metaCreatedBy: createdBy}
	if sp.Server != "" {
		meta[metaServer] = sp.Server
	}
	files := map[string][]byte{skills.File: []byte(skillMarkdown(sp.Name, sp.Description, sp.Instructions, meta))}
	for name, text := range sp.Files {
		rel, err := cleanRel(name, skills.File)
		if err != nil {
			return "", 0, err
		}
		if len(text) > maxFileSize {
			return "", 0, fmt.Errorf("the file %q is larger than %d KB", name, maxFileSize/1024)
		}
		files[rel] = []byte(text)
	}
	skillsDir := b.Skills.Dir()
	dest := filepath.Join(skillsDir, sp.Name)
	tmp := filepath.Join(skillsDir, "."+sp.Name+".installing") // a leading dot: the scanner skips it
	_ = os.RemoveAll(tmp)
	if err := writeTree(tmp, files); err != nil {
		return "", 0, err
	}
	if _, _, err := skills.ParseAs(tmp, ""); err != nil {
		_ = os.RemoveAll(tmp)
		return "", 0, fmt.Errorf("the skill is not valid: %v", err)
	}
	if err := os.RemoveAll(dest); err != nil {
		_ = os.RemoveAll(tmp)
		return "", 0, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.RemoveAll(tmp)
		return "", 0, err
	}
	b.Skills.Reload()
	return dest, len(files), nil
}

// skillsOfServer lists the skills that were written together with an MCP server.
func (b *Builder) skillsOfServer(id string) []skills.Skill {
	var out []skills.Skill
	for _, s := range b.Skills.List() {
		if s.Metadata[metaCreatedBy] == createdBy && s.Metadata[metaServer] == id {
			out = append(out, s)
		}
	}
	return out
}
