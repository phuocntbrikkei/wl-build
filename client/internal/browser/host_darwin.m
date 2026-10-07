// Phần Cocoa/WKWebView của trình duyệt tích hợp (MacOS). Xem host_unix.go.
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

// Hàm Go export (host_unix.go)
extern int  rkDecide(char *uri, int mainFrame);
extern void rkNewWindow(char *uri);
extern void rkCommitted(char *uri);
extern void rkLoaded(char *uri);
extern void rkTitle(char *title);
extern void rkHistory(int back, int fwd);
extern void rkToolbarMsg(char *msg);
extern void rkRunQueue(void);
extern int  rkKey(int key, int ctrl, int alt);

@interface RKDelegate : NSObject <WKNavigationDelegate, WKUIDelegate, WKScriptMessageHandler, NSWindowDelegate>
@end

static NSWindow *rkWin;
static WKWebView *rkToolbar;
static WKWebView *rkContent;
static RKDelegate *rkDel;

static char *rkURL(NSURL *u) { return (char *)((u.absoluteString ?: @"").UTF8String); }

@implementation RKDelegate

- (void)history {
    rkHistory(rkContent.canGoBack ? 1 : 0, rkContent.canGoForward ? 1 : 0);
}

// Mọi lượt điều hướng (khung chính, khung con, chuyển hướng) đi qua đây.
- (void)webView:(WKWebView *)wv decidePolicyForNavigationAction:(WKNavigationAction *)a decisionHandler:(void (^)(WKNavigationActionPolicy))h {
    if (wv != rkContent) { h(WKNavigationActionPolicyAllow); return; }
    if (a.targetFrame == nil) { h(WKNavigationActionPolicyAllow); return; } // cửa sổ mới: xử lý ở createWebView
    int ok = rkDecide(rkURL(a.request.URL), a.targetFrame.isMainFrame ? 1 : 0);
    h(ok ? WKNavigationActionPolicyAllow : WKNavigationActionPolicyCancel);
}

- (void)webView:(WKWebView *)wv didCommitNavigation:(WKNavigation *)n {
    if (wv != rkContent) return;
    rkCommitted(rkURL(wv.URL));
    [self history];
}

- (void)webView:(WKWebView *)wv didFinishNavigation:(WKNavigation *)n {
    if (wv != rkContent) return;
    rkLoaded(rkURL(wv.URL));
    [self history];
}

// target=_blank / window.open: mở ngay trong khung hiện tại để luôn qua cùng một cửa kiểm tra.
- (WKWebView *)webView:(WKWebView *)wv createWebViewWithConfiguration:(WKWebViewConfiguration *)c forNavigationAction:(WKNavigationAction *)a windowFeatures:(WKWindowFeatures *)f {
    rkNewWindow(rkURL(a.request.URL));
    return nil;
}

- (void)userContentController:(WKUserContentController *)ucc didReceiveScriptMessage:(WKScriptMessage *)m {
    if ([m.body isKindOfClass:[NSString class]]) rkToolbarMsg((char *)[(NSString *)m.body UTF8String]);
}

- (void)observeValueForKeyPath:(NSString *)keyPath ofObject:(id)object change:(NSDictionary *)change context:(void *)context {
    if ([keyPath isEqualToString:@"title"]) rkTitle((char *)((rkContent.title ?: @"").UTF8String));
}

- (void)windowWillClose:(NSNotification *)n { [NSApp terminate:nil]; }

- (void)goBack:(id)s { rkKey(3, 0, 0); }
- (void)goForward:(id)s { rkKey(4, 0, 0); }
- (void)reloadPage:(id)s { rkKey(1, 0, 0); }
- (void)focusAddress:(id)s { rkKey(2, 0, 0); }
@end

static NSMenuItem *rkItem(NSString *title, SEL action, NSString *key, id target) {
    NSMenuItem *it = [[NSMenuItem alloc] initWithTitle:title action:action keyEquivalent:key];
    it.target = target;
    return it;
}

