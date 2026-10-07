//go:build darwin

package screen

/*
#cgo CFLAGS: -x objective-c -Wno-deprecated-declarations -Wno-unguarded-availability-new
#cgo LDFLAGS: -framework CoreGraphics -framework Foundation -framework AppKit

// Disable availability checks - we handle this at runtime
#define __API_UNAVAILABLE(...)
#define API_UNAVAILABLE(...)

#import <CoreGraphics/CoreGraphics.h>
#import <AppKit/AppKit.h>
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>

// Use dlsym to call CGWindowListCreateImage dynamically to bypass macOS 15 availability check
typedef CGImageRef (*CGWindowListCreateImageFunc)(CGRect, CGWindowListOption, CGWindowID, CGWindowImageOption);

static unsigned char* CaptureFullDesktop(int* outLen, int quality) {
    // Dynamically load CGWindowListCreateImage to bypass compile-time availability check
    static CGWindowListCreateImageFunc createImageFunc = NULL;
    if (!createImageFunc) {
        void *handle = dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", RTLD_LAZY);
        if (handle) {
            createImageFunc = (CGWindowListCreateImageFunc)dlsym(handle, "CGWindowListCreateImage");
        }
    }

    if (!createImageFunc) {
        *outLen = 0;
        return NULL;
    }

    CGImageRef image = createImageFunc(
        CGRectInfinite,
        kCGWindowListOptionOnScreenOnly,
        kCGNullWindowID,
        kCGWindowImageDefault
    );

    if (!image) {
        *outLen = 0;
        return NULL;
    }

    @autoreleasepool {
        // Calculate new size maintaining aspect ratio (limiting to max 1024x768)
        CGFloat originalWidth = CGImageGetWidth(image);
        CGFloat originalHeight = CGImageGetHeight(image);
        CGFloat maxWidth = 1024.0f;
        CGFloat maxHeight = 768.0f;
        CGFloat ratio = 1.0f;
        if (originalWidth > maxWidth || originalHeight > maxHeight) {
            CGFloat ratioW = maxWidth / originalWidth;
            CGFloat ratioH = maxHeight / originalHeight;
            ratio = ratioW < ratioH ? ratioW : ratioH;
        }
        size_t newWidth = (size_t)(originalWidth * ratio);
        size_t newHeight = (size_t)(originalHeight * ratio);
        if (newWidth < 1) newWidth = 1;
        if (newHeight < 1) newHeight = 1;

        // Create bitmap context
        CGColorSpaceRef colorSpace = CGColorSpaceCreateDeviceRGB();
        CGContextRef context = CGBitmapContextCreate(NULL,
                                                     newWidth,
                                                     newHeight,
                                                     8,
                                                     newWidth * 4,
                                                     colorSpace,
                                                     kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big);
        CGColorSpaceRelease(colorSpace);
        if (!context) {
            CGImageRelease(image);
            *outLen = 0;
            return NULL;
        }

        // Draw image into context to resize
        CGContextSetInterpolationQuality(context, kCGInterpolationHigh);
        CGContextDrawImage(context, CGRectMake(0, 0, newWidth, newHeight), image);
        CGImageRelease(image);

        // Get resized image
        CGImageRef resizedImage = CGBitmapContextCreateImage(context);
        CGContextRelease(context);
        if (!resizedImage) {
            *outLen = 0;
            return NULL;
        }

        NSBitmapImageRep *rep = [[NSBitmapImageRep alloc] initWithCGImage:resizedImage];
        CGImageRelease(resizedImage);

        if (!rep) {
            *outLen = 0;
            return NULL;
        }

        float q = (float)quality / 100.0f;
        if (q < 0.1f) q = 0.1f;
        if (q > 1.0f) q = 1.0f;

        NSDictionary *props = @{NSImageCompressionFactor: @(q)};
        NSData *jpegData = [rep representationUsingType:NSBitmapImageFileTypeJPEG properties:props];

        if (!jpegData || jpegData.length == 0) {
            [rep release];
            *outLen = 0;
            return NULL;
        }

        *outLen = (int)jpegData.length;
        unsigned char *buf = (unsigned char*)malloc(jpegData.length);
        memcpy(buf, jpegData.bytes, jpegData.length);
        [rep release];
        return buf;
    }
}
*/
import "C"
import (
	"encoding/base64"
	"fmt"
	"log"
	"unsafe"
)

// CaptureScreen captures the full desktop on macOS using CGWindowListCreateImage (via dlsym)
// Returns a data:image/jpeg;base64,... string
func CaptureScreen() (string, error) {
	return CaptureScreenQ(50)
}

// CaptureScreenQ chụp desktop và nén JPEG ở mức chất lượng cho trước (kẹp 20..95).
func CaptureScreenQ(q int) (string, error) {
	if q < 20 {
		q = 20
	}
	if q > 95 {
		q = 95
	}
	var outLen C.int
	quality := C.int(q)

	buf := C.CaptureFullDesktop(&outLen, quality)
	if buf == nil || int(outLen) == 0 {
		return "", fmt.Errorf("failed to capture screen: CGWindowListCreateImage returned nil")
	}
	defer C.free(unsafe.Pointer(buf))

	jpegBytes := C.GoBytes(unsafe.Pointer(buf), outLen)

	if len(jpegBytes) < 100 {
		log.Printf("[SCREEN] Warning: captured image is very small (%d bytes), may indicate permission issue", len(jpegBytes))
	}

	encoded := base64.StdEncoding.EncodeToString(jpegBytes)
	return "data:image/jpeg;base64," + encoded, nil
}
