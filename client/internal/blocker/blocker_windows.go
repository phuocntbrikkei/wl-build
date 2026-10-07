//go:build windows

package blocker

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procGetWindow                = user32.NewProc("GetWindow")
	procGetWindowLongW           = user32.NewProc("GetWindowLongW")
	procGetAncestor              = user32.NewProc("GetAncestor")

	dwmapi                    = syscall.NewLazyDLL("dwmapi.dll")
	procDwmGetWindowAttribute = dwmapi.NewProc("DwmGetWindowAttribute")

	kernel32 = syscall.NewLazyDLL("kernel32.dll")
)

const (
	GW_OWNER         = 4
	WS_EX_TOOLWINDOW = 0x00000080
	DWMWA_CLOAKED    = 14
)

func isRealGUIWindow(hwnd uintptr) bool {
	// 1. Phải đang hiển thị
	ret, _, _ := procIsWindowVisible.Call(hwnd)
	if ret == 0 {
		return false
	}

	// 2. Không được có chủ sở hữu (phải là cửa sổ chính - top-level)
	owner, _, _ := procGetWindow.Call(hwnd, GW_OWNER)
	if owner != 0 {
		return false
	}

	// 3. Không phải là tool window (WS_EX_TOOLWINDOW)
	gwlExStyle := int32(-20) // GWL_EXSTYLE = -20
	style, _, _ := procGetWindowLongW.Call(hwnd, uintptr(gwlExStyle))
	if (style & WS_EX_TOOLWINDOW) != 0 {
		return false
	}

	// 4. Không bị cloaked bởi DWM (ví dụ: app UWP bị treo/chạy ngầm, màn hình ảo)
	var cloaked uint32
	hr, _, _ := procDwmGetWindowAttribute.Call(hwnd, DWMWA_CLOAKED, uintptr(unsafe.Pointer(&cloaked)), 4)
	if hr == 0 && cloaked != 0 {
		return false
	}

	return true
}

func getProcessMap() (map[uint32]string, map[uint32]uint32, error) {
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, nil, err
	}
	defer syscall.CloseHandle(snapshot)

	var pe syscall.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))

	err = syscall.Process32First(snapshot, &pe)
	if err != nil {
		return nil, nil, err
	}

	pm := make(map[uint32]string)
	parents := make(map[uint32]uint32)
	for {
		name := syscall.UTF16ToString(pe.ExeFile[:])
		pm[pe.ProcessID] = name
		parents[pe.ProcessID] = pe.ParentProcessID

		err = syscall.Process32Next(snapshot, &pe)
		if err != nil {
			break
		}
	}
	return pm, parents, nil
}

func getWindowText(hwnd uintptr) string {
	length, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if length == 0 {
		return ""
	}
	buf := make([]uint16, length+1)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), length+1)
	return syscall.UTF16ToString(buf)
}

var (
	enumWindowsMutex sync.Mutex
	enumWindowsList  []WindowInfo
	enumProcessMap   map[uint32]string
	enumParentMap    map[uint32]uint32
)

var enumWindowsCallback = syscall.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[BLOCKER] Callback panic recovered: %v", r)
		}
	}()

	if !isRealGUIWindow(hwnd) {
		return 1
	}

	// Kiểm tra xem chủ sở hữu gốc (root owner) của cửa sổ này có thuộc về app ta hay không
	rootHwnd, _, _ := procGetAncestor.Call(hwnd, 3) // GA_ROOTOWNER = 3
	if rootHwnd != 0 {
		var rootPid uint32
		procGetWindowThreadProcessId.Call(rootHwnd, uintptr(unsafe.Pointer(&rootPid)))
		if rootPid == uint32(os.Getpid()) {
			return 1 // Cửa sổ thuộc về WebView2 / app của ta, bỏ qua không quét
		}
	}

	title := getWindowText(hwnd)
	if title == "" {
		return 1
	}

	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))

	procName := enumProcessMap[pid]
	if procName == "" {
		procName = "Unknown"
	}

	enumWindowsList = append(enumWindowsList, WindowInfo{
		PID:         pid,
		Title:       title,
		ProcessName: procName,
	})

	return 1
})

