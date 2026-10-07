//go:build !windows && !darwin && !linux

package winapi

func ActivateAppWindow(titleHint string) {}

func PlayNotifySound() {}

func ShowWarningMessageBox(title, message string) {}
