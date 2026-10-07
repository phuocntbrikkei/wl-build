//go:build linux

package winapi

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// GetWifiConnection đọc WiFi đang kết nối qua NetworkManager (nmcli — có sẵn trên
// Ubuntu, Mint, Fedora, Debian desktop). Máy không có nmcli hoặc đang dùng dây mạng
// thì trả rỗng (giống Windows khi không có WiFi).
func GetWifiConnection() WifiConnection {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nmcli", "-t", "-f", "ACTIVE,SSID,BSSID", "device", "wifi", "list", "--rescan", "no").Output()
	if err != nil {
		return WifiConnection{}
	}
	return parseNmcliWifi(string(out))
}

// parseNmcliWifi: mỗi dòng "yes:Ten WiFi:AA\:BB\:CC\:DD\:EE\:FF" (chế độ -t thoát dấu ':'
// trong giá trị bằng '\'). Lấy dòng ACTIVE=yes.
func parseNmcliWifi(out string) WifiConnection {
	for _, line := range strings.Split(out, "\n") {
		f := splitNmcliTerse(strings.TrimRight(line, "\r"))
		if len(f) >= 3 && (f[0] == "yes" || f[0] == "có") {
			return WifiConnection{SSID: f[1], BSSID: f[2]}
		}
	}
	return WifiConnection{}
}

func splitNmcliTerse(line string) []string {
	var fields []string
	var cur strings.Builder
	esc := false
	for _, r := range line {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\':
			esc = true
		case r == ':':
			fields = append(fields, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	return append(fields, cur.String())
}
