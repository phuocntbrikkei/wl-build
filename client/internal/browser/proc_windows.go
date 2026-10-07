//go:build windows

package browser

import (
	"os/exec"
	"syscall"
)

// hideChildConsole: Client là ứng dụng GUI nên tiến trình con không cần console.
func hideChildConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
