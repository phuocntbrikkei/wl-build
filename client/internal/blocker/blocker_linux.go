//go:build linux

package blocker

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"sync"
	"time"
)

var systemAllowed = map[string]bool{
	// ── Display servers / Compositors ─────────────────────────────────────────
	"gnome-shell":       true,
	"mutter":            true,
	"mutter-x11-fram":   true, // prefix match too
	"xwayland":          true,
	"xorg":              true,
	"Xorg":              true,
	"x11":               true,
	"kwin_wayland":      true, // KDE compositor
	"kwin_x11":          true,
	"plasmashell":       true, // KDE Plasma shell
	"hyprland":          true, // Hyprland Wayland compositor
	"sway":              true, // Sway compositor
	"wayfire":           true, // Wayfire compositor
	"river":             true, // River compositor
	"labwc":             true, // LabWC compositor
	"openbox":           true,
	"i3":                true,
	"i3bar":             true,
	"awesome":           true,
	"bspwm":             true,
	"xfwm4":             true,
	"marco":             true, // MATE wm
	"compiz":            true,
	"picom":             true,
	"compton":           true,

	// ── Wayland session tools ─────────────────────────────────────────────────
	"wl-paste":          true,
	"wl-copy":           true,
	"wlr-randr":         true,
	"kanshi":            true, // output manager
	"wlsunset":          true,
	"gammastep":         true,
	"swaylock":          true,
	"swayidle":          true,
	"swaybg":            true,
	"swaync":            true, // SwayNotificationCenter
	"waybar":            true, // Wayland statusbar
	"eww":               true, // ElKowar's wacky widgets
	"ags":               true, // Aylur's GTK Shell
	"rofi":              true, // app launcher (used in many WMs)
	"wofi":              true, // Wayland rofi
	"fuzzel":            true, // Wayland launcher
	"tofi":              true,
	"dmenu":             true,
	"bemenu":            true,

	// ── Session / Login managers ──────────────────────────────────────────────
	"gdm":               true,
	"gdm3":              true,
	"sddm":              true,
	"lightdm":           true,
	"lxdm":              true,
	"slim":              true,
	"greetd":            true,
	"gnome-session":     true,
	"gnome-session-bi":  true, // prefix matches binary
	"lxsession":         true,
	"startx":            true,
	"xinit":             true,

	// ── Terminals ─────────────────────────────────────────────────────────────
	"xterm":             true,
	"gnome-terminal":    true,
	"gnome-terminal-":   true, // prefix for server process
	"ptyxis":            true,
	"konsole":           true,
	"kitty":             true,
	"alacritty":         true,
	"wezterm":           true,
	"wezterm-gui":       true,
	"foot":              true,
	"xfce4-terminal":    true,
	"tilix":             true,
	"terminator":        true,
	"urxvt":             true,
	"rxvt":              true,
	"sakura":            true,
	"st":                true,

	// ── Shells ────────────────────────────────────────────────────────────────
	"bash":              true,
	"zsh":               true,
	"sh":                true,
	"fish":              true,
	"dash":              true,
	"ksh":               true,

	// ── Input methods (critical — killing these breaks Vietnamese typing) ─────
	"ibus-daemon":       true,
	"ibus-x11":          true,
	"ibus-":             true, // prefix: ibus-extension-, ibus-portal, ibus-engine-...
	"fcitx5":            true,
	"fcitx":             true,
	"fcitx-":            true, // prefix
	"uim":               true,
	"scim":              true,
	"sogou-qimpanel":    true,
	"gcin":              true,
	"kimpanel":          true,

	// ── GNOME core services ───────────────────────────────────────────────────
	"gjs":               true,
	"gsd-":              true, // prefix: gsd-keyboard, gsd-media-keys, gsd-power, gsd-color...
	"goa-daemon":        true,
	"goa-identity-ser":  true,
	"evolution-":        true, // prefix: evolution-calendar, evolution-addressbook...
	"gnome-keyring-d":   true,
	"gnome-keyring":     true,
	"polkit-gnome-au":   true,
	"polkitd":           true,
	"gnome-settings-d":  true,
	"gnome-initial-se":  true,
	"gnome-control-ce":  true,
	"gvfsd":             true,
	"gvfsd-":            true, // prefix
	"tracker-miner-":    true, // prefix
	"tracker3":          true,
	"zeitgeist":         true,
	"zeitgeist-":        true,
	"accounts-daemon":   true,
	"colord":            true,
	"power-profiles-":   true,
	"fprintd":           true,
	"fwupd":             true,
	"udisksd":           true,
	"upowerd":           true,
	"packagekitd":       true,
	"nm-dispatcher":     true,

	// ── D-Bus / XDG / AT-SPI ─────────────────────────────────────────────────
	"xdg-":              true, // prefix: xdg-desktop-portal, xdg-permission-store...
	"at-spi":            true, // prefix
	"at-spi-bus-laun":   true,
	"at-spi2-registr":   true,
	"dbus-daemon":       true,
	"dbus-launch":       true,

	// ── System services (often have GTK tray icons) ───────────────────────────
	"systemd":           true, // prefix match handles systemd-*
	"snapd-":            true, // prefix
	"snapd":             true,

	// ── Network/Bluetooth tray (critical for connectivity UI) ─────────────────
	"nm-applet":         true, // NetworkManager tray — if killed, students lose wifi UI
	"nm-tray":           true,
	"network-manager-":  true, // prefix
	"blueman-applet":    true, // Bluetooth tray — kills BT management
	"blueman-tray":      true,
	"blueman-manager":   true,
	"kdeconnectd":       true, // KDE Connect daemon
	"kdeconnect-indi":   true, // KDE Connect indicator

	// ── Notification daemons ──────────────────────────────────────────────────
	"dunst":             true,
	"mako":              true,
	"notify-osd":        true,
	"xfce4-notifyd":     true,
	"fnott":             true,

	// ── Polkit authentication agents ─────────────────────────────────────────
	"lxqt-policykit-":   true,
	"xfce-polkit":       true,
	"mate-polkit":       true,
	"pkttyagent":        true,

	// ── Clipboard managers (killing breaks copy/paste) ────────────────────────
	"copyq":             true,
	"clipit":            true,
	"xclip":             true,
	"xsel":              true,
	"clipman":           true,
	"greenclip":         true,

	// ── GTK/GNOME image helpers ───────────────────────────────────────────────
	"glycin":            true,
	"zenity":            true,
	"yad":               true,
	"kdialog":           true,

	// ── Screensaver / Lock ────────────────────────────────────────────────────
	"gnome-screensave":  true,
	"xscreensaver":      true,
	"xlock":             true,
	"i3lock":            true,

	// ── WebKit subprocesses (used by many GTK apps) ───────────────────────────
	"webkit":            true, // prefix
	"webkit2gtk":        true,
	"WebKitWebProcess":  true,
	"WebKitNetworkPro":  true, // prefix

	// ── Security / antivirus / updates (from app_pool + common) ───────────────
	"update-notifier":   true, // Ubuntu update notifier (app_pool)
	"apport-gtk":        true, // Ubuntu crash reporter (app_pool)
	"kgpg":              true, // KDE GPG encryption (app_pool)
	"ksecretd":          true, // KDE secrets daemon (app_pool)
	"clamtk":            true,
	"clamav":            true,
	"seahorse":          true, // GNOME password/keys manager
	"kwalletd":          true,
	"kwalletd5":         true,
	"kwalletd6":         true,
	"gufw":              true, // UFW firewall GUI
	"firewalld":         true,
	"pkexec":            true, // Polkit privilege elevation prompts

	// ── Remote support tools (safety) ────────────────────────────────────────
	"rustdesk":          true,
	"rustdesk-bin":      true,
	"anydesk":           true,
	"teamviewer":        true,
	"remmina":           true,

	// ── Our app + dev tools ───────────────────────────────────────────────────
	"wails":             true,
	"code":              true,
	"cursor":            true,
	"windsurf":          true,
	"goland":            true,
	"idea":              true,
	"client":            true,
	"simple_care_v1.0":  true,
	"simple_care_v1.1":  true,
	"simple_care_v1.2":  true,
	"simple_care_v1.3":  true,
	"simple_care":       true,
	"rescide":           true, // RE SC IDE — Theia-based IDE đi kèm (Python/Java/C++)
}

