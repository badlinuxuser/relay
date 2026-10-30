package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const pathMarker = "# added by relay install"

func installDir(flagDir string) (string, error) {
	if flagDir != "" {
		return filepath.Abs(flagDir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(base, "Programs", "relay"), nil
	}
	return filepath.Join(home, ".local", "bin"), nil
}

func binName() string {
	if runtime.GOOS == "windows" {
		return "relay.exe"
	}
	return "relay"
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func sameFile(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return os.SameFile(sa, sb)
}

func cmdInstall(args []string) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	dirFlag := fs.String("dir", "", "directory to install into")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	dir, err := installDir(*dirFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "relay:", err)
		return 1
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "relay:", err)
		return 1
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	target := filepath.Join(dir, binName())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "relay:", err)
		return 1
	}
	if sameFile(exe, target) {
		fmt.Println("Уже установлен:", target)
	} else {
		if err := copyFile(exe, target); err != nil {
			fmt.Fprintln(os.Stderr, "relay: не удалось скопировать:", err)
			return 1
		}
		fmt.Println("Скопировал:", target)
	}
	if err := addToPath(dir); err != nil {
		fmt.Fprintln(os.Stderr, "relay: PATH:", err)
		return 1
	}
	fmt.Println("Готово. Открой новый терминал и запусти: relay")
	return 0
}

func cmdUninstall(args []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	dirFlag := fs.String("dir", "", "directory it was installed into")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	dir, err := installDir(*dirFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "relay:", err)
		return 1
	}
	target := filepath.Join(dir, binName())
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "relay: не удалось удалить", target+":", err)
	} else {
		fmt.Println("Удалил:", target)
	}
	if err := removeFromPath(dir); err != nil {
		fmt.Fprintln(os.Stderr, "relay: PATH:", err)
		return 1
	}
	fmt.Println("Конфигурация и ключи не тронуты (каталог данных можно удалить вручную: relay config)")
	return 0
}

func addToPath(dir string) error {
	if runtime.GOOS == "windows" {
		return addToPathWindows(dir)
	}
	return addToPathUnix(dir)
}

func removeFromPath(dir string) error {
	if runtime.GOOS == "windows" {
		return removeFromPathWindows(dir)
	}
	return removeFromPathUnix()
}

// ---- Linux / macOS -----------------------------------------------------------

func rcFile() (path string, fish bool) {
	home, _ := os.UserHomeDir()
	switch filepath.Base(os.Getenv("SHELL")) {
	case "zsh":
		return filepath.Join(home, ".zshrc"), false
	case "fish":
		return filepath.Join(home, ".config", "fish", "conf.d", "relay.fish"), true
	case "bash":
		if _, err := os.Stat(filepath.Join(home, ".bashrc")); err == nil {
			return filepath.Join(home, ".bashrc"), false
		}
		return filepath.Join(home, ".bash_profile"), false
	}
	return filepath.Join(home, ".profile"), false
}

func addToPathUnix(dir string) error {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p == dir {
			fmt.Println("Каталог уже в PATH:", dir)
			return nil
		}
	}
	rc, fish := rcFile()
	if err := os.MkdirAll(filepath.Dir(rc), 0o755); err != nil {
		return err
	}
	b, _ := os.ReadFile(rc)
	if strings.Contains(string(b), pathMarker) {
		fmt.Println("PATH уже настроен в", rc)
		return nil
	}
	shown := dir
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(dir, home+string(filepath.Separator)) {
		shown = "$HOME" + strings.TrimPrefix(dir, home)
	}
	line := fmt.Sprintf("export PATH=\"%s:$PATH\"", shown)
	if fish {
		line = fmt.Sprintf("fish_add_path -g \"%s\"", shown)
	}
	f, err := os.OpenFile(rc, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "\n%s\n%s\n", pathMarker, line); err != nil {
		return err
	}
	fmt.Println("Добавил в PATH через", rc)
	return nil
}

func removeFromPathUnix() error {
	rc, _ := rcFile()
	b, err := os.ReadFile(rc)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(b), "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == pathMarker {
			i++ // следующая строка (export PATH=...) тоже наша
			continue
		}
		out = append(out, lines[i])
	}
	if err := os.WriteFile(rc, []byte(strings.Join(out, "\n")), 0o644); err != nil {
		return err
	}
	fmt.Println("Убрал запись из", rc)
	return nil
}

// ---- Windows -----------------------------------------------------------------

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func runPowerShell(script string) (string, error) {
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	msg := strings.TrimSpace(string(out))
	if err != nil {
		return msg, fmt.Errorf("powershell: %v: %s", err, msg)
	}
	return msg, nil
}

func addToPathWindows(dir string) error {
	script := fmt.Sprintf(`$d=%s; $p=[Environment]::GetEnvironmentVariable('Path','User'); if($null -eq $p){$p=''}; `+
		`$parts=@($p -split ';' | Where-Object { $_ -ne '' }); `+
		`if($parts -notcontains $d){ [Environment]::SetEnvironmentVariable('Path', (($parts + $d) -join ';'), 'User'); 'added' } else { 'exists' }`,
		psQuote(dir))
	msg, err := runPowerShell(script)
	if err != nil {
		return err
	}
	if msg == "exists" {
		fmt.Println("Каталог уже в PATH пользователя:", dir)
	} else {
		fmt.Println("Добавил в PATH пользователя:", dir)
	}
	return nil
}

func removeFromPathWindows(dir string) error {
	script := fmt.Sprintf(`$d=%s; $p=[Environment]::GetEnvironmentVariable('Path','User'); if($null -eq $p){$p=''}; `+
		`$parts=@($p -split ';' | Where-Object { $_ -ne '' -and $_ -ne $d }); `+
		`[Environment]::SetEnvironmentVariable('Path', ($parts -join ';'), 'User')`,
		psQuote(dir))
	if _, err := runPowerShell(script); err != nil {
		return err
	}
	fmt.Println("Убрал из PATH пользователя:", dir)
	return nil
}
