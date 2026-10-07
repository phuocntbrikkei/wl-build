//go:build !windows

package browser

import "os/exec"

func hideChildConsole(cmd *exec.Cmd) {}
