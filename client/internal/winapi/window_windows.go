//go:build windows

package winapi

import (
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32Window              = syscall.NewLazyDLL("user32.dll")
	procFindWindowW           = user32Window.NewProc("FindWindowW")
	procEnumWindows           = user32Window.NewProc("EnumWindows")
	procGetWindowTextW        = user32Window.NewProc("GetWindowTextW")
	procIsWindowVisible       = user32Window.NewProc("IsWindowVisible")
	procShowWindow            = user32Window.NewProc("ShowWindow")
	procSetForegroundWindow   = user32Window.NewProc("SetForegroundWindow")
	procFlashWindowEx         = user32Window.NewProc("FlashWindowEx")
	procMessageBeep           = user32Window.NewProc("MessageBeep")
	procMessageBoxW           = user32Window.NewProc("MessageBoxW")
)

const (
	swRestore = 9
)

type flashwinfo struct {
	cbSize    uint32
	hwnd      uintptr
	dwFlags   uint32
	uCount    uint32
	dwTimeout uint32
}

// ActivateAppWindow — đưa cửa sổ app lên trước + nhấp nháy taskbar
func ActivateAppWindow(titleHint string) {
	hwnd := findWindowByTitleContains(titleHint)
	if hwnd == 0 {
		return
	}
	_, _, _ = procShowWindow.Call(hwnd, swRestore)
	_, _, _ = procSetForegroundWindow.Call(hwnd)
	var fi flashwinfo
	fi.cbSize = uint32(unsafe.Sizeof(fi))
	fi.hwnd = hwnd
	fi.dwFlags = 0x0000000E // FLASHW_ALL | FLASHW_TIMERNOFG
	fi.uCount = 4
	fi.dwTimeout = 0
	_, _, _ = procFlashWindowEx.Call(uintptr(unsafe.Pointer(&fi)))
}

// PlayNotifySound — beep hệ thống Windows (không phụ thuộc autoplay trình duyệt)
func PlayNotifySound() {
	_, _, _ = procMessageBeep.Call(0x00000040) // MB_ICONASTERISK
	time.Sleep(120 * time.Millisecond)
	_, _, _ = procMessageBeep.Call(0xFFFFFFFF) // MB_OK
}

var enumTargetTitle string
var enumFoundHwnd uintptr

var enumWindowsCallback = syscall.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
	ret, _, _ := procIsWindowVisible.Call(hwnd)
	if ret == 0 {
		return 1
	}
	buf := make([]uint16, 256)
	_, _, _ = procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), 256)
	title := syscall.UTF16ToString(buf)
	if title != "" && strings.Contains(strings.ToLower(title), strings.ToLower(enumTargetTitle)) {
		enumFoundHwnd = hwnd
		return 0
	}
	return 1
})

func findWindowByTitleContains(hint string) uintptr {
	if hint == "" {
		return 0
	}
	titlePtr, _ := syscall.UTF16PtrFromString(hint)
	hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(titlePtr)))
	if hwnd != 0 {
		return hwnd
	}
	enumTargetTitle = hint
	enumFoundHwnd = 0
	_, _, _ = procEnumWindows.Call(enumWindowsCallback, 0)
	return enumFoundHwnd
}

// ShowWarningMessageBox hiển thị hộp thoại cảnh báo Win32 bất đồng bộ
func ShowWarningMessageBox(title, message string) {
	titlePtr, _ := syscall.UTF16PtrFromString(title)
	messagePtr, _ := syscall.UTF16PtrFromString(message)
	go func() {
		// MB_ICONWARNING = 0x00000030, MB_TOPMOST = 0x00040000
		_, _, _ = procMessageBoxW.Call(0, uintptr(unsafe.Pointer(messagePtr)), uintptr(unsafe.Pointer(titlePtr)), 0x00040030)
	}()
}
