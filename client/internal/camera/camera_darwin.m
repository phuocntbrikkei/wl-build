#import <AVFoundation/AVFoundation.h>
#import <Foundation/Foundation.h>
#import <CoreImage/CoreImage.h>
#import <AppKit/AppKit.h>
#include <stdlib.h>
#include <string.h>

// ── Shared state ──────────────────────────────────────────────
static AVCaptureSession       *captureSession  = nil;
static AVCaptureVideoDataOutput *videoOutput   = nil;
static dispatch_queue_t        captureQueue    = nil;
static CIContext              *sharedCIContext = nil;

// Latest frame stored as JPEG base64 (owning reference)
static NSString *latestFrameBase64 = nil;
static NSLock   *frameLock         = nil;
static int       frameCount        = 0;

// ── Delegate that receives sample buffers ─────────────────────
@interface CameraFrameDelegate : NSObject <AVCaptureVideoDataOutputSampleBufferDelegate>
@end

@implementation CameraFrameDelegate

- (void)captureOutput:(AVCaptureOutput *)output
    didOutputSampleBuffer:(CMSampleBufferRef)sampleBuffer
           fromConnection:(AVCaptureConnection *)connection {
    @autoreleasepool {
        CVImageBufferRef imageBuffer = CMSampleBufferGetImageBuffer(sampleBuffer);
        if (!imageBuffer) {
            NSLog(@"[CAMERA] didOutputSampleBuffer: imageBuffer is NULL");
            return;
        }

        CIImage *ciImage = [CIImage imageWithCVPixelBuffer:imageBuffer];
        if (!sharedCIContext) {
            return;
        }
        CGImageRef cgImage = [sharedCIContext createCGImage:ciImage fromRect:ciImage.extent];
        if (!cgImage) {
            NSLog(@"[CAMERA] didOutputSampleBuffer: cgImage is NULL");
            return;
        }

        // Convert to JPEG NSData  (quality ≈ 0.4)
        NSBitmapImageRep *rep = [[NSBitmapImageRep alloc] initWithCGImage:cgImage];
        CGImageRelease(cgImage);
        if (!rep) {
            return;
        }

        NSDictionary *props = @{NSImageCompressionFactor: @(0.4)};
        NSData *jpegData = [rep representationUsingType:NSBitmapImageFileTypeJPEG properties:props];
        if (!jpegData) {
            NSLog(@"[CAMERA] didOutputSampleBuffer: jpegData is NULL");
            [rep release];
            return;
        }

        NSString *b64 = [jpegData base64EncodedStringWithOptions:0];
        // Create a retained string (ownership transfer)
        NSString *dataUrl = [[NSString alloc] initWithFormat:@"data:image/jpeg;base64,%@", b64];
        [rep release];

        [frameLock lock];
        if (latestFrameBase64) {
            [latestFrameBase64 release];
        }
        latestFrameBase64 = dataUrl; // retained copy
        frameCount++;
        int fc = frameCount;
        [frameLock unlock];

        // Log first few frames to confirm camera is working
        if (fc <= 3) {
            NSLog(@"[CAMERA] Frame #%d captured, size=%lu bytes", fc, (unsigned long)jpegData.length);
        }
    }
}

@end

static CameraFrameDelegate *frameDelegate = nil;

// ── C-exported functions ──────────────────────────────────────

