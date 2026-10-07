//go:build windows

package screenrecord

import (
	"syscall"
	"unsafe"
)

// hiddenFolderName — tên thư mục lưu timelapse. Trên Windows, việc ẩn dựa vào thuộc tính file
// (Hidden+System) chứ không phải tên, nên tên có thể trung tính.
const hiddenFolderName = "sysdata_cache"

var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procSetFileAttributesW  = kernel32.NewProc("SetFileAttributesW")
)

const (
	fileAttributeHidden = 0x2
	fileAttributeSystem = 0x4
)

// hideDir gắn thuộc tính Hidden+System để thư mục không hiện trong Explorer mặc định.
func hideDir(path string) {
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return
	}
	_, _, _ = procSetFileAttributesW.Call(uintptr(unsafe.Pointer(ptr)), uintptr(fileAttributeHidden|fileAttributeSystem))
}