// Menu chính: cần có Edit để Cmd+C/V/X/A chạy trong ô nhập và trang web.
static void rkBuildMenu(void) {
    NSMenu *bar = [[NSMenu alloc] init];

    NSMenuItem *appItem = [[NSMenuItem alloc] init];
    NSMenu *app = [[NSMenu alloc] initWithTitle:@"Trình duyệt"];
    [app addItem:rkItem(@"Đóng trình duyệt", @selector(terminate:), @"q", nil)];
    appItem.submenu = app;
    [bar addItem:appItem];

    NSMenuItem *editItem = [[NSMenuItem alloc] init];
    NSMenu *edit = [[NSMenu alloc] initWithTitle:@"Sửa"];
    [edit addItem:rkItem(@"Hoàn tác", @selector(undo:), @"z", nil)];
    NSMenuItem *redo = rkItem(@"Làm lại", @selector(redo:), @"z", nil);
    redo.keyEquivalentModifierMask = NSEventModifierFlagCommand | NSEventModifierFlagShift;
    [edit addItem:redo];
    [edit addItem:[NSMenuItem separatorItem]];
    [edit addItem:rkItem(@"Cắt", @selector(cut:), @"x", nil)];
    [edit addItem:rkItem(@"Sao chép", @selector(copy:), @"c", nil)];
    [edit addItem:rkItem(@"Dán", @selector(paste:), @"v", nil)];
    [edit addItem:rkItem(@"Chọn tất cả", @selector(selectAll:), @"a", nil)];
    editItem.submenu = edit;
    [bar addItem:editItem];

    NSMenuItem *viewItem = [[NSMenuItem alloc] init];
    NSMenu *view = [[NSMenu alloc] initWithTitle:@"Xem"];
    [view addItem:rkItem(@"Quay lại", @selector(goBack:), @"[", rkDel)];
    [view addItem:rkItem(@"Tới", @selector(goForward:), @"]", rkDel)];
    [view addItem:rkItem(@"Tải lại", @selector(reloadPage:), @"r", rkDel)];
    [view addItem:rkItem(@"Ô địa chỉ", @selector(focusAddress:), @"l", rkDel)];
    viewItem.submenu = view;
    [bar addItem:viewItem];

    NSApp.mainMenu = bar;
}

void rk_create_window(const char *dataDir, const char *toolbarHTML, int toolbarHeight) {
    (void)dataDir; // WKWebView dùng kho dữ liệu mặc định của ứng dụng
    @autoreleasepool {
        [NSApplication sharedApplication];
        [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
        rkDel = [[RKDelegate alloc] init];
        rkBuildMenu();

        NSRect screen = NSScreen.mainScreen.visibleFrame;
        CGFloat w = MIN(1200, screen.size.width - 40), hgt = MIN(800, screen.size.height - 40);
        NSRect frame = NSMakeRect(0, 0, w, hgt);
        rkWin = [[NSWindow alloc] initWithContentRect:frame
                                            styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable
                                              backing:NSBackingStoreBuffered
                                                defer:NO];
        rkWin.title = @"Trình duyệt — Rikkei LMS Connect";
        rkWin.releasedWhenClosed = NO;
        rkWin.delegate = rkDel;
        [rkWin center];

        NSView *root = rkWin.contentView;
        NSRect b = root.bounds;

        WKWebViewConfiguration *tc = [[WKWebViewConfiguration alloc] init];
        [tc.userContentController addScriptMessageHandler:rkDel name:@"rk"];
        rkToolbar = [[WKWebView alloc] initWithFrame:NSMakeRect(0, b.size.height - toolbarHeight, b.size.width, toolbarHeight) configuration:tc];
        rkToolbar.autoresizingMask = NSViewWidthSizable | NSViewMinYMargin;
        rkToolbar.navigationDelegate = rkDel;
        [root addSubview:rkToolbar];

        WKWebViewConfiguration *cc = [[WKWebViewConfiguration alloc] init];
        rkContent = [[WKWebView alloc] initWithFrame:NSMakeRect(0, 0, b.size.width, b.size.height - toolbarHeight) configuration:cc];
        rkContent.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
        rkContent.navigationDelegate = rkDel;
        rkContent.UIDelegate = rkDel;
        rkContent.allowsBackForwardNavigationGestures = YES;
        [rkContent addObserver:rkDel forKeyPath:@"title" options:NSKeyValueObservingOptionNew context:NULL];
        [root addSubview:rkContent];

        [rkToolbar loadHTMLString:[NSString stringWithUTF8String:toolbarHTML] baseURL:nil];
        [rkWin makeKeyAndOrderFront:nil];
        [rkWin makeFirstResponder:rkContent];
        [NSApp activateIgnoringOtherApps:YES];
    }
}

void rk_run(void) { [NSApp run]; }

void rk_dispatch(void) {
    dispatch_async(dispatch_get_main_queue(), ^{ rkRunQueue(); });
}

void rk_navigate(const char *u) {
    NSURL *url = [NSURL URLWithString:[NSString stringWithUTF8String:u]];
    if (url) [rkContent loadRequest:[NSURLRequest requestWithURL:url]];
}

void rk_load_html(const char *h) { [rkContent loadHTMLString:[NSString stringWithUTF8String:h] baseURL:nil]; }
void rk_back(void) { [rkContent goBack]; }
void rk_forward(void) { [rkContent goForward]; }
void rk_reload(void) { [rkContent reload]; }
void rk_stop(void) { [rkContent stopLoading]; }
void rk_toolbar_eval(const char *js) { [rkToolbar evaluateJavaScript:[NSString stringWithUTF8String:js] completionHandler:nil]; }
void rk_set_title(const char *t) { rkWin.title = [NSString stringWithUTF8String:t]; }
void rk_focus(void) {
    if (rkWin.miniaturized) [rkWin deminiaturize:nil];
    [rkWin makeKeyAndOrderFront:nil];
    [NSApp activateIgnoringOtherApps:YES];
}
void rk_focus_toolbar(void) { [rkWin makeFirstResponder:rkToolbar]; }
void rk_quit(void) { [NSApp terminate:nil]; }
