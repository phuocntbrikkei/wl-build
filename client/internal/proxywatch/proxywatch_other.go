//go:build !windows

package proxywatch

// Nền không phải Windows không quản proxy hệ thống -> watchdog là no-op.
func ParentPID() (int, bool) { return 0, false }
func Spawn()                 {}
func Run(_ int)              {}