func EnumerateGUIWindows() ([]WindowInfo, map[uint32]uint32, error) {
	pMap, parentMap, err := getProcessMap()
	if err != nil {
		return nil, nil, err
	}

	enumWindowsMutex.Lock()
	defer enumWindowsMutex.Unlock()

	enumWindowsList = make([]WindowInfo, 0, 100)
	enumProcessMap = pMap
	enumParentMap = parentMap

	procEnumWindows.Call(enumWindowsCallback, 0)

	// Clean up map reference so GC can reclaim it
	enumProcessMap = nil
	enumParentMap = nil

	// Copy to a new slice to return safely
	res := make([]WindowInfo, len(enumWindowsList))
	copy(res, enumWindowsList)
	return res, parentMap, nil
}

const enumCacheTTL = 2 * time.Second

var (
	enumCacheMu      sync.Mutex
	enumCacheAt      time.Time
	enumCacheWindows []WindowInfo
	enumCacheParents map[uint32]uint32
	enumCacheErr     error
)

// cachedEnumerateGUIWindows — như EnumerateGUIWindows nhưng dùng chung kết quả trong enumCacheTTL
// giữa checkAndReport (chu kỳ 3s) và SnapshotOpenApps (chu kỳ 20s) để tránh quét tiến trình/cửa sổ 2 lần.
func cachedEnumerateGUIWindows() ([]WindowInfo, map[uint32]uint32, error) {
	enumCacheMu.Lock()
	defer enumCacheMu.Unlock()
	if time.Since(enumCacheAt) < enumCacheTTL {
		return enumCacheWindows, enumCacheParents, enumCacheErr
	}
	windows, parents, err := EnumerateGUIWindows()
	enumCacheWindows, enumCacheParents, enumCacheErr = windows, parents, err
	enumCacheAt = time.Now()
	return windows, parents, err
}

