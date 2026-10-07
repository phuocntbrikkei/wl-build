#import <CoreWLAN/CoreWLAN.h>
#import <CoreLocation/CoreLocation.h>
#import <AVFoundation/AVFoundation.h>
#import <CoreGraphics/CoreGraphics.h>
#import <Foundation/Foundation.h>

static CLLocationManager *locationManager = nil;

void RequestLocationPermission() {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (locationManager == nil) {
            locationManager = [[CLLocationManager alloc] init];
        }
        if (@available(macOS 10.15, *)) {
            [locationManager requestWhenInUseAuthorization];
        }
    });
}

void RequestCameraAndMicPermission() {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (@available(macOS 10.14, *)) {
            // Request Camera
            AVAuthorizationStatus cameraStatus = [AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeVideo];
            if (cameraStatus == AVAuthorizationStatusNotDetermined) {
                [AVCaptureDevice requestAccessForMediaType:AVMediaTypeVideo completionHandler:^(BOOL granted) {
                    // Camera permission requested
                }];
            }
            
            // Request Microphone
            AVAuthorizationStatus micStatus = [AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeAudio];
            if (micStatus == AVAuthorizationStatusNotDetermined) {
                [AVCaptureDevice requestAccessForMediaType:AVMediaTypeAudio completionHandler:^(BOOL granted) {
                    // Mic permission requested
                }];
            }
        }
    });
}

void RequestScreenCapturePermission() {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (@available(macOS 11.0, *)) {
            BOOL hasAccess = CGPreflightScreenCaptureAccess();
            if (!hasAccess) {
                CGRequestScreenCaptureAccess();
            }
        }
    });
}

int GetCameraPermissionStatus() {
    if (@available(macOS 10.14, *)) {
        return (int)[AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeVideo];
    }
    return -1;
}

int GetMicrophonePermissionStatus() {
    if (@available(macOS 10.14, *)) {
        return (int)[AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeAudio];
    }
    return -1;
}

int GetScreenCapturePermissionStatus() {
    if (@available(macOS 11.0, *)) {
        return CGPreflightScreenCaptureAccess() ? 1 : 0;
    }
    return -1;
}

typedef struct {
    char* ssid;
    char* bssid;
} CWifiInfo;

CWifiInfo GetCurrentWifiInfo() {
    CWifiInfo info;
    info.ssid = NULL;
    info.bssid = NULL;

    @autoreleasepool {
        CWWiFiClient *client = [[CWWiFiClient alloc] init];
        if (client != nil) {
            CWInterface *interface = [client interface];
            if (interface != nil) {
                NSString *ssidStr = [interface ssid];
                NSString *bssidStr = [interface bssid];
                if (ssidStr != nil) {
                    info.ssid = strdup([ssidStr UTF8String]);
                }
                if (bssidStr != nil) {
                    info.bssid = strdup([bssidStr UTF8String]);
                }
            }
        }
    }
    return info;
}
