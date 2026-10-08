package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Số phiên bản Client: 1 nguồn duy nhất là file VERSION (make-wl-build.ps1 -Version
// ghi file này + wails.json). Gửi kèm mọi lần gọi SC chính (X-App-Version) để SC chặn
// bản dưới mức tối thiểu; Client tự hỏi SC /api/public/app-version để chặn giao diện.

//go:embed VERSION
var versionFile string

var AppVersion = strings.TrimSpace(versionFile)

func appPlatform() string {
	switch goruntime.GOOS {
	case "darwin":
		return "mac"
	case "windows":
		return "windows"
	}
	return "linux"
}

// versionStatus là kết quả kiểm tra phiên bản gần nhất.
type versionStatus struct {
	Current     string `json:"current"`
	Min         string `json:"min"`
	Latest      string `json:"latest"`
	Required    bool   `json:"required"` // dưới bản tối thiểu -> chặn sử dụng
	Outdated    bool   `json:"outdated"` // có bản mới hơn
	DownloadURL string `json:"downloadUrl"`
	Message     string `json:"message"`
	CheckedAt   string `json:"checkedAt"`
}

// GetAppVersion trả số phiên bản cho giao diện.
func (a *App) GetAppVersion() string { return AppVersion }

// GetVersionStatus trả kết quả kiểm tra phiên bản gần nhất cho giao diện.
func (a *App) GetVersionStatus() versionStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.version
	st.Current = AppVersion
	return st
}

// RecheckVersion: nút "Kiểm tra lại" trên màn hình chặn.
func (a *App) RecheckVersion() versionStatus {
	a.checkAppVersion()
	return a.GetVersionStatus()
}

// OpenDownloadPage mở trang/bản tải bằng trình duyệt hệ thống (để tải file cài).
func (a *App) OpenDownloadPage(link string) {
	link = strings.TrimSpace(link)
	if link == "" {
		return
	}
	if strings.HasPrefix(link, "/") {
		link = strings.TrimRight(API_BASE, "/") + link
	}
	if u, err := url.Parse(link); err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return
	}
	runtime.BrowserOpenURL(a.ctx, link)
}

// scRequest tạo request tới SC kèm phiên bản Client.
func scRequest(method, rawURL string) (*http.Request, error) {
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-App-Version", AppVersion)
	req.Header.Set("X-App-Platform", appPlatform())
	return req, nil
}

// scPostJSON gửi POST JSON tới SC kèm phiên bản Client.
func scPostJSON(client *http.Client, rawURL string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-App-Version", AppVersion)
	req.Header.Set("X-App-Platform", appPlatform())
	return client.Do(req)
}

// versionHeader dùng cho kết nối WebSocket tới SC.
func versionHeader() http.Header {
	h := http.Header{}
	h.Set("X-App-Version", AppVersion)
	h.Set("X-App-Platform", appPlatform())
	return h
}

// checkAppVersion hỏi SC bản tối thiểu / mới nhất.
func (a *App) checkAppVersion() {
	q := url.Values{"app": {"client"}, "version": {AppVersion}, "platform": {appPlatform()}}
	client := http.Client{Timeout: 6 * time.Second}
	resp, err := client.Get(API_BASE + "/api/public/app-version?" + q.Encode())
	if err != nil {
		return // mất mạng: giữ kết quả cũ, không chặn oan
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var st versionStatus
	if json.NewDecoder(resp.Body).Decode(&st) != nil {
		return
	}
	if strings.HasPrefix(st.DownloadURL, "/") {
		st.DownloadURL = strings.TrimRight(API_BASE, "/") + st.DownloadURL
	}
	st.CheckedAt = time.Now().Format(time.RFC3339)
	a.mu.Lock()
	prev := a.version.Required
	a.version = st
	a.mu.Unlock()
	if st.Required && !prev {
		log.Printf("[VERSION] %s dưới bản tối thiểu %s — chặn sử dụng", AppVersion, st.Min)
	}
}

// markVersionBlocked: SC báo chặn ngay trong cấu hình lớp (allowed-apps) -> chặn luôn,
// không chờ lượt kiểm tra định kỳ.
func (a *App) markVersionBlocked(reason string) {
	a.mu.Lock()
	already := a.version.Required
	if !already {
		a.version.Required = true
		a.version.Message = reason
	}
	a.mu.Unlock()
	if !already {
		go a.checkAppVersion()
	}
}

// versionCheckLoop: kiểm tra lúc mở app và mỗi 30 phút.
func (a *App) versionCheckLoop() {
	a.checkAppVersion()
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	for range t.C {
		a.checkAppVersion()
	}
}

func versionLabel() string { return fmt.Sprintf("v%s", AppVersion) }
