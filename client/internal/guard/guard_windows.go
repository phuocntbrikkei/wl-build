//go:build windows

package guard

import (
	"log"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	smCMonitors            = 80
	eventSystemDesktopSwitch = 0x0020
	wineventOutofcontext   = 0x0000
	wmWtsSessionChange     = 0x02B1

	wtsConsoleDisconnect = 0x2
	wtsRemoteDisconnect  = 0x3
	wtsSessionLogoff     = 0x6
	wtsSessionLock       = 0x7

	whKeyboardLl  = 13
	wmKeyDown     = 0x0100
	wmSysKeyDown  = 0x0104

	vkLWin    = 0x5B
	vkRWin    = 0x5C
	vkControl = 0x11
	vkTab     = 0x09
	vkL       = 0x4C
	vkD       = 0x44
	vkLeft    = 0x25
	vkRight   = 0x27
	vkF4      = 0x73
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	wtsapi32 = syscall.NewLazyDLL("wtsapi32.dll")

	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	procSetWinEventHook               = user32.NewProc("SetWinEventHook")
	procUnhookWinEvent                = user32.NewProc("UnhookWinEvent")
	procGetMessageW                   = user32.NewProc("GetMessageW")
	procTranslateMessage              = user32.NewProc("TranslateMessage")
	procDispatchMessageW              = user32.NewProc("DispatchMessageW")
	procCreateWindowExW               = user32.NewProc("CreateWindowExW")
	procDefWindowProcW                = user32.NewProc("DefWindowProcW")
	procRegisterClassExW              = user32.NewProc("RegisterClassExW")
	procDestroyWindow                 = user32.NewProc("DestroyWindow")
	procGetCurrentProcessId           = kernel32.NewProc("GetCurrentProcessId")
	procProcessIdToSessionId          = kernel32.NewProc("ProcessIdToSessionId")
	procWTSRegisterSessionNotification   = wtsapi32.NewProc("WTSRegisterSessionNotification")
	procWTSUnRegisterSessionNotification = wtsapi32.NewProc("WTSUnRegisterSessionNotification")

	procSetWindowsHookExW    = user32.NewProc("SetWindowsHookExW")
	procCallNextHookEx       = user32.NewProc("CallNextHookEx")
	procUnhookWindowsHookEx  = user32.NewProc("UnhookWindowsHookEx")
	procGetAsyncKeyState     = user32.NewProc("GetAsyncKeyState")
	procGetModuleHandleW     = kernel32.NewProc("GetModuleHandleW")
)

// kbdllhookstruct — tham số của hook WH_KEYBOARD_LL (xem MSDN KBDLLHOOKSTRUCT).
type kbdllhookstruct struct {
	VkCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type wndclassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   syscall.Handle
	Icon       syscall.Handle
	Cursor     syscall.Handle
	Background syscall.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     syscall.Handle
}

type point struct {
	X, Y int32
}

type msg struct {
	Hwnd    syscall.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

var (
	guardOnce               sync.Once
	guardViolation          func(kind, reason string)
	guardStop               chan struct{}
	guardBaselineUser       string
	guardBaselineSession    uint32
	desktopHook             uintptr
	keyboardHook            uintptr
	guardClassAtom          uint16
	suppressMu              sync.Mutex
	suppressViolationsUntil time.Time
)

// SuppressFor tạm không thoát app khi WebView/Explorer chuyển màn hình nội bộ.
func SuppressFor(d time.Duration) {
	if d <= 0 {
		return
	}
	suppressMu.Lock()
	next := time.Now().Add(d)
	if next.After(suppressViolationsUntil) {
		suppressViolationsUntil = next
	}
	suppressMu.Unlock()
}

func violationsSuppressed() bool {
	suppressMu.Lock()
	defer suppressMu.Unlock()
	return time.Now().Before(suppressViolationsUntil)
}

// Start giám sát môi trường Windows — vi phạm thì gọi onViolation(kind, reason).
func Start(onViolation func(kind, reason string)) {
	guardOnce.Do(func() {
		if onViolation == nil {
			return
		}
		guardViolation = onViolation
		guardStop = make(chan struct{})
		guardBaselineUser = currentUsername()
		guardBaselineSession = currentSessionID()

		if monitorCount() > 1 {
			onViolation("multi_monitor", "Phát hiện nhiều hơn 1 màn hình. Vui lòng chỉ dùng một màn hình khi chạy Rikkei Lms Connect.")
			return
		}

		go pollLoop()
		go runMessageWindow()
	})
}

func Stop() {
	if guardStop != nil {
		close(guardStop)
	}
}

func pollLoop() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-guardStop:
			return
		case <-ticker.C:
			checkEnvironment()
		}
	}
}

func checkEnvironment() {
	if guardViolation == nil || violationsSuppressed() {
		return
	}
	if n := monitorCount(); n > 1 {
		guardViolation("multi_monitor", "Phát hiện nhiều hơn 1 màn hình. Vui lòng rút/bật tắt màn hình phụ.")
		return
	}
	user := currentUsername()
	if user != "" && guardBaselineUser != "" && user != guardBaselineUser {
		guardViolation("user_switch", "Phát hiện đổi tài khoản Windows. Ứng dụng sẽ thoát.")
		return
	}
	sid := currentSessionID()
	if sid != 0 && guardBaselineSession != 0 && sid != guardBaselineSession {
		guardViolation("session_change", "Phiên đăng nhập Windows đã thay đổi. Ứng dụng sẽ thoát.")
	}
}

