//go:build !windows && !darwin && !linux

package blocker

func (b *Blocker) checkAndReport() {}

func (b *Blocker) SnapshotOpenApps() []WindowInfo { return nil }
