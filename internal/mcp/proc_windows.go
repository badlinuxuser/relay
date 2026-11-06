//go:build windows

package mcp

import (
	"os/exec"
	"strconv"
	"syscall"
)

func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// killTree ends the process and everything it started (taskkill /T).
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := kill.Run(); err != nil {
		_ = cmd.Process.Kill()
	}
}