func triggerViolation(kind, reason string) {
	if violationsSuppressed() {
		log.Printf("[GUARD] suppressed: %s", reason)
		return
	}
	if guardViolation != nil {
		guardViolation(kind, reason)
	}
}

func monitorCount() int {
	n, _, _ := procGetSystemMetrics.Call(smCMonitors)
	return int(n)
}

func currentSessionID() uint32 {
	pid, _, _ := procGetCurrentProcessId.Call()
	var sid uint32
	procProcessIdToSessionId.Call(pid, uintptr(unsafe.Pointer(&sid)))
	return sid
}

func currentUsername() string {
	if u, ok := syscall.Getenv("USERNAME"); ok {
		return u
	}
	return ""
}

func runMessageWindow() {
	className, _ := syscall.UTF16PtrFromString("SimpleCareGuardWnd")
	hInstance := syscall.Handle(0)

	wndProc := syscall.NewCallback(guardWndProc)
	wc := wndclassEx{
		Size:      uint32(unsafe.Sizeof(wndclassEx{})),
		WndProc:   wndProc,
		Instance:  hInstance,
		ClassName: className,
	}
	atom, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		log.Println("[GUARD] RegisterClassEx failed")
		return
	}
	guardClassAtom = uint16(atom)

	title, _ := syscall.UTF16PtrFromString("SimpleCareGuard")
	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		0,
		0, 0, 0, 0,
		0,
		0, uintptr(hInstance), 0,
	)
	if hwnd == 0 {
		log.Println("[GUARD] CreateWindowEx failed")
		return
	}
	defer procDestroyWindow.Call(hwnd)

	procWTSRegisterSessionNotification.Call(hwnd, 0)

	desktopHook, _, _ = procSetWinEventHook.Call(
		eventSystemDesktopSwitch,
		eventSystemDesktopSwitch,
		0,
		syscall.NewCallback(desktopSwitchCallback),
		0, 0,
		wineventOutofcontext,
	)

	hMod, _, _ := procGetModuleHandleW.Call(0)
	keyboardHook, _, _ = procSetWindowsHookExW.Call(
		whKeyboardLl,
		syscall.NewCallback(lowLevelKeyboardProc),
		hMod,
		0,
	)
	if keyboardHook == 0 {
		log.Println("[GUARD] SetWindowsHookEx(WH_KEYBOARD_LL) failed — Win+Tab/Win+L sẽ không bị chặn ở tầng phím.")
	}

	defer func() {
		if desktopHook != 0 {
			procUnhookWinEvent.Call(desktopHook)
		}
		if keyboardHook != 0 {
			procUnhookWindowsHookEx.Call(keyboardHook)
			keyboardHook = 0
		}
		procWTSUnRegisterSessionNotification.Call(hwnd)
	}()

	var m msg
	for {
		select {
		case <-guardStop:
			return
		default:
		}
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if ret == 0 || ret == ^uintptr(0) {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func guardWndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch uint32(msg) {
	case wmWtsSessionChange:
		switch uint32(wParam) {
		case wtsSessionLock, wtsSessionLogoff, wtsConsoleDisconnect, wtsRemoteDisconnect:
			triggerViolation("session_change", "Phiên Windows bị khóa, đăng xuất hoặc chuyển người dùng. Ứng dụng sẽ thoát.")
		}
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return r
}

func desktopSwitchCallback(hWinEventHook, event, hwnd, idObject, idChild, idEventThread, dwmsEventTime uintptr) uintptr {
	if event == eventSystemDesktopSwitch {
		triggerViolation("virtual_desktop", "Không được chuyển Desktop ảo (Win+Tab). Ứng dụng sẽ thoát.")
	}
	return 0
}

func isKeyDown(vk int) bool {
	state, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return state&0x8000 != 0
}

// lowLevelKeyboardProc chặn tận gốc các tổ hợp phím dùng để chuyển Desktop ảo (Win+Tab,
// Win+Ctrl+Trái/Phải/D/F4) và khóa máy (Win+L) — nuốt sự kiện phím (không gọi CallNextHookEx)
// để hệ điều hành không nhận được tổ hợp phím này.
func lowLevelKeyboardProc(nCode int32, wParam uintptr, lParam uintptr) uintptr {
	if nCode >= 0 && (wParam == wmKeyDown || wParam == wmSysKeyDown) {
		kb := (*kbdllhookstruct)(unsafe.Pointer(lParam))
		winDown := isKeyDown(vkLWin) || isKeyDown(vkRWin)
		if winDown && !violationsSuppressed() {
			switch kb.VkCode {
			case vkTab:
				// Win+Tab — mở Task View / danh sách Desktop ảo
				return 1
			case vkL:
				// Win+L — khóa máy trạm
				return 1
			case vkD, vkF4, vkLeft, vkRight:
				// Win+Ctrl+D/F4/Trái/Phải — tạo/đóng/chuyển Desktop ảo
				if isKeyDown(vkControl) {
					return 1
				}
			}
		}
	}
	ret, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return ret
}
