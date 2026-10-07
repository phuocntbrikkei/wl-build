//go:build darwin

package browser

/*
#cgo darwin CFLAGS: -x objective-c -fobjc-arc
#cgo darwin LDFLAGS: -framework Cocoa -framework WebKit
*/
import "C"
