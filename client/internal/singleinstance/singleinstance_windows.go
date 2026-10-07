//go:build windows

package singleinstance

import (
	"syscall"
	"unsafe"

	"client/internal/winapi"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutexW = kernel32.NewProc("CreateMutexW")
	user32           = syscall.NewLazyDLL("user32.dll")
	procMessageBoxW  = user32.NewProc("MessageBoxW")

	// Giữ handle mutex suốt đời process — đóng = nhả lock.
	mutexHandle uintptr
)

const errorAlreadyExists = 183

// Acquire — chỉ cho phép 1 instance. Instance thứ 2: đưa cửa sổ cũ lên rồi báo lỗi.
func Acquire() error {
	namePtr, err := syscall.UTF16PtrFromString(mutexName)
	if err != nil {
		return err
	}
	r1, _, lastErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(namePtr)))
	if r1 == 0 {
		return lastErr
	}
	mutexHandle = r1
	if errno, ok := lastErr.(syscall.Errno); ok && errno == errorAlreadyExists {
		winapi.ActivateAppWindow(windowTitle)
		showAlreadyRunningDialog()
		return ErrAlreadyRunning
	}
	return nil
}

func showAlreadyRunningDialog() {
	title, _ := syscall.UTF16PtrFromString("Rikkei Lms Connect")
	msg, _ := syscall.UTF16PtrFromString("Ứng dụng Rikkei Lms Connect đang chạy.\n\nChỉ được mở một cửa sổ. Cửa sổ hiện có đã được đưa lên phía trước.")
	_, _, _ = procMessageBoxW.Call(0, uintptr(unsafe.Pointer(msg)), uintptr(unsafe.Pointer(title)), 0x00000040) // MB_ICONINFORMATION
}
