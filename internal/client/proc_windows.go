//go:build windows

package client

import (
	"os/exec"
	"syscall"
)

// detach отвязывает фоновый процесс от консоли: CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000208, HideWindow: true}
}
