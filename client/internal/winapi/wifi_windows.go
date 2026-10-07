//go:build windows

package winapi

import (
	"bytes"
	"os/exec"
	"strings"
	"syscall"
)

// GetWifiConnection đọc SSID và BSSID từ netsh (Windows)
func GetWifiConnection() WifiConnection {
	cmd := exec.Command("netsh", "wlan", "show", "interfaces")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return WifiConnection{}
	}

	var conn WifiConnection
	for _, line := range strings.Split(out.String(), "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "ssid") && !strings.Contains(lower, "bssid") {
			if v := valueAfterColon(trimmed); v != "" {
				conn.SSID = v
			}
		}
		// Windows: "AP BSSID" (EN) hoặc dòng có chứa "bssid" (locale khác)
		if strings.Contains(lower, "bssid") {
			if v := valueAfterColon(trimmed); v != "" {
				conn.BSSID = normalizeMAC(v)
			}
		}
	}
	return conn
}

// HasWifiPermission — trên Windows, khi Location Services TẮT thì netsh che SSID
// (rỗng). Có đọc được SSID nghĩa là đã đủ quyền đọc Wi-Fi.
func HasWifiPermission() bool {
	return strings.TrimSpace(GetWifiConnection().SSID) != ""
}

// OpenLocationSettings mở trang cài đặt Quyền riêng tư > Vị trí của Windows.
func OpenLocationSettings() {
	cmd := exec.Command("cmd", "/c", "start", "ms-settings:privacy-location")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Start()
}

// RequestLocationAccess requests Location permission (stub for Windows)
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
