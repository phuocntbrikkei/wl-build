//go:build windows

package netproxy

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const internetSettingsPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

var (
	wininet                = syscall.NewLazyDLL("wininet.dll")
	procInternetSetOptionW = wininet.NewProc("InternetSetOptionW")
)

const (
	internetOptionSettingsChanged = 39
	internetOptionRefresh         = 37
)

// notifySystem báo cho Windows/trình duyệt biết cấu hình proxy vừa đổi, áp dụng ngay không cần khởi động lại.
func notifySystem() {
	_, _, _ = procInternetSetOptionW.Call(0, internetOptionSettingsChanged, 0, 0)
	_, _, _ = procInternetSetOptionW.Call(0, internetOptionRefresh, 0, 0)
}

// savedProxyState — cấu hình proxy hệ thống TRƯỚC khi app bật, để khôi phục đúng lại (không phải
// cứ tắt trắng), và để nhận biết "phiên trước bật proxy nhưng chưa kịp tắt" khi app khởi động lại.
type savedProxyState struct {
	Enable int    `json:"enable"`
	Server string `json:"server"`
}

func stateFilePath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	dir := filepath.Join(configDir, "SimpleCare")
	_ = os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "proxy_state.json")
}

func openInternetSettingsKey() (registry.Key, error) {
	return registry.OpenKey(registry.CURRENT_USER, internetSettingsPath, registry.QUERY_VALUE|registry.SET_VALUE)
}

// CanSetSystemProxy kiểm tra có mở được khóa registry Internet Settings với
// quyền GHI không (đủ để bật/tắt proxy hệ thống). HKCU nên thường luôn OK.
func CanSetSystemProxy() bool {
	key, err := openInternetSettingsKey()
	if err != nil {
		return false
	}
	_ = key.Close()
	return true
}

// EnableSystemProxy trỏ proxy hệ thống (HKCU Internet Settings) về local capture proxy, lưu lại
// cấu hình cũ (chỉ lưu 1 lần — nếu gọi lại khi đã bật thì giữ nguyên state gốc đã lưu).
func EnableSystemProxy(addr string) error {
	key, err := openInternetSettingsKey()
	if err != nil {
		return err
	}
	defer key.Close()

	if _, err := os.Stat(stateFilePath()); os.IsNotExist(err) {
		prevEnable, _, _ := key.GetIntegerValue("ProxyEnable")
		prevServer, _, _ := key.GetStringValue("ProxyServer")
		state := savedProxyState{Enable: int(prevEnable), Server: prevServer}
		data, errMarshal := json.Marshal(state)
		if errMarshal == nil {
			_ = os.WriteFile(stateFilePath(), data, 0644)
		}
	}

	if err := key.SetDWordValue("ProxyEnable", 1); err != nil {
		return err
	}
	if err := key.SetStringValue("ProxyServer", addr); err != nil {
		return err
	}
	// Không proxy các địa chỉ nội bộ/loopback.
	_ = key.SetStringValue("ProxyOverride", "<local>")

	notifySystem()
	log.Printf("[NETPROXY] System proxy enabled -> %s", addr)
	return nil
}

// DisableSystemProxy khôi phục cấu hình proxy hệ thống về đúng trạng thái trước khi Enable.
// Nếu không tìm thấy state đã lưu (ví dụ app crash trước khi lưu kịp), tắt hẳn proxy cho an toàn.
func DisableSystemProxy() error {
	key, err := openInternetSettingsKey()
	if err != nil {
		return err
	}
	defer key.Close()

	statePath := stateFilePath()
	data, errRead := os.ReadFile(statePath)
	if errRead == nil {
		var state savedProxyState
		if json.Unmarshal(data, &state) == nil {
			_ = key.SetDWordValue("ProxyEnable", uint32(state.Enable))
			if state.Server != "" {
				_ = key.SetStringValue("ProxyServer", state.Server)
			} else {
				_ = key.DeleteValue("ProxyServer")
			}
		}
		_ = os.Remove(statePath)
	} else {
		_ = key.SetDWordValue("ProxyEnable", 0)
	}

	notifySystem()
	log.Println("[NETPROXY] System proxy disabled/restored")
	return nil
}

// RecoverStaleProxyIfAny — gọi lúc app khởi động. Nếu còn sót file state từ phiên trước (app bị
// Task Manager kill / crash trong lúc proxy đang bật), khôi phục lại proxy ngay để máy không bị
// kẹt mất mạng do trỏ vào proxy local đã không còn tiến trình nào phục vụ.
func RecoverStaleProxyIfAny() {
	if _, err := os.Stat(stateFilePath()); err == nil {
		log.Println("[NETPROXY] Found leftover proxy state from a previous session — restoring now")
		_ = DisableSystemProxy()
	}
}

// resetProxyScript — bản sao nội dung của client/scripts/reset-proxy.bat. Giữ 2 nơi (file nguồn
// để IT/sinh viên tham khảo & chạy tay, chuỗi này để tự ghi ra máy) vì go:embed không thể tham
// chiếu ra ngoài thư mục package.
const resetProxyScript = `@echo off
setlocal

rem reset-proxy.bat - Huy proxy he thong Windows do Rikkei Lms Connect bat len khi giam sat.
rem Dung script nay khi: app bi Task Manager kill / go / crash trong luc dang giam sat, khien
rem may khong vao duoc Internet (trinh duyet bao loi ket noi proxy).
rem An toan de chay nhieu lan, khong can quyen Administrator (chi sua HKCU cua user hien tai).

echo ============================================
echo   Huy proxy he thong (Rikkei Lms Connect)
echo ============================================

reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings" /v ProxyEnable /t REG_DWORD /d 0 /f >nul
reg delete "HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings" /v ProxyServer /f >nul 2>&1
reg delete "HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings" /v ProxyOverride /f >nul 2>&1

del /f /q "%LOCALAPPDATA%\SimpleCare\proxy_state.json" >nul 2>&1

echo Da huy proxy he thong thanh cong.
echo Vui long dong va mo lai trinh duyet de ap dung.
echo.
pause
`

// WriteRescueScript ghi sẵn script hủy proxy khẩn cấp ra máy (idempotent, gọi mỗi lần app khởi
// động) — để dù app có bị gỡ/không mở lại được, sinh viên/IT vẫn có sẵn cách tự cứu máy khỏi kẹt mạng.
func WriteRescueScript() {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	dir := filepath.Join(configDir, "SimpleCare")
	_ = os.MkdirAll(dir, 0755)
	path := filepath.Join(dir, "reset-proxy.bat")
	if err := os.WriteFile(path, []byte(resetProxyScript), 0644); err != nil {
		log.Printf("[NETPROXY] failed to write rescue script: %v", err)
	}
}
