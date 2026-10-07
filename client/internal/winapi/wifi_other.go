//go:build !windows && !darwin

package winapi

// GetWifiConnection returns an empty WifiConnection stub for other systems
func GetWifiConnection() WifiConnection {
	return WifiConnection{}
}

// HasWifiPermission — linux/khác: không chặn (coi như đủ quyền).
func HasWifiPermission() bool { return true }

// OpenLocationSettings stub cho hệ khác.
func OpenLocationSettings() {}

// RequestLocationAccess requests Location permission (stub for other systems)
func RequestLocationAccess() {}

// RequestCameraAndMicAccess stub
func RequestCameraAndMicAccess() {}

// RequestScreenCaptureAccess stub
func RequestScreenCaptureAccess() {}

// GetCameraPermission stub
func GetCameraPermission() int { return -1 }

// GetMicrophonePermission stub
func GetMicrophonePermission() int { return -1 }

// GetScreenCapturePermission stub
func GetScreenCapturePermission() int { return -1 }
