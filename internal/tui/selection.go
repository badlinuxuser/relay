package tui

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// ---- clipboard ----------------------------------------------------------------

func clipboardCmd(text string) tea.Cmd {
	return func() tea.Msg {
		copyToClipboard(text)
		return nil
	}
}

// clipboardTools are the programs that can put text on the clipboard, best first, for this system.
func clipboardTools() [][]string {
	switch runtime.GOOS {
	case "darwin":
		return [][]string{{"pbcopy"}}
	case "windows":
		return [][]string{{"powershell", "-NoProfile", "-Command",
			"[Console]::InputEncoding=[Text.Encoding]::UTF8; Set-Clipboard -Value ([Console]::In.ReadToEnd())"}}
	}
	tools := [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard", "-in"}, {"xsel", "--clipboard", "--input"}}
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		tools[0], tools[1] = tools[1], tools[0]
	}
	return tools
}

// copyToClipboard tries a clipboard program first (it works in every terminal, including GNOME
// Console) and falls back to the OSC 52 escape sequence, which terminals such as Windows Terminal,
// iTerm2, kitty and WezTerm accept.
func copyToClipboard(text string) {
	for _, t := range clipboardTools() {
		path, err := exec.LookPath(t[0])
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		cmd := exec.CommandContext(ctx, path, t[1:]...)
		cmd.Stdin = strings.NewReader(text)
		err = cmd.Run()
		cancel()
		if err == nil {
			return
		}
	}
	fmt.Fprintf(os.Stdout, "\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(text)))
}
