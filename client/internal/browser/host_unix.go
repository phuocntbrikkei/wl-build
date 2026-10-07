//go:build (darwin || linux) && cgo

package browser

/*
#include <stdlib.h>

// Định nghĩa ở host_linux.c (GTK/WebKitGTK) và host_darwin.m (Cocoa/WKWebView).
// Cờ biên dịch: flags_linux.go, flags_darwin.go.
void rk_create_window(const char *dataDir, const char *toolbarHTML, int toolbarHeight);
void rk_run(void);
void rk_dispatch(void);
void rk_navigate(const char *u);
void rk_load_html(const char *h);
void rk_back(void);
void rk_forward(void);
void rk_reload(void);
void rk_stop(void);
void rk_toolbar_eval(const char *js);
void rk_set_title(const char *t);
void rk_focus(void);
void rk_focus_toolbar(void);
void rk_quit(void);
*/
import "C"

import (
	"os"
	"runtime"
	"sync"
	"unsafe"
)

// GTK và Cocoa đều phải chạy trên luồng chính của tiến trình.
func init() { runtime.LockOSThread() }

type nativeHost struct {
	core  *Core
	qmu   sync.Mutex
	queue []func()
}

var lhost *nativeHost

func runHost(c *Core, initial string) error {
	h := &nativeHost{core: c}
	lhost = h
	c.attach(h)
	dir := C.CString(dataDir())
	tb := C.CString(toolbarHTML)
	defer C.free(unsafe.Pointer(dir))
	defer C.free(unsafe.Pointer(tb))
	C.rk_create_window(dir, tb, C.int(toolbarDIP))
	c.start(os.Stdin, initial)
	C.rk_run()
	return nil
}

const toolbarDIP = 46

func withC(s string, f func(*C.char)) {
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	f(cs)
}

func (h *nativeHost) Navigate(u string)       { withC(u, func(s *C.char) { C.rk_navigate(s) }) }
func (h *nativeHost) LoadHTML(s string)       { withC(s, func(p *C.char) { C.rk_load_html(p) }) }
func (h *nativeHost) Back()                   { C.rk_back() }
func (h *nativeHost) Forward()                { C.rk_forward() }
func (h *nativeHost) Reload()                 { C.rk_reload() }
func (h *nativeHost) Stop()                   { C.rk_stop() }
func (h *nativeHost) ToolbarEval(js string)   { withC(js, func(s *C.char) { C.rk_toolbar_eval(s) }) }
func (h *nativeHost) SetWindowTitle(t string) { withC(t, func(s *C.char) { C.rk_set_title(s) }) }
func (h *nativeHost) Focus()                  { C.rk_focus() }
func (h *nativeHost) Quit()                   { C.rk_quit() }

func (h *nativeHost) Dispatch(f func()) {
	h.qmu.Lock()
	h.queue = append(h.queue, f)
	h.qmu.Unlock()
	C.rk_dispatch()
}

//export rkRunQueue
func rkRunQueue() {
	h := lhost
	h.qmu.Lock()
	q := h.queue
	h.queue = nil
	h.qmu.Unlock()
	for _, f := range q {
		f()
	}
}

//export rkDecide
func rkDecide(uri *C.char, mainFrame C.int) C.int {
	if lhost.core.AllowNavigation(C.GoString(uri), mainFrame != 0) {
		return 1
	}
	return 0
}

//export rkNewWindow
func rkNewWindow(uri *C.char) { lhost.core.NewWindow(C.GoString(uri)) }

//export rkCommitted
func rkCommitted(uri *C.char) { lhost.core.Committed(C.GoString(uri)) }

//export rkLoaded
func rkLoaded(uri *C.char) { lhost.core.Loaded(C.GoString(uri)) }

//export rkTitle
func rkTitle(t *C.char) { lhost.core.TitleChanged(C.GoString(t)) }

//export rkHistory
func rkHistory(back, fwd C.int) { lhost.core.HistoryChanged(back != 0, fwd != 0) }

//export rkToolbarMsg
func rkToolbarMsg(m *C.char) { lhost.core.ToolbarMessage(C.GoString(m)) }

//export rkKey
func rkKey(key, ctrl, alt C.int) C.int {
	c := lhost.core
	switch key {
	case 1:
		c.ToolbarMessage(`{"cmd":"reload"}`)
	case 2:
		C.rk_focus_toolbar()
		lhost.ToolbarEval("window.rkFocusAddress&&window.rkFocusAddress()")
	case 3:
		lhost.Back()
	case 4:
		lhost.Forward()
	default:
		return 0
	}
	return 1
}
