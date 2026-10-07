package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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
	a.browserMode = mode
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

// handleBrowserOpenFromIDE: POST /browser/open {"url": "..."} từ Rikkei Ide.
func (a *App) handleBrowserOpenFromIDE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get("X-Rikkei-Ide") != "1" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16*1024)).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	u := strings.TrimSpace(body.URL)
	low := strings.ToLower(u)
	if !strings.HasPrefix(low, "http://") && !strings.HasPrefix(low, "https://") {
		http.Error(w, "only http(s)", http.StatusBadRequest)
		return
	}
	if err := a.OpenBrowser(u); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("[BROWSER] opened from Rikkei Ide: %s", u)
	_, _ = w.Write([]byte("ok"))
}

// ideStatus là trạng thái Client báo cho Rikkei Ide (GET /ide/status).
type ideStatus struct {
	LoggedIn    bool   `json:"loggedIn"`
	StudentRkID int64  `json:"studentRkId"`
	StudentCode string `json:"studentCode"`
	StudentName string `json:"studentName"`
	Mode        string `json:"mode"` // free | learning | exam
	Label       string `json:"label"`
	ClassRkID   int64  `json:"classRkId"`
	ClassCode   string `json:"classCode"`
	CourseName  string `json:"courseName"`
	ExamRoomID  uint   `json:"examRoomId,omitempty"`
	ExamName    string `json:"examName,omitempty"`
	EndsAt      string `json:"endsAt,omitempty"` // RFC3339: hết giờ thi / hết ca học
}

// currentIDEStatus: chế độ lấy theo cấu hình lớp đã áp cho trình duyệt (chính xác
// nhất: ngoài giờ / giờ học / giờ thi); chưa có thì suy từ trạng thái dashboard.
func (a *App) currentIDEStatus() ideStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := ideStatus{Mode: a.browserMode}
	d := a.dashboard
	if st.Mode == "" {
		switch d.MonitorMode {
		case "exam", "learning":
			st.Mode = d.MonitorMode
		default:
			st.Mode = "free"
		}
	}
	if a.student != nil && a.student.StudentID > 0 {
		st.LoggedIn = true
		st.StudentRkID = a.student.StudentID
		st.StudentCode = a.student.StudentCode
		st.StudentName = a.student.FullName
	}
	st.ClassRkID, st.ClassCode, st.CourseName = d.ClassRkID, d.ClassCode, d.CurrentCourseName
	switch st.Mode {
	case "exam":
		if d.Exam != nil {
			st.ExamRoomID, st.ExamName = d.Exam.ExamRoomID, d.Exam.ExamName
			st.Label = "Giờ thi · " + d.Exam.ExamName
			if !d.Exam.EndTime.IsZero() {
				st.EndsAt = d.Exam.EndTime.Format(time.RFC3339)
			}
		} else {
			st.Label = "Giờ thi"
		}
	case "learning":
		st.Label = "Giờ học"
		if d.CurrentCourseName != "" {
			st.Label += " · " + d.CurrentCourseName
		}
		if t, err := time.ParseInLocation("15:04", d.CurrentShiftEnd, time.Local); err == nil {
			now := time.Now()
			st.EndsAt = time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, time.Local).Format(time.RFC3339)
		}
	default:
		st.Label = "Ngoài giờ học"
	}
	return st
}

// handleIDEStatus: GET /ide/status — chỉ cho Rikkei Ide (header X-Rikkei-Ide, không CORS).
func (a *App) handleIDEStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.Header.Get("X-Rikkei-Ide") != "1" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.currentIDEStatus())
}
