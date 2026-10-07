//go:build darwin

package blocker

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"sync"
	"time"
)

var systemAllowed = map[string]bool{
	// ── Core macOS Desktop Infrastructure ─────────────────────────────────────
	"finder":                          true, // macOS file manager / desktop
	"dock":                            true, // macOS Dock — kill = dock disappears
	"windowserver":                    true, // WindowServer — kill = instant logout
	"loginwindow":                     true, // Login/session manager — kill = logout
	"systemuiserver":                  true, // Menu bar icons (volume, wifi, battery, clock)
	"controlcenter":                   true, // macOS Control Center (Monterey+)
	"notificationcenter":              true, // Notification Center
	"spotlight":                       true, // Spotlight search
	"launchpad":                       true,
	"mission control":                 true,
	"exposé":                          true,
	"universalaccessd":                true,
	"accessibilityuiagent":            true, // Accessibility helper

	// ── Input Methods & Language (critical — kill = can't type) ───────────────
	"inputmethodkit":                  true,
	"ibus":                            true,
	"hiragana kakomi input":           true,
	"kinput2":                         true,
	"squirrel":                        true, // Rime input method
	"scim":                            true,
	"kotoeri":                         true, // Japanese IME
	"pinyin - simplified":             true, // macOS Chinese Pinyin
	"zhuyin - traditional":            true,
	"vietnamese":                      true, // macOS built-in Vietnamese IME
	"abc":                             true, // macOS ABC keyboard input

	// ── Security / Keychain / Authentication ──────────────────────────────────
	"securityagent":                   true, // macOS security agent — kill breaks sudo GUI, Keychain prompts
	"keychain":                        true, // Keychain access
	"keychainservicesagent":           true,
	"trustd":                          true,
	"opendirectoryd":                  true,
	"authorizationhost":               true, // Authorization host — UAC equivalent
	"securityd":                       true,
	"coreauthenticationd":             true,
	"biometricd":                      true,
	"touchidd":                        true,

	// ── Security utilities / AV (from app_pool + common) ──────────────────────
	"activity monitor":                true, // System monitor (app_pool)
	"passwords":                       true, // Apple Passwords / iCloud Keychain (app_pool)
	"xprotectservice":                 true, // macOS built-in malware protection
	"xprotect":                        true,
	"malware removal tool":            true,
	"mrt":                             true,
	"avast":                           true,
	"avast security":                  true,
	"bitdefender":                     true,
	"norton":                          true,
	"sophos":                          true,
	"malwarebytes":                    true,
	"eset":                            true,
	"little snitch":                   true,
	"lulu":                            true,

	// ── Audio / Media ─────────────────────────────────────────────────────────
	"coreaudiod":                      true, // Core Audio daemon — kill = no sound
	"audioundockhelper":               true,
	"audio midi setup":                true,
	"noiseremoval":                    true,

	// ── Networking / VPN ──────────────────────────────────────────────────────
	"networkd":                        true,
	"nesessionmanager":                true, // Network Extension — kill drops VPN
	"scutil":                          true,
	"configd":                         true,
	"mDNSResponder":                   true, // Bonjour DNS

	// ── Spotlight / File Indexing ──────────────────────────────────────────────
	"mds":                             true, // Spotlight metadata server
	"mds_stores":                      true,
	"mdworker":                        true, // prefix match covers mdworker_shared
	"mdworker_shared":                 true,

	// ── iCloud / Apple Services ───────────────────────────────────────────────
	"bird":                            true, // iCloud Drive daemon
	"cloudd":                          true,
	"com.apple.icloud":                true, // prefix
	"cloudphotod":                     true,
	"nsurlsessiond":                   true,

	// ── System Preferences / Settings ─────────────────────────────────────────
	"system preferences":              true, // macOS System Preferences (pre-Ventura)
	"system settings":                 true, // macOS System Settings (Ventura+)
	"software update":                 true,
	"app store":                       true,

	// ── Screen / Display ──────────────────────────────────────────────────────
	"screensaver engine":              true, // Screensaver
	"com.apple.screensaver":           true,
	"colorsyncd":                      true,
	"colorsync utility":               true,
	"nightshift":                      true,
	"display menu":                    true,

	// ── Clipboard / Pasteboard ────────────────────────────────────────────────
	"pboard":                          true, // Pasteboard daemon — kill breaks copy/paste

	// ── Printing ─────────────────────────────────────────────────────────────
	"printingproxy":                   true,
	"cupsd":                           true,

	// ── Crash Reporting / Diagnostics ─────────────────────────────────────────
	"crashreporter":                   true,
	"diagnosticsd":                    true,
	"spindump":                        true,
	"reportmemoryexception":           true,

	// ── Webkit / App subprocesses ─────────────────────────────────────────────
	"webkit":                          true, // prefix
	"com.apple.webkit":                true, // prefix
	"com.apple.webkit.networking":     true,

	// ── Remote support ────────────────────────────────────────────────────────
	"applescriptkit":                  true,
	"applescript runner":              true,
	"rustdesk":                        true,
	"anydesk":                         true,
	"teamviewer":                      true,
	"screen sharing":                  true,
	"screensharingd":                  true, // macOS Screen Sharing

	// ── Terminals ─────────────────────────────────────────────────────────────
	"terminal":                        true, // macOS Terminal
	"iterm":                           true,
	"iterm2":                          true,
	"wezterm":                         true,
	"kitty":                           true,
	"alacritty":                       true,
	"hyper":                           true,

	// ── Shells ────────────────────────────────────────────────────────────────
	"bash":                            true,
	"zsh":                             true,
	"sh":                              true,
	"fish":                            true,

	// ── AppleScript / Automation ──────────────────────────────────────────────
	"system events":                   true, // AppleScript System Events (used by our blocker itself)
	"osascript":                       true, // AppleScript runner (used by our getVisibleProcesses)

	// ── Git & Credential Helpers ──────────────────────────────────────────────
	"git":                             true,
	"git-credential-manager":         true,
	"git-credential-osxkeychain":     true,
	"github desktop":                  true,
	"sourcetree":                      true,
	"fork":                            true,

	// ── Docker ───────────────────────────────────────────────────────────────
	"docker":                          true,
	"docker desktop":                  true,
	"com.docker":                      true, // prefix

	// ── Our app + IDE/dev tools ────────────────────────────────────────────────
	"client":                          true,
	"simple_care_v1.0":                true,
	"simple_care_v1.1":                true,
	"simple_care_v1.2":                true,
	"simple_care_v1.3":                true,
	"simple_care":                     true,
	"wails":                           true,
	"code":                            true, // VSCode
	"cursor":                          true,
	"windsurf":                        true,
	"goland":                          true,
	"idea":                            true,
	"clion":                           true,
	"webstorm":                        true,
	"pycharm":                         true,
	"rider":                           true,
	"studio":                          true, // Android Studio
	"eclipse":                         true,
	"sublime text":                    true,
	"rescide":                         true, // RE SC IDE — Theia-based IDE đi kèm (Python/Java/C++)
}
type ProcessInfo struct {
	Name     string
	BundleID string
}

