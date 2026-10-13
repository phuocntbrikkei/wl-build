//go:build windows

package winapi

import (
	"encoding/binary"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

// Đọc trạng thái card Wi-Fi qua Native WLAN API (wlanapi.dll) — không phụ thuộc ngôn ngữ
// Windows như netsh. Dùng để biết máy CÓ card Wi-Fi / đang nối Wi-Fi hay không, và Windows
// có đang che tên Wi-Fi vì chưa bật Vị trí không.

var (
	modWlan                = syscall.NewLazyDLL("wlanapi.dll")
	procWlanOpenHandle     = modWlan.NewProc("WlanOpenHandle")
	procWlanCloseHandle    = modWlan.NewProc("WlanCloseHandle")
	procWlanEnumInterfaces = modWlan.NewProc("WlanEnumInterfaces")
	procWlanQueryInterface = modWlan.NewProc("WlanQueryInterface")
	procWlanFreeMemory     = modWlan.NewProc("WlanFreeMemory")
)

const (
	wlanClientVersion          = 2
	wlanStateConnected         = 1 // wlan_interface_state_connected
	wlanOpcodeCurrentConnected = 7 // wlan_intf_opcode_current_connection
	wlanIfaceInfoSize          = 16 + 512 + 4
	wlanListHeaderSize         = 8
	wlanConnSSIDLenOffset      = 4 + 4 + 512 // isState + wlanConnectionMode + strProfileName
)

// wifiState: tình trạng Wi-Fi của máy.
type wifiState int

const (
	wifiNoAdapter    wifiState = iota // không có card Wi-Fi / dịch vụ WLAN không chạy / không có wlanapi
	wifiDisconnected                  // có card nhưng không nối Wi-Fi (vd cắm dây mạng)
	wifiReadable                      // đang nối Wi-Fi, đọc được tên mạng
	wifiHidden                        // đang nối Wi-Fi nhưng Windows che tên mạng (chưa bật Vị trí)
)

// queryWifiState: tình trạng Wi-Fi qua WLAN API.
func queryWifiState() wifiState {
	if modWlan.Load() != nil {
		return wifiNoAdapter
	}
	var negotiated uint32
	var h syscall.Handle
	if r, _, _ := procWlanOpenHandle.Call(wlanClientVersion, 0, uintptr(unsafe.Pointer(&negotiated)), uintptr(unsafe.Pointer(&h))); r != 0 {
		return wifiNoAdapter // vd ERROR_SERVICE_NOT_ACTIVE: dịch vụ WLAN AutoConfig không chạy
	}
	defer procWlanCloseHandle.Call(uintptr(h), 0)

	var list unsafe.Pointer
	if r, _, _ := procWlanEnumInterfaces.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&list))); r != 0 || list == nil {
		return wifiNoAdapter
	}
	defer procWlanFreeMemory.Call(uintptr(list))
	n := *(*uint32)(list)
	if n == 0 {
		return wifiNoAdapter
	}
	state := wifiDisconnected
	for i := uint32(0); i < n; i++ {
		item := unsafe.Add(list, wlanListHeaderSize+int(i)*wlanIfaceInfoSize)
		isState := *(*uint32)(unsafe.Add(item, 16+512))
		if isState != wlanStateConnected {
			continue
		}
		var size uint32
		var data unsafe.Pointer
		var vt uint32
		r, _, _ := procWlanQueryInterface.Call(uintptr(h), uintptr(item), wlanOpcodeCurrentConnected, 0,
			uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&vt)))
		if r != 0 || data == nil {
			state = wifiHidden // thường là ERROR_ACCESS_DENIED: chưa cấp quyền Vị trí
			continue
		}
		buf := unsafe.Slice((*byte)(data), int(size))
		ssidLen := uint32(0)
		if int(size) >= wlanConnSSIDLenOffset+4 {
			ssidLen = binary.LittleEndian.Uint32(buf[wlanConnSSIDLenOffset:])
		}
		procWlanFreeMemory.Call(uintptr(data))
		if ssidLen > 0 {
			return wifiReadable
		}
		state = wifiHidden
	}
	return state
}

// locationUnavailable: máy KHÔNG bật được Vị trí — bị chính sách tắt (DisableLocation) hoặc không
// có / đã vô hiệu dịch vụ định vị (lfsvc). Khi đó có yêu cầu SV bật Vị trí cũng vô ích.
func locationUnavailable() bool {
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Policies\Microsoft\Windows\LocationAndSensors`, registry.QUERY_VALUE); err == nil {
		v, _, e := k.GetIntegerValue("DisableLocation")
		k.Close()
		if e == nil && v == 1 {
			return true
		}
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\lfsvc`, registry.QUERY_VALUE)
	if err != nil {
		return true // không có dịch vụ định vị
	}
	defer k.Close()
	start, _, err := k.GetIntegerValue("Start")
	return err == nil && start == 4 // SERVICE_DISABLED
}

// wifiPermissionOK: quyết định cổng "Quyền Wi-Fi (Vị trí)" (hàm thuần, để test được).
// Chỉ chặn khi máy ĐANG nối Wi-Fi, Windows che tên mạng, và máy bật được Vị trí. Máy không có
// card Wi-Fi / không nối Wi-Fi / không bật được Vị trí -> bỏ qua kiểm tra.
func wifiPermissionOK(state wifiState, netshSSID string, locUnavailable bool) bool {
	if netshSSID != "" {
		return true
	}
	switch state {
	case wifiNoAdapter, wifiDisconnected, wifiReadable:
		return true
	}
	return locUnavailable
}
