package winapi

import (
	"strings"
)

// WifiConnection — SSID + BSSID (MAC) của điểm phát đang kết nối
type WifiConnection struct {
	SSID  string
	BSSID string
}

// GetWifiSSID trả về SSID Wifi đang kết nối
func GetWifiSSID() string {
	return GetWifiConnection().SSID
}

func valueAfterColon(line string) string {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(line[idx+1:])
}

func normalizeMAC(mac string) string {
	var hex []rune
	for _, c := range strings.ToLower(mac) {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			hex = append(hex, c)
		}
	}
	if len(hex) != 12 {
		return strings.TrimSpace(mac)
	}
	return string(hex[0:2]) + ":" + string(hex[2:4]) + ":" + string(hex[4:6]) + ":" +
		string(hex[6:8]) + ":" + string(hex[8:10]) + ":" + string(hex[10:12])
}

// BSSIDKey chuẩn hóa MAC để so khớp (12 ký tự hex)
func BSSIDKey(bssid string) string {
	var hex []rune
	for _, c := range strings.ToLower(bssid) {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			hex = append(hex, c)
		}
	}
	return string(hex)
}
