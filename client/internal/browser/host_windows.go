//go:build windows

package browser

import (
	"errors"
	"io"
	"log"
	"os"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/wailsapp/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

// Trình duyệt trên Windows: một cửa sổ Win32 chứa 2 WebView2 — thanh công cụ ở
// trên (HTML nhúng) và trang web bên dưới. Các sự kiện điều hướng mà go-webview2
// chưa bọc (NavigationStarting, FrameNavigationStarting, NewWindowRequested,
// SourceChanged, HistoryChanged, DocumentTitleChanged) được đăng ký trực tiếp qua
// bảng hàm COM của ICoreWebView2.

const toolbarDIP = 46

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")

	pRegisterClassExW              = user32.NewProc("RegisterClassExW")
	pCreateWindowExW               = user32.NewProc("CreateWindowExW")
	pDefWindowProcW                = user32.NewProc("DefWindowProcW")
	pGetMessageW                   = user32.NewProc("GetMessageW")
	pTranslateMessage              = user32.NewProc("TranslateMessage")
	pDispatchMessageW              = user32.NewProc("DispatchMessageW")
	pPostMessageW                  = user32.NewProc("PostMessageW")
	pPostQuitMessage               = user32.NewProc("PostQuitMessage")
	pShowWindow                    = user32.NewProc("ShowWindow")
	pUpdateWindow                  = user32.NewProc("UpdateWindow")
	pGetClientRect                 = user32.NewProc("GetClientRect")
	pSetWindowTextW                = user32.NewProc("SetWindowTextW")
	pDestroyWindow                 = user32.NewProc("DestroyWindow")
	pLoadCursorW                   = user32.NewProc("LoadCursorW")
	pGetDpiForWindow               = user32.NewProc("GetDpiForWindow")
	pSetForegroundWindow           = user32.NewProc("SetForegroundWindow")
	pIsIconic                      = user32.NewProc("IsIconic")
	pGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	pSetWindowPos                  = user32.NewProc("SetWindowPos")
	pGetKeyState                   = user32.NewProc("GetKeyState")
	pSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	pGetModuleHandleW              = kernel32.NewProc("GetModuleHandleW")
	pExtractIconW                  = shell32.NewProc("ExtractIconW")
)

const (
	wmDestroy     = 0x0002
	wmSize        = 0x0005
	wmSetFocus    = 0x0007
	wmClose       = 0x0010
	wmMove        = 0x0003
	wmDpiChanged  = 0x02E0
	wmApp         = 0x8000
	wmRunQueue    = wmApp + 1
	swShow        = 5
	swRestore     = 9
	wsOverlapped  = 0x00CF0000 // WS_OVERLAPPEDWINDOW
	cwUseDefault  = 0x80000000
	idcArrow      = 32512
	vkControl     = 0x11
	vkMenu        = 0x12
	vkF5          = 0x74
	vkF6          = 0x75
	vkL           = 0x4C
	vkLeft        = 0x25
	vkRight       = 0x27
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type rect struct{ Left, Top, Right, Bottom int32 }

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

type winHost struct {
	core  *Core
	hwnd  uintptr
	tb    *edge.Chromium
	ct    *edge.Chromium
	ctWV  uintptr // ICoreWebView2 của khung nội dung
	ready bool

	qmu   sync.Mutex
	queue []func()
}

var host *winHost // một cửa sổ mỗi tiến trình

func runHost(c *Core, initial string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	_ = windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED)
	if pSetProcessDpiAwarenessContext.Find() == nil {
		pSetProcessDpiAwarenessContext.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)
	}

	h := &winHost{core: c}
	host = h
	c.attach(h)
	if err := h.createWindow(); err != nil {
		return err
	}

	dir := dataDir()
	h.tb = edge.NewChromium()
	h.tb.DataPath = dir
	h.ct = edge.NewChromium()
	h.ct.DataPath = dir
	h.tb.SetErrorCallback(func(err error) { log.Printf("[BROWSER] toolbar webview: %v", err) })
	h.ct.SetErrorCallback(func(err error) { log.Printf("[BROWSER] content webview: %v", err) })

	if !h.tb.Embed(h.hwnd) || !h.ct.Embed(h.hwnd) {
		return errors.New("không khởi tạo được WebView2")
	}
	h.setupToolbar()
	if err := h.setupContent(); err != nil {
		return err
	}
	h.ready = true
	h.layout()
	h.ct.Focus()

	c.start(stdinReader(), initial)

	var m msg
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	return nil
}

