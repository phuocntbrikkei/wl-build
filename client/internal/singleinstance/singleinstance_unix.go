//go:build unix

package singleinstance

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"client/internal/winapi"

	"golang.org/x/sys/unix"
)

// Giữ file lock mở suốt đời process.
var lockFile *os.File

// Acquire — flock non-blocking trên file trong config dir.
func Acquire() error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	dir := filepath.Join(configDir, "SimpleCare")
	_ = os.MkdirAll(dir, 0755)
	path := filepath.Join(dir, "instance.lock")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		winapi.ActivateAppWindow(windowTitle)
		winapi.ShowWarningMessageBox(
			"Rikkei Lms Connect",
			"Ứng dụng Rikkei Lms Connect đang chạy.\n\nChỉ được mở một cửa sổ.",
		)
		// Cho dialog async kịp hiện trước khi process thoát.
		time.Sleep(1500 * time.Millisecond)
		fmt.Fprintln(os.Stderr, "Rikkei Lms Connect is already running")
		return ErrAlreadyRunning
	}
	_, _ = f.WriteString(fmt.Sprintf("%d\n", os.Getpid()))
	_ = f.Sync()
	lockFile = f
	return nil
}
