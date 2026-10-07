package main

import (
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"client/internal/browser"
	"client/internal/netproxy"
	"client/internal/screen"
)

// Trình duyệt tích hợp: Client giữ chính sách hiện hành (giờ học / giờ thi / ngoài
// giờ) và đẩy xuống tiến trình trình duyệt; các lượt mở trang (URL đầy đủ) đi chung
// đường báo "URL đã truy cập" với proxy.

// browserConfig là phần cấu hình trình duyệt SC trả kèm danh sách ứng dụng.
type browserConfig struct {
	Allow       []string `json:"browserAllow"`
	AlwaysHosts []string `json:"browserAlwaysHosts"`
	HomeURL     string   `json:"browserHome"`
}

func (a *App) browserLauncher() *browser.Launcher {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.browser == nil {
		a.browser = browser.NewLauncher(a.handleBrowserVisit)
	}
	return a.browser
}

// OpenBrowser mở trình duyệt tích hợp (gọi từ giao diện). url rỗng = trang đầu.
func (a *App) OpenBrowser(url string) error {
	a.ensureBrowserPolicy()
	if err := a.browserLauncher().Open(strings.TrimSpace(url)); err != nil {
		log.Printf("[BROWSER] open failed: %v", err)
		return fmt.Errorf("không mở được trình duyệt: %v", err)
	}
	return nil
}

// applyBrowserPolicy: gọi mỗi lần lấy cấu hình lớp xong. mode: free | learning | exam.
func (a *App) applyBrowserPolicy(mode string, cfg browserConfig) {
	label := ""
	a.mu.Lock()
	switch mode {
	case browser.ModeExam:
		if a.dashboard.Exam != nil && a.dashboard.Exam.ExamName != "" {
			label = "Giờ thi · " + a.dashboard.Exam.ExamName
		} else {
			label = "Giờ thi"
		}
	case browser.ModeLearning:
		label = "Giờ học"
		if a.dashboard.CurrentCourseName != "" {
			label += " · " + a.dashboard.CurrentCourseName
		}
	}
	a.browserPolicySet = true
	a.mu.Unlock()
	always := cfg.AlwaysHosts
	if len(always) == 0 {
		always = browser.DefaultAlwaysHosts
	}
	a.browserLauncher().SetPolicy(browser.Policy{
		Mode:        mode,
		Label:       label,
		Allow:       cfg.Allow,
		AlwaysHosts: always,
		HomeURL:     cfg.HomeURL,
	})
}

// ensureBrowserPolicy: mở trình duyệt khi chưa lấy được cấu hình lớp. Nếu Client
// biết đang trong giờ học/thi thì khóa an toàn (chỉ tên miền hệ thống), còn lại
// coi như ngoài giờ.
func (a *App) ensureBrowserPolicy() {
	a.mu.Lock()
	set := a.browserPolicySet
	mode := a.dashboard.MonitorMode
	a.mu.Unlock()
	if set {
		return
	}
	if mode == browser.ModeExam || mode == browser.ModeLearning {
		a.browserLauncher().SetPolicy(browser.LockedPolicy(browser.DefaultAlwaysHosts))
		return
	}
	a.browserLauncher().SetPolicy(browser.Policy{Mode: browser.ModeFree, AlwaysHosts: browser.DefaultAlwaysHosts})
}

// handleBrowserVisit nhận một lượt mở trang từ trình duyệt.
func (a *App) handleBrowserVisit(v browser.Visit) {
	host := ""
	if u, err := url.Parse(v.URL); err == nil {
		host = strings.ToLower(u.Hostname())
	}
	at, err := time.Parse(time.RFC3339, v.At)
	if err != nil {
		at = time.Now()
	}
	capturedURLsMu.Lock()
	capturedURLsBuf = append(capturedURLsBuf, netproxy.CapturedURL{Method: "BROWSER", Host: host, URL: v.URL, Time: at, Blocked: v.Blocked})
	overflow := len(capturedURLsBuf) > 500
	capturedURLsMu.Unlock()
	if overflow {
		go a.flushCapturedURLs()
	}
	if v.Blocked && (v.Mode == browser.ModeLearning || v.Mode == browser.ModeExam) {
		a.reportBrowserBlocked(host, v.URL)
	}
}

// reportBrowserBlocked ghi vi phạm khi sinh viên cố mở trang không được phép
// trong giờ học/thi (1 lần/30 giây/tên miền, kèm ảnh màn hình).
func (a *App) reportBrowserBlocked(host, rawURL string) {
	a.mu.Lock()
	mode := a.dashboard.MonitorMode
	a.mu.Unlock()
	if !shouldRecordViolation(mode) {
		return
	}
	key := "browser:" + host
	if v, ok := blockedSiteViolationLast.Load(key); ok && time.Since(v.(time.Time)) < 30*time.Second {
		return
	}
	blockedSiteViolationLast.Store(key, time.Now())
	shot, err := screen.CaptureScreen()
	if err != nil {
		shot = ""
	}
	a.reportViolationWithScreenshot("browser_blocked", "Mở trang không được phép trong trình duyệt (đã chặn): "+rawURL, shot)
}