// StartNativeCamera: returns 0 on success, -1 on failure
int StartNativeCamera(void) {
    NSLog(@"[CAMERA] StartNativeCamera called");

    if (captureSession && captureSession.isRunning) {
        NSLog(@"[CAMERA] Session already running");
        return 0; // already running
    }

    if (!frameLock) {
        frameLock = [[NSLock alloc] init];
    }
    frameCount = 0;

    // Check camera permission
    if (@available(macOS 10.14, *)) {
        AVAuthorizationStatus status = [AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeVideo];
        NSLog(@"[CAMERA] Camera permission status: %ld (0=NotDetermined, 1=Restricted, 2=Denied, 3=Authorized)", (long)status);

        if (status == AVAuthorizationStatusDenied || status == AVAuthorizationStatusRestricted) {
            NSLog(@"[CAMERA] Camera permission denied (status=%ld)", (long)status);
            return -1;
        }
        if (status == AVAuthorizationStatusNotDetermined) {
            NSLog(@"[CAMERA] Requesting camera permission...");
            dispatch_semaphore_t sem = dispatch_semaphore_create(0);
            __block BOOL granted = NO;
            [AVCaptureDevice requestAccessForMediaType:AVMediaTypeVideo completionHandler:^(BOOL g) {
                granted = g;
                NSLog(@"[CAMERA] Permission request result: %@", g ? @"GRANTED" : @"DENIED");
                dispatch_semaphore_signal(sem);
            }];
            dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 10 * NSEC_PER_SEC));
            if (!granted) {
                NSLog(@"[CAMERA] Camera permission not granted after request");
                return -1;
            }
        }
    }

    if (!sharedCIContext) {
        sharedCIContext = [[CIContext contextWithOptions:nil] retain];
    }

    captureSession = [[AVCaptureSession alloc] init];
    captureSession.sessionPreset = AVCaptureSessionPresetLow; // 320×240-ish
    NSLog(@"[CAMERA] Session created with preset Low");

    // Find default video device
    AVCaptureDevice *camera = [AVCaptureDevice defaultDeviceWithMediaType:AVMediaTypeVideo];
    if (!camera) {
        NSLog(@"[CAMERA] No camera device found");
        captureSession = nil;
        return -1;
    }
    NSLog(@"[CAMERA] Found camera: %@", camera.localizedName);

    NSError *error = nil;
    AVCaptureDeviceInput *input = [AVCaptureDeviceInput deviceInputWithDevice:camera error:&error];
    if (error || !input) {
        NSLog(@"[CAMERA] Cannot create camera input: %@", error);
        captureSession = nil;
        return -1;
    }

    if ([captureSession canAddInput:input]) {
        [captureSession addInput:input];
        NSLog(@"[CAMERA] Input added to session");
    } else {
        NSLog(@"[CAMERA] Cannot add camera input to session");
        captureSession = nil;
        return -1;
    }

    // Video data output
    videoOutput = [[AVCaptureVideoDataOutput alloc] init];
    videoOutput.videoSettings = @{
        (NSString *)kCVPixelBufferPixelFormatTypeKey: @(kCVPixelFormatType_32BGRA)
    };
    videoOutput.alwaysDiscardsLateVideoFrames = YES;

    captureQueue = dispatch_queue_create("com.simplecare.camera", DISPATCH_QUEUE_SERIAL);
    frameDelegate = [[CameraFrameDelegate alloc] init];
    [videoOutput setSampleBufferDelegate:frameDelegate queue:captureQueue];

    if ([captureSession canAddOutput:videoOutput]) {
        [captureSession addOutput:videoOutput];
        NSLog(@"[CAMERA] Output added to session");
    } else {
        NSLog(@"[CAMERA] Cannot add video output to session");
        captureSession = nil;
        return -1;
    }

    [captureSession startRunning];
    NSLog(@"[CAMERA] Session startRunning called, isRunning=%d", captureSession.isRunning);
    return 0;
}

void StopNativeCamera(void) {
    NSLog(@"[CAMERA] StopNativeCamera called");
    if (captureSession && captureSession.isRunning) {
        [captureSession stopRunning];
        NSLog(@"[CAMERA] Session stopped");
    }
    captureSession = nil;
    videoOutput = nil;
    frameDelegate = nil;

    [frameLock lock];
    if (latestFrameBase64) {
        [latestFrameBase64 release];
        latestFrameBase64 = nil;
    }
    [frameLock unlock];
}

// GetLatestCameraFrame: returns a C-string (caller must free) or NULL
char* GetLatestCameraFrame(void) {
    [frameLock lock];
    NSString *frame = latestFrameBase64;
    latestFrameBase64 = nil; // transfers ownership to caller
    [frameLock unlock];

    if (!frame) {
        return NULL;
    }
    char *res = strdup([frame UTF8String]);
    [frame release]; // Release since we had ownership
    return res;
}
