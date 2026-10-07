//go:build !windows && !darwin && !linux

package guard

import "time"

func Start(onViolation func(kind, reason string)) {}
func Stop() {}
func SuppressFor(_ time.Duration) {}