func isSystemAllowed(name string) bool {
	name = strings.ToLower(name)
	if systemAllowed[name] {
		return true
	}
	for pattern := range systemAllowed {
		if strings.HasPrefix(name, pattern) && pattern != name {
			return true
		}
	}
	return false
}

func getPPID(pid uint32) uint32 {
	statusBytes, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	lines := strings.Split(string(statusBytes), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "PPid:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				if ppid, err := strconv.ParseUint(parts[1], 10, 32); err == nil {
					return uint32(ppid)
				}
			}
		}
	}
	return 0
}

func isDescendantOf(pid, targetPid uint32) bool {
	curr := pid
	for i := 0; i < 10; i++ { // limits lookup to 10 ancestor levels
		ppid := getPPID(curr)
		if ppid == 0 {
			return false
		}
		if ppid == targetPid {
			return true
		}
		curr = ppid
	}
	return false
}

type ProcessInfo struct {
	PID  uint32
	Name string
}

func getVisibleProcesses() (map[uint32]ProcessInfo, error) {
	files, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	procs := make(map[uint32]ProcessInfo)
	for _, f := range files {
		if !f.IsDir() {
			continue
		}
		pid, err := strconv.ParseUint(f.Name(), 10, 32)
		if err != nil {
			continue
		}

		mapsPath := fmt.Sprintf("/proc/%d/maps", pid)
		mapsBytes, err := os.ReadFile(mapsPath)
		if err != nil {
			// Skip processes we don't own (permission denied)
			continue
		}

		mapsStr := string(mapsBytes)
		isGUI := strings.Contains(mapsStr, "libgtk") ||
			strings.Contains(mapsStr, "libQt") ||
			strings.Contains(mapsStr, "libX11") ||
			strings.Contains(mapsStr, "libwayland-client")

		if !isGUI {
			continue
		}

		// Read process name from /proc/PID/comm
		commBytes, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
		if err != nil {
			continue
		}
		procName := strings.TrimSpace(string(commBytes))

		if procName != "" {
			procs[uint32(pid)] = ProcessInfo{
				PID:  uint32(pid),
				Name: procName,
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
// giữa checkAndReport (chu kỳ 3s) và SnapshotOpenApps (chu kỳ 20s), tránh quét /proc 2 lần.
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
		isOurSubprocess := pid == myPid || isDescendantOf(pid, myPid)
		if isOurSubprocess || (currentExec != "" && pNameLower == currentExec) || isSystemAllowed(pNameLower) {
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

		// 1. Always allow our app, our sub-processes, system/critical developer tools, or agent helpers
		isOurSubprocess := pid == myPid || isDescendantOf(pid, myPid)
		if isOurSubprocess || (currentExec != "" && pNameLower == currentExec) || isSystemAllowed(pNameLower) {
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

		// 3. If not allowed, flag the violation (does not kill the process)
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
