package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"relay/internal/config"
	"relay/internal/skills"
)

const skillUsage = `relay skill: skills (instructions that teach the agent a task)

Usage:
  relay skill list                      installed skills
  relay skill add <folder> [--force]    copy a skill folder (with a SKILL.md) into Relay
  relay skill remove <name> [--yes]
  relay skill info <name>
  relay skill validate [name | folder]  check one skill, or all installed skills
  relay skill enable <name> | disable <name>

A skill is a folder with a SKILL.md (the open Agent Skills format). You can also just copy
the folder into the skills folder; Relay notices it without a restart.
`

func cmdSkill(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(skillUsage)
		return 0
	}
	home, err := config.Home()
	if err != nil {
		return fail(err)
	}
	dir := config.SkillsDir(home)
	_ = os.MkdirAll(dir, 0o755)
	m := skills.NewManager(dir, config.SkillsState(home))
	m.Reload()
	switch args[0] {
	case "list", "ls":
		return skillList(m)
	case "add":
		return skillAdd(dir, args[1:])
	case "remove", "rm":
		return skillRemove(dir, args[1:])
	case "info":
		return skillInfo(m, args[1:])
	case "validate":
		return skillValidate(m, dir, args[1:])
	case "enable", "disable":
		return skillEnable(m, args[0] == "enable", args[1:])
	}
	fmt.Fprintf(os.Stderr, "relay skill: unknown command %q\n\n%s", args[0], skillUsage)
	return 2
}

func skillList(m *skills.Manager) int {
	list := m.List()
	if len(list) == 0 && len(m.Problems()) == 0 {
		fmt.Printf("No skills yet.\nAdd one:  relay skill add <folder>\nOr copy a skill folder into: %s\n", m.Dir())
		return 0
	}
	if len(list) > 0 {
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tSTATE\tDESCRIPTION")
		for _, s := range list {
			state := "on"
			if !s.Enabled {
				state = "off"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Name, state, oneLine(s.Description, 80))
		}
		tw.Flush()
	}
	for _, p := range m.Problems() {
		fmt.Printf("! %s: %s\n", p.Dir, p.Error)
	}
	return 0
}

func skillAdd(dir string, args []string) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprint(os.Stderr, "usage: relay skill add <folder> [--force]\n")
		return 2
	}
	src, rest := args[0], args[1:]
	fs := flag.NewFlagSet("relay skill add", flag.ContinueOnError)
	force := fs.Bool("force", false, "replace an installed skill of the same name")
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if strings.HasPrefix(src, "github:") || strings.Contains(src, "://") {
		return fail(fmt.Errorf("installing from Git is not available yet: download the folder and give its path"))
	}
	sk, rep, err := skills.Install(src, dir, *force)
	if err != nil {
		return fail(err)
	}
	fmt.Printf("Installed %q (%d files) in %s\n", sk.Name, len(rep.Files), sk.Dir)
	fmt.Println("  " + oneLine(sk.Description, 100))
	if len(rep.Scripts) > 0 {
		fmt.Printf("It contains scripts (%s). Relay never runs them by itself; the agent can run one only after you approve the command.\n", strings.Join(rep.Scripts, ", "))
	}
	for _, s := range rep.Skipped {
		fmt.Println("  not copied:", s)
	}
	return 0
}

func skillRemove(dir string, args []string) int {
	fs := flag.NewFlagSet("relay skill remove", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "do not ask")
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, "usage: relay skill remove <name> [--yes]\n")
		return 2
	}
	name := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if !skills.ValidName(name) {
		return fail(fmt.Errorf("%q is not a skill name (see: relay skill list)", name))
	}
	path := filepath.Join(dir, name)
	if _, err := os.Lstat(path); err != nil {
		return fail(fmt.Errorf("there is no skill %q (see: relay skill list)", name))
	}
	if !*yes && !confirm(fmt.Sprintf("Delete %s and everything in it? [y/N] ", path)) {
		fmt.Println("Nothing was deleted.")
		return 1
	}
	if _, err := skills.Remove(dir, name); err != nil {
		return fail(err)
	}
	fmt.Printf("Removed %q.\n", name)
	return 0
}

func skillInfo(m *skills.Manager, args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, "usage: relay skill info <name>\n")
		return 2
	}
	s, ok := m.Get(args[0])
	if !ok {
		return fail(fmt.Errorf("there is no skill %q (see: relay skill list)", args[0]))
	}
	state := "on"
	if !s.Enabled {
		state = "off (relay skill enable " + s.Name + ")"
	}
	fmt.Printf("Name:         %s\nState:        %s\nPath:         %s\nDescription:  %s\n", s.Name, state, s.Dir, s.Description)
	if s.License != "" {
		fmt.Println("License:     ", s.License)
	}
	if s.Compatibility != "" {
		fmt.Println("Compatibility:", s.Compatibility)
	}
	for k, v := range s.Metadata {
		fmt.Printf("  %s: %s\n", k, v)
	}
	if len(s.AllowedTools) > 0 {
		fmt.Println("Allowed tools:", strings.Join(s.AllowedTools, " "), "(informational: Relay still asks before it acts)")
	}
	if _, body, err := skills.Parse(s.Dir); err == nil {
		fmt.Printf("Instructions: %d lines\n", strings.Count(body, "\n")+1)
	}
	if files := m.Files(s.Name, 30); len(files) > 0 {
		fmt.Println("Files:")
		for _, f := range files {
			fmt.Println("  " + f)
		}
	}
	return 0
}

func skillValidate(m *skills.Manager, dir string, args []string) int {
	bad := 0
	check := func(label, folder, as string) {
		if _, _, err := skills.ParseAs(folder, as); err != nil {
			fmt.Printf("✗ %s: %v\n", label, err)
			bad++
			return
		}
		fmt.Printf("✓ %s\n", label)
	}
	if len(args) == 0 {
		entries, _ := os.ReadDir(dir)
		n := 0
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			if strings.HasPrefix(e.Name(), ".") || !isDir(p) {
				continue
			}
			n++
			check(e.Name(), p, e.Name())
		}
		if n == 0 {
			fmt.Println("No skills installed.")
		}
	} else {
		arg := args[0]
		if isDir(arg) {
			check(arg, arg, "") // a folder that is not installed yet: its name need not match
		} else if skills.ValidName(arg) && isDir(filepath.Join(dir, arg)) {
			check(arg, filepath.Join(dir, arg), arg)
		} else {
			return fail(fmt.Errorf("%q is neither a folder nor an installed skill", arg))
		}
	}
	if bad > 0 {
		return 1
	}
	return 0
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func skillEnable(m *skills.Manager, on bool, args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, "usage: relay skill enable|disable <name>\n")
		return 2
	}
	if err := m.SetEnabled(args[0], on); err != nil {
		return fail(err)
	}
	if on {
		fmt.Printf("%q is on.\n", args[0])
	} else {
		fmt.Printf("%q is off: the agent will not see it.\n", args[0])
	}
	return 0
}
