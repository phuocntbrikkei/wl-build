//go:build windows

// Package proxywatch chạy 1 tiến trình "watchdog" cùng file exe (chế độ ẩn) để
// canh tiến trình app chính. Khi app chính chết BẤT NGỜ (crash/panic/Task Manager
// kill) mà chưa kịp tự gỡ proxy, watchdog sẽ gỡ proxy hệ thống ngay — máy không
// bị kẹt mạng và KHÔNG cần mở lại app.
package proxywatch

import (
	"log"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"client/internal/netproxy"

	"golang.org/x/sys/windows"
)

const envParent = "SC_PROXY_WATCHDOG"

// ParentPID: nếu tiến trình này được chạy ở chế độ watchdog (có env kèm pid cha),
// trả pid cha + true. main() dùng để rẽ nhánh sang Run() thay vì mở app.
func ParentPID() (int, bool) {
	v := os.Getenv(envParent)
	if v == "" {
		return 0, false
	}
	pid, err := strconv.Atoi(v)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// Spawn khởi chạy watchdog canh tiến trình hiện tại (gọi 1 lần lúc app khởi động).
func Spawn() {
	exe, err := os.Executable()
	if err != nil {
		log.Printf("[WATCHDOG] cannot resolve exe: %v", err)
		return
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), envParent+"="+strconv.Itoa(os.Getpid()))
	// CREATE_NO_WINDOW: không hiện cửa sổ console; không Wait -> watchdog sống độc
	// lập, sống sót khi tiến trình cha chết.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	if err := cmd.Start(); err != nil {
		log.Printf("[WATCHDOG] spawn failed: %v", err)
		return
	}
	log.Printf("[WATCHDOG] spawned pid=%d watching parent=%d", cmd.Process.Pid, os.Getpid())
}

// Run (trong tiến trình watchdog): chặn tới khi tiến trình cha kết thúc rồi gỡ
// proxy nếu phiên đó có bật. RecoverStaleProxyIfAny chỉ hành động khi còn file
// state (app từng bật proxy) nên tắt bình thường (app tự gỡ proxy) sẽ là no-op.
func Run(ppid int) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(ppid))
	if err == nil {
		_, _ = windows.WaitForSingleObject(h, windows.INFINITE)
		_ = windows.CloseHandle(h)
	} else {
		log.Printf("[WATCHDOG] OpenProcess(%d) failed: %v — recovering proxy anyway", ppid, err)
	}
	netproxy.RecoverStaleProxyIfAny()
	log.Printf("[WATCHDOG] parent %d ended — proxy recovery done, watchdog exiting", ppid)
}
