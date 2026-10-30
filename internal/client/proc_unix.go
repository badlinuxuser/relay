//go:build !windows

package client

import (
	"os/exec"
	"syscall"
)

// detach отвязывает фоновый процесс от терминала (своя сессия).
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
