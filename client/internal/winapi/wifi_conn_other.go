//go:build !windows && !darwin && !linux

package winapi

// GetWifiConnection returns an empty WifiConnection stub for other systems
func GetWifiConnection() WifiConnection {
	return WifiConnection{}
}
