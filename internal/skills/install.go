package skills

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Limits for a skill that is copied in: a skill is text and small scripts, not a download.
const (
	maxInstallFiles = 2000
	maxInstallBytes = 20 << 20
)

// Report tells what Install copied.
type Report struct {
	Files   []string // relative paths that were copied
	Scripts []string // the ones under scripts/ (never run by Relay by itself)
	Skipped []string // links and other things that were not copied
}

// Install copies the skill folder src into skillsDir/<name>. It checks SKILL.md first; it
// copies nothing but regular files (links are skipped, .git is left out); it never runs
// anything. An installed skill of the same name is replaced only when force is set.
func Install(src, skillsDir string, force bool) (*Skill, Report, error) {
	var rep Report
	src, err := filepath.Abs(src)
	if err != nil {
		return nil, rep, err
	}
	if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
		return nil, rep, fmt.Errorf("%s is not a folder", src)
	}
	sk, _, err := ParseAs(src, "")
	if err != nil {
		return nil, rep, err
	}
	dest := filepath.Join(skillsDir, sk.Name)
	if _, err := os.Lstat(dest); err == nil && !force {
		return nil, rep, fmt.Errorf("a skill named %q is already installed (%s); use --force to replace it", sk.Name, dest)
	}
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return nil, rep, err
	}
	tmp := dest + ".installing"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return nil, rep, err
	}
	var total int64
	walkErr := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil || rel == "." {
			return err
		}
		switch {
		case d.IsDir() && d.Name() == ".git":
			return filepath.SkipDir
		case d.IsDir():
			return os.MkdirAll(filepath.Join(tmp, rel), 0o755)
		case !d.Type().IsRegular():
			rep.Skipped = append(rep.Skipped, filepath.ToSlash(rel)+" (a link or special file)")
			return nil
		}
		if len(rep.Files) >= maxInstallFiles {
			return fmt.Errorf("more than %d files: this does not look like a skill", maxInstallFiles)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		if total > maxInstallBytes {
			return fmt.Errorf("more than %d MB: this does not look like a skill", maxInstallBytes>>20)
		}
		if err := copyFile(p, filepath.Join(tmp, rel), info.Mode()); err != nil {
			return err
		}
		slash := filepath.ToSlash(rel)
		rep.Files = append(rep.Files, slash)
		if strings.HasPrefix(slash, "scripts/") {
			rep.Scripts = append(rep.Scripts, slash)
		}
		return nil
	})
	if walkErr != nil {
		_ = os.RemoveAll(tmp)
		return nil, rep, walkErr
	}
	if force {
		if err := os.RemoveAll(dest); err != nil {
			_ = os.RemoveAll(tmp)
			return nil, rep, err
		}
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, rep, err
	}
	sort.Strings(rep.Files)
	sk.Dir = dest
	return sk, rep, nil
}

func copyFile(from, to string, mode fs.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	// keep the executable bit of a script, but nothing more than owner/group/other rwx
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm()|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Remove deletes an installed skill. A skill folder that is a link (a skill under development)
// is only unlinked: what it points to stays.
func Remove(skillsDir, name string) (string, error) {
	if !ValidName(name) {
		return "", fmt.Errorf("%q is not a skill name", name)
	}
	dest := filepath.Join(skillsDir, name)
	fi, err := os.Lstat(dest)
	if err != nil {
		if os.IsNotExist(err) {
			return dest, fmt.Errorf("there is no skill %q (see: relay skill list)", name)
		}
		return dest, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return dest, os.Remove(dest)
	}
	return dest, os.RemoveAll(dest)
}