func stdinReader() io.Reader { return os.Stdin }

func (h *winHost) createWindow() error {
	inst, _, _ := pGetModuleHandleW.Call(0)
	className, _ := windows.UTF16PtrFromString("RikkeiLmsConnectBrowser")
	cursor, _, _ := pLoadCursorW.Call(0, idcArrow)
	var icon uintptr
	if exe, err := os.Executable(); err == nil {
		p, _ := windows.UTF16PtrFromString(exe)
		icon, _, _ = pExtractIconW.Call(inst, uintptr(unsafe.Pointer(p)), 0)
		if icon <= 1 {
			icon = 0
		}
	}
	wc := wndClassEx{
		WndProc:   windows.NewCallback(wndProc),
		Instance:  inst,
		Cursor:    cursor,
		Icon:      icon,
		IconSm:    icon,
		ClassName: className,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return err
	}
	title, _ := windows.UTF16PtrFromString("Trình duyệt — Rikkei LMS Connect")

	// Mở 1200x800 (theo DPI màn hình chính), căn giữa.
	sw, _, _ := pGetSystemMetrics.Call(0)
	sh, _, _ := pGetSystemMetrics.Call(1)
	w, hgt := int32(1200), int32(800)
	if int32(sw) < w+40 {
		w = int32(sw) - 40
	}
	if int32(sh) < hgt+80 {
		hgt = int32(sh) - 80
	}
	x, y := (int32(sw)-w)/2, (int32(sh)-hgt)/2

	hwnd, _, err := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		wsOverlapped, uintptr(x), uintptr(y), uintptr(w), uintptr(hgt), 0, 0, inst, 0)
	if hwnd == 0 {
		return err
	}
	h.hwnd = hwnd
	pShowWindow.Call(hwnd, swShow)
	pUpdateWindow.Call(hwnd)
	pSetForegroundWindow.Call(hwnd)
	return nil
}

func wndProc(hwnd, m, wp, lp uintptr) uintptr {
	h := host
	switch uint32(m) {
	case wmSize:
		if h != nil && h.ready {
			h.layout()
		}
		return 0
	case wmMove:
		if h != nil && h.ready {
			_ = h.ct.NotifyParentWindowPositionChanged()
			_ = h.tb.NotifyParentWindowPositionChanged()
		}
	case wmDpiChanged:
		r := (*rect)(unsafe.Pointer(lp))
		pSetWindowPos.Call(hwnd, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), swpNoZOrder|swpNoActivate)
		return 0
	case wmSetFocus:
		if h != nil && h.ready {
			h.ct.Focus()
		}
	case wmRunQueue:
		if h != nil {
			h.runQueue()
		}
		return 0
	case wmClose:
		pDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, m, wp, lp)
	return r
}

func (h *winHost) toolbarHeight() int32 {
	dpi := uintptr(96)
	if pGetDpiForWindow.Find() == nil {
		if d, _, _ := pGetDpiForWindow.Call(h.hwnd); d != 0 {
			dpi = d
		}
	}
	return int32(toolbarDIP * dpi / 96)
}

func (h *winHost) layout() {
	var r rect
	pGetClientRect.Call(h.hwnd, uintptr(unsafe.Pointer(&r)))
	tbh := h.toolbarHeight()
	h.tb.ResizeWithBounds(&edge.Rect{Left: 0, Top: 0, Right: r.Right, Bottom: tbh})
	h.ct.ResizeWithBounds(&edge.Rect{Left: 0, Top: tbh, Right: r.Right, Bottom: r.Bottom})
}

