//go:build !windows

package netproxy

// Cơ chế bật proxy hệ thống hiện chỉ làm cho Windows (theo yêu cầu) — các nền tảng khác no-op.

func EnableSystemProxy(addr string) error { return nil }

func DisableSystemProxy() error { return nil }

func RecoverStaleProxyIfAny() {}

func WriteRescueScript() {}

// CanSetSystemProxy — nền khác không quản proxy hệ thống -> coi như OK.
func CanSetSystemProxy() bool { return true }