func getVisibleProcesses() (map[uint32]ProcessInfo, error) {
	script := `tell application "System Events"
  set out to ""
  set procList to every process whose visible is true
  repeat with p in procList
    try
      set nameStr to name of p
      set pidVal to unix id of p
      set bid to bundle identifier of p
      if bid is missing value then
        set bid to ""
      end if
      set out to out & nameStr & "|" & pidVal & "|" & bid & "\n"
    on error
      -- ignore
    end try
  end repeat
  return out
end tell`
	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	procs := make(map[uint32]ProcessInfo)
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 2 {
			continue
		}
		pName := parts[0]
		pIdStr := parts[1]
		bundleID := ""
		if len(parts) >= 3 {
			bundleID = parts[2]
		}
		var pid uint32
		if _, err := fmt.Sscanf(pIdStr, "%d", &pid); err == nil {
			procs[pid] = ProcessInfo{
				Name:     pName,
				BundleID: bundleID,
			}
		}
	}
	return procs, nil
}

const procCacheTTL = 2 * time.Second

var (
	procCacheMu  sync.Mutex
	procCacheAt  time.Time
	procCacheMap map[uint32]ProcessInfo
	procCacheErr error
)

// cachedGetVisibleProcesses — như getVisibleProcesses nhưng dùng chung kết quả trong procCacheTTL
// giữa checkAndReport (chu kỳ 3s) và SnapshotOpenApps (chu kỳ 20s), tránh gọi osascript 2 lần.
func cachedGetVisibleProcesses() (map[uint32]ProcessInfo, error) {
	procCacheMu.Lock()
	defer procCacheMu.Unlock()
	if time.Since(procCacheAt) < procCacheTTL {
		return procCacheMap, procCacheErr
	}
	procs, err := getVisibleProcesses()
	procCacheMap, procCacheErr = procs, err
	procCacheAt = time.Now()
	return procs, err
}