func (h *winHost) setupToolbar() {
	if s, err := h.tb.GetSettings(); err == nil {
		_ = s.PutAreDevToolsEnabled(false)
		_ = s.PutAreDefaultContextMenusEnabled(false)
		_ = s.PutIsZoomControlEnabled(false)
		_ = s.PutIsStatusBarEnabled(false)
		_ = s.PutAreBrowserAcceleratorKeysEnabled(false)
	}
	h.tb.MessageCallback = func(message string, _ *edge.ICoreWebView2, _ *edge.ICoreWebView2WebMessageReceivedEventArgs) {
		h.core.ToolbarMessage(message)
	}
	h.tb.AcceleratorKeyCallback = h.accelerator
	h.tb.NavigateToString(toolbarHTML)
}

func (h *winHost) setupContent() error {
	if s, err := h.ct.GetSettings(); err == nil {
		_ = s.PutAreDevToolsEnabled(false) // không F12 / "Kiểm tra phần tử"
		_ = s.PutAreDefaultContextMenusEnabled(true)
		_ = s.PutIsStatusBarEnabled(true)
		_ = s.PutAreHostObjectsAllowed(false)
		_ = s.PutIsWebMessageEnabled(false)
	}
	h.ct.AcceleratorKeyCallback = h.accelerator
	h.ct.NavigationCompletedCallback = func(sender *edge.ICoreWebView2, _ *edge.ICoreWebView2NavigationCompletedEventArgs) {
		if src, err := sender.GetSource(); err == nil {
			h.core.Loaded(src)
		}
	}

	wv, err := h.ct.GetController().GetCoreWebView2()
	if err != nil {
		return err
	}
	h.ctWV = uintptr(unsafe.Pointer(wv))

	// NavigationStarting (khung chính) và FrameNavigationStarting (khung con) dùng chung kiểu tham số.
	navStart := func(main bool) func(sender, args uintptr) uintptr {
		return func(_, args uintptr) uintptr {
			uri := comString(args, 3) // get_Uri
			if !h.core.AllowNavigation(uri, main) {
				vcall(args, 8, 1) // put_Cancel(TRUE)
			}
			return 0
		}
	}
	h.addEvent(7, navStart(true))                  // add_NavigationStarting
	h.addEvent(17, navStart(false))                // add_FrameNavigationStarting
	h.addEvent(44, func(_, args uintptr) uintptr { // add_NewWindowRequested
		uri := comString(args, 3)
		vcall(args, 6, 1) // put_Handled(TRUE): không tạo cửa sổ riêng
		h.core.NewWindow(uri)
		return 0
	})
	h.addEvent(11, func(_, _ uintptr) uintptr { // add_SourceChanged
		h.core.Committed(comString(h.ctWV, 4)) // get_Source
		return 0
	})
	h.addEvent(13, func(_, _ uintptr) uintptr { // add_HistoryChanged
		var back, fwd int32
		vcall(h.ctWV, 38, uintptr(unsafe.Pointer(&back)))
		vcall(h.ctWV, 39, uintptr(unsafe.Pointer(&fwd)))
		h.core.HistoryChanged(back != 0, fwd != 0)
		return 0
	})
	h.addEvent(46, func(_, _ uintptr) uintptr { // add_DocumentTitleChanged
		h.core.TitleChanged(comString(h.ctWV, 48)) // get_DocumentTitle
		return 0
	})
	return nil
}

// accelerator: phím tắt chung cho cả hai khung.
func (h *winHost) accelerator(vk uint) bool {
	ctrl := keyDown(vkControl)
	alt := keyDown(vkMenu)
	switch {
	case vk == vkF6, ctrl && vk == vkL:
		h.tb.Focus()
		h.tb.Eval("window.rkFocusAddress&&window.rkFocusAddress()")
		return true
	case vk == vkF5:
		h.core.ToolbarMessage(`{"cmd":"reload"}`)
		return true
	case alt && vk == vkLeft:
		h.Back()
		return true
	case alt && vk == vkRight:
		h.Forward()
		return true
	}
	return false
}

func keyDown(vk uintptr) bool {
	r, _, _ := pGetKeyState.Call(vk)
	return int16(r) < 0
}

