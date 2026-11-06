// Package skills finds, checks and serves Agent Skills: folders with a SKILL.md that tell the
// agent how to do one kind of task. The format is the open Agent Skills one, so a skill written
// for another tool works here unchanged, and the other way round.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// File is the name of the file that makes a folder a skill.
const File = "SKILL.md"

// Skill is the head of a SKILL.md. The instructions (the body) are read only when needed.
type Skill struct {
	Name          string
	Description   string
	License       string
	Compatibility string
	Metadata      map[string]string
	AllowedTools  []string // read, but never used to skip a permission question
	Dir           string   // absolute path of the folder
	Enabled       bool
}

var reName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidName reports whether name is a valid skill name: 1-64 lowercase letters, digits and
// single hyphens, not starting or ending with a hyphen.
func ValidName(name string) bool { return len(name) <= 64 && reName.MatchString(name) }

// Parse reads SKILL.md of the skill in dir and checks it. It returns the skill and the
// instructions (the part after the YAML block).
func Parse(dir string) (*Skill, string, error) { return ParseAs(dir, filepath.Base(dir)) }

// ParseAs is Parse with the folder name that the skill name must match ("" skips that check:
// a folder that is about to be installed under its skill name).
func ParseAs(dir, folder string) (*Skill, string, error) {
	path := filepath.Join(dir, File)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", fmt.Errorf("no %s in the folder", File)
		}
		return nil, "", err
	}
	if len(data) > 1<<20 {
		return nil, "", fmt.Errorf("%s is larger than 1 MB", File)
	}
	if !utf8.Valid(data) {
		return nil, "", fmt.Errorf("%s is not valid UTF-8 text", File)
	}
	yaml, body, ok := splitFrontmatter(string(data))
	if !ok {
		return nil, "", fmt.Errorf("%s must start with a block between two \"---\" lines (with name and description)", File)
	}
	fields, err := parseYAML(yaml)
	if err != nil {
		return nil, "", fmt.Errorf("the header of %s: %w", File, err)
	}
	s := &Skill{Dir: dir, Enabled: true}
	s.Name = text(fields["name"])
	s.Description = strings.Join(strings.Fields(text(fields["description"])), " ")
	s.License = text(fields["license"])
	s.Compatibility = text(fields["compatibility"])
	if m, ok := fields["metadata"].(map[string]string); ok {
		s.Metadata = m
	}
	switch v := fields["allowed-tools"].(type) {
	case string:
		s.AllowedTools = strings.Fields(v)
	case []string:
		s.AllowedTools = v
	}
	if err := s.validate(folder); err != nil {
		return nil, "", err
	}
	return s, strings.TrimLeft(body, "\n"), nil
}

func text(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func (s *Skill) validate(folder string) error {
	switch {
	case s.Name == "":
		return fmt.Errorf("\"name\" is missing")
	case !ValidName(s.Name):
		return fmt.Errorf("name %q is not valid: use up to 64 lowercase letters, digits and single hyphens (like code-review)", s.Name)
	case folder != "" && s.Name != folder:
		return fmt.Errorf("name %q must be the same as the folder name %q", s.Name, folder)
	case s.Description == "":
		return fmt.Errorf("\"description\" is missing: it tells the agent when to use the skill")
	case utf8.RuneCountInString(s.Description) > 1024:
		return fmt.Errorf("\"description\" is longer than 1024 characters")
	case utf8.RuneCountInString(s.Compatibility) > 500:
		return fmt.Errorf("\"compatibility\" is longer than 500 characters")
	}
	return nil
}

// Validate checks the skill in dir; the folder name must match the skill name.
func Validate(dir string) error {
	_, _, err := Parse(dir)
	return err
}
