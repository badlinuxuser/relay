//go:build !windows

package mcp

import (
	"os/exec"
	"syscall"
)

// A server often starts helpers (npx starts node). Giving it its own process group lets
// Close end the whole tree instead of leaving the helpers running.
func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
