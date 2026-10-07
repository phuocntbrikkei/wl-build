//go:build linux

package winapi

import "testing"

func TestParseNmcliWifi(t *testing.T) {
	out := `no:Hang xom:11\:22\:33\:44\:55\:66
yes:Rikkei-HN-T4:AA\:BB\:CC\:DD\:EE\:FF
`
	w := parseNmcliWifi(out)
	if w.SSID != "Rikkei-HN-T4" || BSSIDKey(w.BSSID) != "aabbccddeeff" {
		t.Fatalf("got %+v", w)
	}
	if (parseNmcliWifi(`no:A:11\:22\:33\:44\:55\:66`) != WifiConnection{}) {
		t.Fatal("no active wifi must be empty")
	}
	if w := parseNmcliWifi(`yes:co\:dau hai cham:AA\:BB\:CC\:DD\:EE\:FF`); w.SSID != "co:dau hai cham" {
		t.Fatalf("escaped colon: %+v", w)
	}
}
