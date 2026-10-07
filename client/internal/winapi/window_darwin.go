//go:build darwin

package winapi

import (
	"fmt"
	"os/exec"
)

// ActivateAppWindow — đưa cửa sổ Rikkei Lms Connect đang chạy lên trước (theo tên process / title).
func ActivateAppWindow(titleHint string) {
	hint := titleHint
	if hint == "" {
		hint = "Rikkei Lms Connect"
	}
	script := fmt.Sprintf(`
tell application "System Events"
  set candidates to every process whose name contains %q or name contains "simple_care" or name contains "SimpleCare"
  if (count of candidates) > 0 then
    set frontmost of item 1 of candidates to true
  end if
end tell
`, hint)
	cmd := exec.Command("osascript", "-e", script)
	_ = cmd.Run()
}

// PlayNotifySound — beep hệ thống macOS
func PlayNotifySound() {
	cmd := exec.Command("osascript", "-e", "beep")
	_ = cmd.Run()
}

// ShowWarningMessageBox hiển thị hộp thoại cảnh báo macOS bất đồng bộ
func ShowWarningMessageBox(title, message string) {
	script := fmt.Sprintf(`display alert %q message %q`, title, message)
	cmd := exec.Command("osascript", "-e", script)
	_ = cmd.Start() // Run asynchronously
}
