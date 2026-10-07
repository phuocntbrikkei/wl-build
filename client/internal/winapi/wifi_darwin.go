//go:build darwin

package winapi

/*
#cgo LDFLAGS: -framework CoreWLAN -framework CoreLocation -framework Foundation -framework AVFoundation -framework CoreGraphics
#include <stdlib.h>

typedef struct {
    char* ssid;
    char* bssid;
} CWifiInfo;

void RequestLocationPermission();
void RequestCameraAndMicPermission();
void RequestScreenCapturePermission();
CWifiInfo GetCurrentWifiInfo();
int GetCameraPermissionStatus();
int GetMicrophonePermissionStatus();
int GetScreenCapturePermissionStatus();
*/
import "C"
import (
	"os/exec"
	"strings"
	"unsafe"
)

// HasWifiPermission — macOS che SSID ("redacted"/rỗng) khi chưa cấp quyền Vị trí.
func HasWifiPermission() bool {
	ssid := strings.TrimSpace(GetWifiConnection().SSID)
	return ssid != "" && !strings.Contains(strings.ToLower(ssid), "redacted")
}

// OpenLocationSettings mở pane Location Services trong System Settings (macOS).
func OpenLocationSettings() {
	_ = exec.Command("open", "x-apple.systempreferences:com.apple.preference.security?Privacy_LocationServices").Start()
}

// RequestLocationAccess requests Location permission on macOS
func RequestLocationAccess() {
	C.RequestLocationPermission()
}

// RequestCameraAndMicAccess requests Camera and Microphone permissions on macOS
func RequestCameraAndMicAccess() {
	C.RequestCameraAndMicPermission()
}

// RequestScreenCaptureAccess requests Screen Capture permission on macOS
func RequestScreenCaptureAccess() {
	C.RequestScreenCapturePermission()
}

// GetCameraPermission status on macOS
func GetCameraPermission() int {
	return int(C.GetCameraPermissionStatus())
}

// GetMicrophonePermission status on macOS
func GetMicrophonePermission() int {
	return int(C.GetMicrophonePermissionStatus())
}

// GetScreenCapturePermission status on macOS
func GetScreenCapturePermission() int {
	return int(C.GetScreenCapturePermissionStatus())
}

// GetWifiConnection đọc SSID và BSSID từ CoreWLAN (macOS)
func GetWifiConnection() WifiConnection {
	info := C.GetCurrentWifiInfo()
	defer func() {
		if info.ssid != nil {
			C.free(unsafe.Pointer(info.ssid))
		}
		if info.bssid != nil {
			C.free(unsafe.Pointer(info.bssid))
		}
	}()

	ssid := ""
	if info.ssid != nil {
		ssid = C.GoString(info.ssid)
	}

	bssid := ""
	if info.bssid != nil {
		bssid = C.GoString(info.bssid)
	}

	return WifiConnection{
		SSID:  ssid,
		BSSID: normalizeMAC(bssid),
	}
}
