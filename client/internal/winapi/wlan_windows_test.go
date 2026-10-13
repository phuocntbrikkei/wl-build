//go:build windows

package winapi

import "testing"

func TestWifiPermissionOK(t *testing.T) {
	cases := []struct {
		name   string
		state  wifiState
		ssid   string
		locOff bool
		want   bool
	}{
		{"netsh đọc được tên", wifiHidden, "RikkeiEdu", false, true},
		{"không có card Wi-Fi", wifiNoAdapter, "", false, true},
		{"có card, không nối Wi-Fi (cắm dây)", wifiDisconnected, "", false, true},
		{"API đọc được tên", wifiReadable, "", false, true},
		{"đang nối, bị che, bật được Vị trí -> chặn", wifiHidden, "", false, false},
		{"đang nối, bị che, máy không bật được Vị trí -> bỏ qua", wifiHidden, "", true, true},
	}
	for _, c := range cases {
		if got := wifiPermissionOK(c.state, c.ssid, c.locOff); got != c.want {
			t.Errorf("%s: được %v, cần %v", c.name, got, c.want)
		}
	}
}

// Gọi API thật trên máy dev: không panic, trả 1 trạng thái hợp lệ.
func TestQueryWifiStateSmoke(t *testing.T) {
	st := queryWifiState()
	t.Logf("wifi state = %d, location unavailable = %v, HasWifiPermission = %v", st, locationUnavailable(), HasWifiPermission())
}
