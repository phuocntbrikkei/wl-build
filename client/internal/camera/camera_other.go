//go:build !darwin

package camera

import "log"

const IsNative = false

// StartCapture is a no-op on non-darwin platforms (Windows uses different webcam API)
func StartCapture() error {
	log.Println("[CAMERA] Native camera capture not supported on this platform")
	return nil
}

// StopCapture is a no-op on non-darwin platforms
func StopCapture() {
}

// GetFrame always returns empty on non-darwin platforms
func GetFrame() string {
	return ""
}
