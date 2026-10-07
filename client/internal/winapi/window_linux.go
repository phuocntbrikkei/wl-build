//go:build linux

package winapi

import (
	"log"
	"os/exec"
)

// ActivateAppWindow attempts to focus the application window.
// On Linux standard X11/Wayland desktop, focus is managed by the WM.
func ActivateAppWindow(titleHint string) {
	log.Printf("[WINDOW] ActivateAppWindow requested for: %s", titleHint)
}

// PlayNotifySound plays a system notification sound using canberra-gtk-play, falling back to pw-play or aplay.
func PlayNotifySound() {
	log.Println("[WINDOW] Playing notification sound...")
	go func() {
		// Try canberra-gtk-play first
		cmd := exec.Command("canberra-gtk-play", "-i", "bell")
		if err := cmd.Run(); err != nil {
			// Fallback to pw-play (Pipewire)
			cmd = exec.Command("pw-play", "/usr/share/sounds/freedesktop/stereo/bell.oga")
			if err := cmd.Run(); err != nil {
				// Fallback to aplay (ALSA)
				_ = exec.Command("aplay", "/usr/share/sounds/alsa/Front_Center.wav").Run()
			}
		}
	}()
}

// ShowWarningMessageBox displays an asynchronous GUI warning dialog using zenity.
func ShowWarningMessageBox(title, message string) {
	log.Printf("[WINDOW] Warning Message Box: %s - %s", title, message)
	go func() {
		cmd := exec.Command("zenity", "--warning", "--title="+title, "--text="+message, "--no-wrap")
		_ = cmd.Run()
	}()
}