// ---------------------------------------------------------------- engine

func (h *winHost) Navigate(u string) { h.ct.Navigate(u) }
func (h *winHost) LoadHTML(s string) { h.ct.NavigateToString(s) }
func (h *winHost) Back()             { vcall(h.ctWV, 40) }
func (h *winHost) Forward()          { vcall(h.ctWV, 41) }
func (h *winHost) Reload()           { vcall(h.ctWV, 31) }
func (h *winHost) Stop()             { vcall(h.ctWV, 43) }
func (h *winHost) ToolbarEval(js string) {
	if h.tb != nil {
		h.tb.Eval(js)
	}
}

func (h *winHost) SetWindowTitle(t string) {
	p, _ := windows.UTF16PtrFromString(t)
	pSetWindowTextW.Call(h.hwnd, uintptr(unsafe.Pointer(p)))
}

func (h *winHost) Focus() {
	if r, _, _ := pIsIconic.Call(h.hwnd); r != 0 {
		pShowWindow.Call(h.hwnd, swRestore)
	}
	pSetForegroundWindow.Call(h.hwnd)
}

func (h *winHost) Quit() { pPostMessageW.Call(h.hwnd, wmClose, 0, 0) }

func (h *winHost) Dispatch(f func()) {
	h.qmu.Lock()
	h.queue = append(h.queue, f)
	h.qmu.Unlock()
	pPostMessageW.Call(h.hwnd, wmRunQueue, 0, 0)
}

func (h *winHost) runQueue() {
	h.qmu.Lock()
	q := h.queue
	h.queue = nil
	h.qmu.Unlock()
	for _, f := range q {
		f()
	}
}

// ---------------------------------------------------------------- COM

// comHandler là một đối tượng COM tối thiểu cài I...EventHandler (Invoke(sender, args)).
type comHandler struct {
	vtbl   *comHandlerVtbl
	invoke func(sender, args uintptr) uintptr
}

type comHandlerVtbl struct {
	QueryInterface, AddRef, Release, Invoke uintptr
}

var (
	handlerVtbl = &comHandlerVtbl{
		QueryInterface: windows.NewCallback(func(this, _, out uintptr) uintptr {
			*(*uintptr)(unsafe.Pointer(out)) = this
			return 0
		}),
		AddRef:  windows.NewCallback(func(uintptr) uintptr { return 1 }),
		Release: windows.NewCallback(func(uintptr) uintptr { return 1 }),
		Invoke: windows.NewCallback(func(this, sender, args uintptr) uintptr {
			return (*comHandler)(unsafe.Pointer(this)).invoke(sender, args)
		}),
	}
	liveHandlers []*comHandler // giữ tham chiếu để GC không thu hồi
)

func (h *winHost) addEvent(vtblIndex int, f func(sender, args uintptr) uintptr) {
	ch := &comHandler{vtbl: handlerVtbl, invoke: f}
	liveHandlers = append(liveHandlers, ch)
	var token int64
	if hr := vcall(h.ctWV, vtblIndex, uintptr(unsafe.Pointer(ch)), uintptr(unsafe.Pointer(&token))); int32(hr) < 0 {
		log.Printf("[BROWSER] add event #%d failed: %08x", vtblIndex, hr)
	}
}

// vcall gọi hàm thứ idx trong bảng hàm COM của obj.
func vcall(obj uintptr, idx int, args ...uintptr) uintptr {
	if obj == 0 {
		return 0x80004003 // E_POINTER
	}
	vt := *(*uintptr)(unsafe.Pointer(obj))
	fn := *(*uintptr)(unsafe.Pointer(vt + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

// comString gọi một getter trả LPWSTR (CoTaskMemAlloc) rồi giải phóng.
func comString(obj uintptr, idx int) string {
	var p *uint16
	if hr := vcall(obj, idx, uintptr(unsafe.Pointer(&p))); int32(hr) < 0 || p == nil {
		return ""
	}
	s := windows.UTF16PtrToString(p)
	windows.CoTaskMemFree(unsafe.Pointer(p))
	return s
}