var systemAllowed = map[string]bool{
	// ── Windows Shell & Explorer ───────────────────────────────────────────────
	"explorer.exe":                    true, // Windows Explorer / desktop shell
	"shellexperiencehost.exe":         true, // Start menu, Action Center shell
	"startmenuexperiencehost.exe":     true, // Start menu host
	"searchhost.exe":                  true, // Windows Search UI
	"searchapp.exe":                   true, // Windows Search (older)
	"searchindexer.exe":               true,
	"sihost.exe":                      true, // Shell Infrastructure Host (taskbar, notification icons)
	"taskhostw.exe":                   true, // Task Host Window
	"taskbar.exe":                     true,
	"lockapp.exe":                     true, // Lock screen
	"logonui.exe":                     true, // Login/Logon UI
	"winlogon.exe":                    true, // Windows Logon Process
	"userinit.exe":                    true,
	"dwm.exe":                         true, // Desktop Window Manager — kill = black screen
	"csrss.exe":                       true, // Client Server Runtime — kill = BSOD

	// ── UWP / Modern App Infrastructure ───────────────────────────────────────
	"applicationframehost.exe":        true, // Host for ALL UWP apps (Camera, Calculator, Photos...)
	"runtimebroker.exe":               true, // UWP permission broker
	"backgroundtaskhost.exe":          true, // UWP background tasks
	"wwahost.exe":                     true, // Web app host
	"microsoftedgecp.exe":             true, // Edge content process (older)
	"textinputhost.exe":               true, // Touch keyboard / handwriting panel

	// ── Input Methods & Language ───────────────────────────────────────────────
	"ctfmon.exe":                      true, // Collaborative Translation Framework — input method manager
	"chsime.exe":                      true, // Chinese IME
	"imetip.exe":                      true, // IME tip
	"imecmnt.exe":                     true,
	"googlepinyin.exe":                true,
	"baidu.exe":                       true, // Baidu IME
	"openkey.exe":                     true, // OpenKey Vietnamese IME
	"openkey64.exe":                   true,
	"gotiengviet.exe":                 true, // GoTiengViet
	"unikeyvnt.exe":                   true, // Unikey
	"unikey.exe":                      true,

	// ── Security / Credential / UAC ───────────────────────────────────────────
	"consent.exe":                     true, // UAC consent dialog — kill breaks all elevation
	"credentialuibroker.exe":          true, // Credential UI
	"smartscreen.exe":                 true, // Windows SmartScreen
	"securityhealthsystray.exe":       true, // Windows Security tray icon
	"securityhealthservice.exe":       true,
	"wscsvc.exe":                      true,
	"msseces.exe":                     true, // Microsoft Security Essentials tray
	"msmpeng.exe":                     true, // Windows Defender engine (has no GUI but safety)
	"nisSrv.exe":                      true,
	"antimalware service executable":  true, // Window title of Defender

	// ── Notifications & Action Center ─────────────────────────────────────────
	"notificationplatformcontroller": true,

	// ── System Tray / Taskbar helpers ─────────────────────────────────────────
	"systemsettings.exe":              true, // Windows Settings
	"settingssynchostservice.exe":     true,
	"settingssynchost.exe":            true,
	"regsvc.exe":                      true,
	"spoolsv.exe":                     true,
	"tabtip.exe":                      true, // Touch keyboard
	"tabtip32.exe":                    true,
	"onedrive.exe":                    true, // OneDrive tray (common, safe to keep)
	"onedriveupdater.exe":             true,

	// ── Accessibility ─────────────────────────────────────────────────────────
	"narrator.exe":                    true,
	"magnify.exe":                     true,
	"osk.exe":                         true, // On-Screen Keyboard

	// ── Audio ─────────────────────────────────────────────────────────────────
	"audiodg.exe":                     true, // Windows Audio Device Graph — kill = no sound
	"sndvol.exe":                      true, // Volume mixer
	"cmediaaudiocontrolpanel.exe":     true, // C-Media audio panel

	// ── Windows Update / Store ─────────────────────────────────────────────────
	"wuauclt.exe":                     true, // Windows Update
	"musnotifyicon.exe":               true, // Update tray notification
	"windowsstore.exe":                true,
	"winstore.app.exe":                true,

	// ── Drivers / Hardware UI ──────────────────────────────────────────────────
	"nvdisplay.container.exe":         true, // NVIDIA display container
	"nvcontainer.exe":                 true,
	"nvinject.exe":                    true,
	"nvtelemetrycontainer.exe":        true,
	"nvvsvc.exe":                      true,
	"nvspcaps64.exe":                  true,
	"geforce experience.exe":          true,
	"radeonsoftware.exe":              true, // AMD Radeon Software
	"amddvr.exe":                      true,
	"igfxtray.exe":                    true, // Intel graphics tray
	"igfxem.exe":                      true,
	"igfxhk.exe":                      true,
	"hkcmd.exe":                       true,
	"atk hub.exe":                     true, // ASUS ATK
	"atkex.exe":                       true,
	"asusoptimization.exe":            true,

	// ── Antivirus / endpoint security (from app_pool + common) ────────────────
	"avastui.exe":                     true,
	"avgui.exe":                       true,
	"mbam.exe":                        true, // Malwarebytes
	"mbamtray.exe":                    true,
	"bdagent.exe":                     true, // Bitdefender
	"bdwtxag.exe":                     true, // Bitdefender widget agent
	"uiseagnt.exe":                    true, // Trend Micro
	"eguiproxy.exe":                   true, // ESET proxy
	"egui.exe":                        true, // ESET NOD32 GUI (app_pool)
	"microsoftsecurityapp.exe":        true, // Microsoft Defender app (app_pool)
	"msascuil.exe":                    true, // Defender notification icon
	"pickerhost.exe":                  true, // Windows Security picker (app_pool)
	"seccenter.exe":                   true, // Security center dialogs (app_pool)
	"mc-web-view.exe":                 true, // McAfee web view (app_pool)
	"rsappui.exe":                     true, // RAV Endpoint Protection (app_pool)
	"avpui.exe":                       true, // Kaspersky UI
	"ksdeui.exe":                      true, // Kaspersky Secure Connection
	"n360.exe":                        true, // Norton 360
	"nortonsecurity.exe":              true,
	"sophos ui.exe":                   true, // Sophos UI

	// ── Task Manager & System tools ────────────────────────────────────────────
	"taskmgr.exe":                     true,
	"resmon.exe":                      true, // Resource Monitor
	"perfmon.exe":                     true,
	"mmc.exe":                         true, // Management Console

	// ── Terminal / Shell ───────────────────────────────────────────────────────
	"cmd.exe":                         true,
	"powershell.exe":                  true,
	"pwsh.exe":                        true, // PowerShell Core
	"conhost.exe":                     true, // Console Host
	"windowsterminal.exe":             true, // Windows Terminal
	"wt.exe":                          true,
	"bash.exe":                        true,
	"git-bash.exe":                    true,
	"mintty.exe":                      true, // Git Bash window
	"wsl.exe":                         true,
	"wslhost.exe":                     true,

	// ── Our app + IDE/dev tools ────────────────────────────────────────────────
	"client.exe":                      true,
	"simple_care_v1.0.exe":            true,
	"simple_care_v1.1.exe":            true,
	"simple_care_v1.2.exe":            true,
	"simple_care_v1.3.exe":            true,
	"simple_care.exe":                 true,
	"wails.exe":                       true,
	"msedgewebview2.exe":              true, // WebView2 runtime (Wails renderer)
	"code.exe":                        true,
	"cursor.exe":                      true,
	"windsurf.exe":                    true,
	"goland.exe":                      true,
	"goland64.exe":                    true,
	"idea64.exe":                      true,
	"clion64.exe":                     true,
	"webstorm64.exe":                  true,
	"pycharm64.exe":                   true,
	"rider64.exe":                     true,
	"studio64.exe":                    true,
	"eclipse.exe":                     true,
	"sublime_text.exe":                true,
	"notepad++.exe":                   true,
	"devenv.exe":                      true,
	"rescide.exe":                     true, // RE SC IDE — Theia-based IDE đi kèm (Python/Java/C++)
}


