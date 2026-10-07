package singleinstance

import "errors"

// ErrAlreadyRunning — đã có một tiến trình Rikkei Lms Connect đang chạy.
var ErrAlreadyRunning = errors.New("simple care already running")

const (
	// mutexName/thư mục lưu trữ giữ nguyên định danh nội bộ "SimpleCare" (không đổi theo tên
	// hiển thị) để không làm mất dữ liệu phiên/log cục bộ của các máy đã cài bản cũ.
	mutexName   = "Local\\SimpleCare_SingleInstance"
	windowTitle = "Rikkei Lms Connect"
)
