//go:build linux

package browser

/*
#cgo linux pkg-config: gtk+-3.0
#cgo !webkit2_41 pkg-config: webkit2gtk-4.0
#cgo webkit2_41 pkg-config: webkit2gtk-4.1
*/
import "C"