// SnapshotOpenApps trả về danh sách ứng dụng đang mở có giao diện (loại trừ chính app này và các app/tiến trình hệ thống).
func (b *Blocker) SnapshotOpenApps() []WindowInfo {
	windows, parentMap, err := cachedEnumerateGUIWindows()
	if err != nil {
		log.Printf("[BLOCKER] SnapshotOpenApps failed: %v", err)
		return nil
	}

	currentExec := ""
	if execPath, err := os.Executable(); err == nil {
		currentExec = strings.ToLower(filepath.Base(execPath))
	}

	myPid := uint32(os.Getpid())
	out := make([]WindowInfo, 0, len(windows))
	for _, w := range windows {
		pNameLower := strings.ToLower(w.ProcessName)
		isOurApp := w.PID == myPid || isDescendant(w.PID, myPid, parentMap)
		if isOurApp || (currentExec != "" && pNameLower == currentExec) || systemAllowed[pNameLower] {
			continue
		}
		out = append(out, w)
	}
	return out
}

func isDescendant(pid, targetPid uint32, parentMap map[uint32]uint32) bool {
	curr := pid
	for i := 0; i < 16; i++ {
		parent, ok := parentMap[curr]
		if !ok || parent == 0 {
			return false
		}
		if parent == targetPid {
			return true
		}
		curr = parent
	}
	return false
}

func (b *Blocker) checkAndReport() {
	b.mu.Lock()
	keywords := make([]string, len(b.allowedKeywords))
	copy(keywords, b.allowedKeywords)
	b.mu.Unlock()

	// Nếu không cấu hình keyword thì không chặn gì cả
	if len(keywords) == 0 {
		return
	}

	currentExec := ""
	if execPath, err := os.Executable(); err == nil {
		currentExec = strings.ToLower(filepath.Base(execPath))
	}

	windows, parentMap, err := cachedEnumerateGUIWindows()
	if err != nil {
		return
	}

	myPid := uint32(os.Getpid())
	for _, w := range windows {
		pNameLower := strings.ToLower(w.ProcessName)
		wTitleLower := strings.ToLower(w.Title)

		// 1. Luôn cho phép hệ thống/app cốt lõi hoặc chính tiến trình này (bao gồm tiến trình con/cháu, và đổi tên)
		isOurApp := w.PID == myPid || isDescendant(w.PID, myPid, parentMap)
		if isOurApp || (currentExec != "" && pNameLower == currentExec) || systemAllowed[pNameLower] {
			continue
		}

		// 2. Kiểm tra xem có chứa bất kỳ từ khóa nào được cho phép không
		allowed := false
		for _, kw := range keywords {
			if matchesAllowedKeyword(kw, pNameLower, wTitleLower) {
				allowed = true
				break
			}
		}

		// 3. Không nằm trong whitelist: chụp+ghi vi phạm (throttle trong OnBlocked)
		//    rồi TỰ TẮT ứng dụng.
		if !allowed {
			if b.OnBlocked != nil {
				b.OnBlocked(w.ProcessName, w.Title)
			}
			if b.KillOnBlock {
				terminateProcess(w.PID)
			}
			log.Printf("[BLOCKER] Unauthorized application blocked: %s (PID: %d, Title: %s)", w.ProcessName, w.PID, w.Title)
		}
	}
}

// terminateProcess buộc đóng 1 tiến trình theo PID (ứng dụng không cho phép).
func terminateProcess(pid uint32) {
	const processTerminate = 0x0001
	h, err := syscall.OpenProcess(processTerminate, false, pid)
	if err != nil {
		return
	}
	defer syscall.CloseHandle(h)
	_ = syscall.TerminateProcess(h, 1)
}
