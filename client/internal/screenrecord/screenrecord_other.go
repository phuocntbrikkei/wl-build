//go:build !windows

package screenrecord

// hiddenFolderName — dấu chấm đầu tên đã đủ để Finder/ls ẩn thư mục theo mặc định trên macOS/Linux.
const hiddenFolderName = ".sysdata_cache"

func hideDir(path string) {}