// SnapshotOpenApps trả về danh sách ứng dụng đang mở có giao diện (loại trừ chính app này và các process hệ thống).
func (b *Blocker) SnapshotOpenApps() []WindowInfo {
	procs, err := cachedGetVisibleProcesses()
	if err != nil {
		log.Printf("[BLOCKER] SnapshotOpenApps failed: %v", err)
		return nil
	}

	currentExec := ""
	if execPath, err := os.Executable(); err == nil {
		currentExec = strings.ToLower(filepath.Base(execPath))
	}

	myPid := uint32(os.Getpid())
	out := make([]WindowInfo, 0, len(procs))
	for pid, info := range procs {
		pNameLower := strings.ToLower(info.Name)
		if pid == myPid || (currentExec != "" && pNameLower == currentExec) || systemAllowed[pNameLower] {
			continue
		}
		out = append(out, WindowInfo{PID: pid, Title: info.Name, ProcessName: info.Name})
	}
	return out
}

func (b *Blocker) checkAndReport() {
	b.mu.Lock()
	keywords := make([]string, len(b.allowedKeywords))
	copy(keywords, b.allowedKeywords)
	b.mu.Unlock()

	if len(keywords) == 0 {
		return
	}

	currentExec := ""
	if execPath, err := os.Executable(); err == nil {
		currentExec = strings.ToLower(filepath.Base(execPath))
	}

	procs, err := cachedGetVisibleProcesses()
	if err != nil {
		log.Printf("[BLOCKER] Failed to get visible processes: %v", err)
		return
	}

	myPid := uint32(os.Getpid())
	for pid, info := range procs {
		pNameLower := strings.ToLower(info.Name)

		// 1. Always allow our app, system/critical developer tools, or agent helpers
		if pid == myPid || (currentExec != "" && pNameLower == currentExec) || systemAllowed[pNameLower] {
			continue
		}

		// 2. Check if the process name contains any allowed keywords
		allowed := false
		for _, kw := range keywords {
			if matchesAllowedKeyword(kw, pNameLower, pNameLower) {
				allowed = true
				break
			}
		}

		// 3. If not allowed, flag the violation (does not close the application)
		if !allowed {
			if b.OnBlocked != nil {
				b.OnBlocked(info.Name, info.Name)
			}
			if b.KillOnBlock {
				terminateProcess(pid)
			}
			log.Printf("[BLOCKER] Unauthorized application blocked: %s (PID: %d)", info.Name, pid)
		}
	}
}

// terminateProcess buộc đóng 1 tiến trình theo PID (ứng dụng không cho phép).
func terminateProcess(pid uint32) {
	_ = syscall.Kill(int(pid), syscall.SIGKILL)
}
