//go:build darwin

package camera

/*
#cgo LDFLAGS: -framework AVFoundation -framework Foundation -framework CoreImage -framework CoreMedia -framework CoreVideo -framework ImageIO -framework AppKit
#include <stdlib.h>

int StartNativeCamera(void);
void StopNativeCamera(void);
char* GetLatestCameraFrame(void);
*/
import "C"
import (
	"fmt"
	"log"
	"unsafe"
)

const IsNative = true

// StartCapture starts native camera capture via AVCaptureSession
func StartCapture() error {
	ret := C.StartNativeCamera()
	if ret != 0 {
		log.Println("[CAMERA] Failed to start native camera capture")
		return fmt.Errorf("failed to start camera (code %d)", int(ret))
	}
	log.Println("[CAMERA] Native camera capture started successfully")
	return nil
}

// StopCapture stops native camera capture
func StopCapture() {
	C.StopNativeCamera()
	log.Println("[CAMERA] Native camera capture stopped")
}

// GetFrame returns the latest camera frame as a data:image/jpeg;base64,... string
// Returns empty string if no frame is available
func GetFrame() string {
	cstr := C.GetLatestCameraFrame()
	if cstr == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(cstr))
	return C.GoString(cstr)
}
