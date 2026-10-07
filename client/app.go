package main

import (
	"archive/zip"
	"bytes"
	"client/internal/browser"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"client/internal/blocker"
	"client/internal/camera"
	"client/internal/guard"
	"client/internal/netproxy"
	"client/internal/proxywatch"
	"client/internal/screen"
	"client/internal/screenrecord"
	"client/internal/winapi"

	"github.com/gorilla/websocket"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

var blockedReportLast sync.Map            // processName -> time.Time
var blockedSiteViolationLast sync.Map     // blocked-site keyword -> time.Time (throttle vi phạm web)
var unauthorizedAppViolationLast sync.Map // processName -> time.Time
var screenRecordUploading atomic.Bool

var API_BASE = getEnv("API_BASE", "https://sc.rikkeiedu.com")

// Trang đăng nhập sinh viên: WebView điều hướng tới đây, sau khi login trang tự
// ghi localStorage.student = {studentId,...}. Tách ra env để trỏ portal khác
// (vd portal LMS mới) mà không phải build lại client.
var STUDENT_PORTAL_URL = getEnv("STUDENT_PORTAL_URL", API_BASE+"/login")

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getWsUrl(apiBase string) string {
	if strings.HasPrefix(apiBase, "https://") {
		return strings.Replace(apiBase, "https://", "wss://", 1)
	}
	if strings.HasPrefix(apiBase, "http://") {
		return strings.Replace(apiBase, "http://", "ws://", 1)
	}
	return "ws://" + apiBase
}

var wifiReportLast sync.Map // ssidKey -> time.Time

func getDynamicEncryptionKey() []byte {
	base := "simple-care-proctor-secretkey32!"
	hostname, _ := os.Hostname()
	home, _ := os.UserHomeDir()
	combined := fmt.Sprintf("%s-%s-%s", base, hostname, home)

	hasher := sha256.New()
	hasher.Write([]byte(combined))
	return hasher.Sum(nil)
}

func encrypt(data []byte) ([]byte, error) {
	key := getDynamicEncryptionKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nonce, nonce, data, nil)
	return ciphertext, nil
}

func decrypt(data []byte) ([]byte, error) {
	key := getDynamicEncryptionKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}

type StudentData struct {
	StudentID   int64  `json:"studentId"`
	FullName    string `json:"fullName"`
	Email       string `json:"email"`
	Avatar      string `json:"avatar"`
	Dob         string `json:"dob"`
	StudentCode string `json:"studentCode"`
	Phone       string `json:"phone"`
	SystemID    int64  `json:"systemId"`
}

type App struct {
	ctx                     context.Context
	student                 *StudentData
	mu                      sync.Mutex
	sessionPath             string
	statsPath               string
	webviewDataPath         string
	pendingViolPath         string
	runLockPath             string
	wsConn                  *websocket.Conn
	wsConnected             bool
	wsConnecting            bool
	wsBackoff               time.Duration
	wsWriteMu               sync.Mutex
	wifiSSID                string
	wifiBSSID               string
	allowedApps             string
	blockedSites            []string // website cấm (keyword theo host) — bắt vi phạm khi proxy thấy host khớp
	allowedWifi             []string // SSID WiFi được phép (rỗng = không giới hạn); sai -> ghi vi phạm
	wifiViolatedSSID        string   // SSID đã ghi vi phạm gần nhất (tránh spam mỗi vòng lặp)
	onlineSecs              int
	offlineSecs             int
	unsyncedOn              int
	unsyncedOff             int
	lastSyncTime            time.Time
	isStreamingSc           bool
	streamScStop            chan struct{}
	scIntervalMs            atomic.Int64 // nhịp chụp màn hình hiện tại (ms) — đổi live theo FPS giám sát viên chọn
	scQuality               atomic.Int64 // chất lượng JPEG luồng xem (20..95) — đổi live theo giám sát viên chọn
	isStreamingCam          bool
	streamCamStop           chan struct{}
	statusMsg               string
	expectingLogin          bool
	needsClearPortalStorage bool
	backendOnline           bool
	dashboard               StudentDashboardSnapshot
	wifiEnforce             bool
	acceptedWifi            map[string]bool
	wifiRejected            bool
	quitDialogShown         bool
	monitoringTornDown      bool
	localBrowserActive      bool // trình duyệt local — chỉ cho phép localhost
	chatUnread              int
	replyStaffID            uint
	fetchAppsMu             sync.Mutex
	browser                 *browser.Launcher // trình duyệt tích hợp (tiến trình con)
	browserPolicySet        bool              // đã có chính sách trình duyệt từ cấu hình lớp
	browserMode             string            // chế độ hiện hành: free | learning | exam (theo cấu hình lớp)
}

type LocalStats struct {
	OnlineSecs  int `json:"onlineSecs"`
	OfflineSecs int `json:"offlineSecs"`
	UnsyncedOn  int `json:"unsyncedOn"`
	UnsyncedOff int `json:"unsyncedOff"`
}

type StudentShiftSnapshot struct {
	Period           int    `json:"period"`
	CourseName       string `json:"courseName"`
	StartTime        string `json:"startTime"`
	EndTime          string `json:"endTime"`
	IsActiveNow      bool   `json:"isActiveNow"`
	OnlineSeconds    int    `json:"onlineSeconds"`
	OfflineSeconds   int    `json:"offlineSeconds"`
	AttendanceStatus int    `json:"attendanceStatus"`
	AttendanceLabel  string `json:"attendanceLabel"`
}

type StudentExamSnapshot struct {
	ExamRoomID uint      `json:"examRoomId"`
	ExamName   string    `json:"examName"`
	StartTime  time.Time `json:"startTime"`
	EndTime    time.Time `json:"endTime"`
	QuizURL    string    `json:"quizUrl"`
	PaperSent  bool      `json:"paperSent"`
	PaperTitle string    `json:"paperTitle"`
	Submitted  bool      `json:"submitted"`
}

type StudentDashboardSnapshot struct {
	SessionDate       string                 `json:"sessionDate"`
	MonitorMode       string                 `json:"monitorMode"`
	MonitorLabel      string                 `json:"monitorLabel"`
	ClassRkID         int64                  `json:"classRkId"`
	ClassName         string                 `json:"className"`
	ClassCode         string                 `json:"classCode"`
	CurrentPeriod     int                    `json:"currentPeriod"`
	CurrentCourseName string                 `json:"currentCourseName"`
	CurrentShiftStart string                 `json:"currentShiftStart"`
	CurrentShiftEnd   string                 `json:"currentShiftEnd"`
	InScheduleNow     bool                   `json:"inScheduleNow"`
	BlockerActive     bool                   `json:"blockerActive"`
	Shifts            []StudentShiftSnapshot `json:"shifts"`
	Exam              *StudentExamSnapshot   `json:"exam,omitempty"`
}

func NewApp() *App {
	configDir, err := os.UserConfigDir()
	if err != nil {
		// Fallback to executable folder if UserConfigDir is unavailable
		exePath, _ := os.Executable()
		configDir = filepath.Dir(exePath)
	}
	appDir := filepath.Join(configDir, "SimpleCare")
	webviewDir := filepath.Join(appDir, "WebView2")
	_ = os.MkdirAll(appDir, 0755)
	_ = os.MkdirAll(webviewDir, 0755)

	return &App{
		sessionPath:     filepath.Join(appDir, "student_session.json"),
		statsPath:       filepath.Join(appDir, "student_stats.json"),
		webviewDataPath: webviewDir,
		pendingViolPath: filepath.Join(appDir, "pending_violations.json"),
		runLockPath:     filepath.Join(appDir, "monitoring.lock"),
		lastSyncTime:    time.Now(),
		expectingLogin:  false,
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// An toàn trước tiên: nếu phiên trước bị kill khi đang bật proxy hệ thống, khôi phục ngay để
	// máy không bị kẹt mất mạng. Đồng thời ghi sẵn script cứu hộ ra đĩa phòng khi app không mở lại được.
	netproxy.RecoverStaleProxyIfAny()
	netproxy.WriteRescueScript()
	// Watchdog chạy ngầm: nếu app chính crash/kill mà chưa kịp gỡ proxy, watchdog
	// tự gỡ ngay để máy không kẹt mạng (không cần mở lại app).
	proxywatch.Spawn()

	a.loadSession()
	a.loadStats()

	// Task Manager / kill process lần trước → còn file lock → báo unclean_shutdown
	a.detectUncleanShutdown()
	go a.flushPendingViolations()

	// Request Location Access (macOS)
	winapi.RequestLocationAccess()
	winapi.RequestCameraAndMicAccess()
	winapi.RequestScreenCaptureAccess()

	// Log initial permission statuses
	log.Printf("[PERMISSIONS] Camera Status: %d (0=NotDetermined, 1=Restricted, 2=Denied, 3=Authorized)", winapi.GetCameraPermission())
	log.Printf("[PERMISSIONS] Microphone Status: %d (0=NotDetermined, 1=Restricted, 2=Denied, 3=Authorized)", winapi.GetMicrophonePermission())
	log.Printf("[PERMISSIONS] Screen Capture Status: %d (0=NoAccess, 1=Authorized, -1=NotSupported)", winapi.GetScreenCapturePermission())

	// Register blocker callbacks — chỉ ghi nhận vi phạm (kèm ảnh chụp màn hình làm bằng chứng),
	// không tự động đóng ứng dụng của sinh viên.
	blocker.Instance.OnBlocked = func(procName string, title string) {
		go a.reportBlockedApp(procName, title)
		go a.reportUnauthorizedApp(procName, title)
	}

	// Giám sát môi trường Windows: đa màn hình, desktop ảo, đổi user
	guard.Start(a.handleGuardViolation)

	// Khởi tạo thư mục lưu timelapse màn hình (ẩn) + dọn phiên cũ hơn 3 ngày
	screenrecord.Init()

	// Khởi chạy HTTP server nhận callback login thành công
	a.startLocalServer()

	// Khởi chạy vòng lặp giám sát định kỳ (mỗi 10 giây)
	go a.monitorLoop()

	// Khởi chạy vòng lặp gửi danh sách ứng dụng đang mở lên server (mỗi 20 giây)
	go a.openAppsReporterLoop()

	// Khởi chạy vòng lặp gửi lô URL bắt được qua local proxy lên server (mỗi 15 giây)
	go a.capturedURLReporterLoop()

	// Khởi chạy vòng lặp đọc local storage của trang Rikkei Portal khi chưa đăng nhập
	go a.authStorageScanner()
}

func (a *App) handleGuardViolation(kind, reason string) {
	if kind == "" {
		kind = "guard"
	}
	a.reportViolation(kind, reason)
	a.showQuitDialog("Vi phạm giám sát", reason)
}

// HandleBeforeClose — SV bấm X / Alt+F4: luôn hỏi xác nhận.
// Có → thoát + lưu vết vi phạm (nếu đang giám sát exam/learning). Không → hủy đóng.
func (a *App) HandleBeforeClose() (prevent bool) {
	a.mu.Lock()
	mode := a.dashboard.MonitorMode
	loggedIn := a.student != nil
	a.mu.Unlock()

	message := "Bạn có chắc muốn thoát Rikkei Lms Connect không?"
	if loggedIn && a.isMonitoringActive() && shouldRecordViolation(mode) {
		message = "Bạn có chắc chắn muốn thoát ứng dụng không?\n\nLưu ý: Thoát khi đang trong giờ thi/học sẽ được ghi nhận là VI PHẠM."
	}

	selection, err := runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Type:          runtime.QuestionDialog,
		Title:         "Xác nhận thoát",
		Message:       message,
		Buttons:       []string{"Có, thoát ứng dụng", "Không, tiếp tục"},
		DefaultButton: "Không, tiếp tục",
		CancelButton:  "Không, tiếp tục",
	})
	if err != nil {
		log.Printf("[CLOSE] MessageDialog error: %v — hủy thoát để an toàn", err)
		return true
	}

	if !isConfirmQuitSelection(selection) {
		return true // Không / Cancel → ở lại
	}

	// Có → gửi vi phạm lên server rồi mới thoát
	if loggedIn && a.isMonitoringActive() && shouldRecordViolation(mode) {
		a.reportViolation("app_closed", "Sinh viên xác nhận tắt ứng dụng khi đang giám sát ("+mode+")")
		a.tearDownBeforeQuit()
	}
	a.clearRunLock()
	log.Printf("[CLOSE] User confirmed quit (mode=%s, loggedIn=%v)", mode, loggedIn)
	return false
}

func isConfirmQuitSelection(selection string) bool {
	s := strings.TrimSpace(strings.ToLower(selection))
	switch s {
	case "có, thoát ứng dụng", "co, thoat ung dung", "yes", "ok", "có", "co":
		return true
	}
	// Windows đôi khi trả về đúng nhãn nút đã truyền
	return strings.Contains(s, "thoát") || strings.Contains(s, "thoat") || s == "yes"
}

// tearDownBeforeQuit ngắt mọi kênh giám sát ngay — trước khi hiện dialog (tránh treo OK để duy trì kết nối).
func (a *App) tearDownBeforeQuit() {
	a.mu.Lock()
	if a.monitoringTornDown {
		a.mu.Unlock()
		return
	}
	a.monitoringTornDown = true
	a.mu.Unlock()

	blocker.Instance.Stop()
	screenrecord.Stop()
	netproxy.Stop()
	_ = netproxy.DisableSystemProxy()
	a.browserLauncher().Close()
	a.stopScreenshotStream()
	a.stopWebcamStream()
	a.disconnectWS()
	guard.Stop()
	a.clearRunLock()
}

func (a *App) isMonitoringActive() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return !a.monitoringTornDown
}

// showQuitDialog — ngắt giám sát trước, hiện cảnh báo, sinh viên bấm OK rồi app mới thoát.
func (a *App) showQuitDialog(title, message string) {
	a.mu.Lock()
	if a.quitDialogShown {
		a.mu.Unlock()
		return
	}
	a.quitDialogShown = true
	a.mu.Unlock()

	a.tearDownBeforeQuit()

	_, _ = runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Type:    runtime.WarningDialog,
		Title:   title,
		Message: message + "\n\nNhấn OK để đóng ứng dụng.",
	})
	runtime.Quit(a.ctx)
}

type pendingViolation struct {
	Kind        string `json:"kind"`
	Reason      string `json:"reason"`
	MonitorMode string `json:"monitorMode"`
	ClientAt    string `json:"clientAt"`
	StudentRkID int64  `json:"studentRkId"`
	ClassRkID   int64  `json:"classRkId"`
	ExamRoomID  uint   `json:"examRoomId"`
	// Screenshot — ảnh chụp màn hình (data:image/jpeg;base64,...) làm bằng chứng, tùy chọn.
	Screenshot string `json:"screenshot,omitempty"`
}

// shouldRecordViolation — chỉ lưu vi phạm khi đang thi hoặc đang học (trong giờ).
func shouldRecordViolation(monitorMode string) bool {
	switch strings.TrimSpace(monitorMode) {
	case "exam", "learning":
		return true
	default:
		return false
	}
}

func (a *App) markRunLock() {
	a.mu.Lock()
	studentID := int64(0)
	mode := a.dashboard.MonitorMode
	classID := a.dashboard.ClassRkID
	examRoomID := uint(0)
	if a.dashboard.Exam != nil {
		examRoomID = a.dashboard.Exam.ExamRoomID
	}
	if a.student != nil {
		studentID = a.student.StudentID
	}
	a.mu.Unlock()
	if studentID <= 0 || !shouldRecordViolation(mode) {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"studentRkId": studentID,
		"monitorMode": mode,
		"classRkId":   classID,
		"examRoomId":  examRoomID,
		"startedAt":   time.Now().Format(time.RFC3339),
	})
	_ = os.WriteFile(a.runLockPath, payload, 0644)
}

func (a *App) clearRunLock() {
	_ = os.Remove(a.runLockPath)
}

func (a *App) detectUncleanShutdown() {
	data, err := os.ReadFile(a.runLockPath)
	if err != nil {
		return
	}
	_ = os.Remove(a.runLockPath)

	var meta struct {
		StudentRkID int64  `json:"studentRkId"`
		MonitorMode string `json:"monitorMode"`
		ClassRkID   int64  `json:"classRkId"`
		ExamRoomID  uint   `json:"examRoomId"`
		StartedAt   string `json:"startedAt"`
	}
	_ = json.Unmarshal(data, &meta)
	if meta.StudentRkID <= 0 {
		a.mu.Lock()
		if a.student != nil {
			meta.StudentRkID = a.student.StudentID
		}
		a.mu.Unlock()
	}
	if meta.StudentRkID <= 0 || !shouldRecordViolation(meta.MonitorMode) {
		return
	}
	reason := "Ứng dụng bị tắt đột ngột (Task Manager / kill process) khi đang giám sát"
	if meta.MonitorMode != "" {
		reason += " (" + meta.MonitorMode + ")"
	}
	a.enqueuePendingViolation(pendingViolation{
		Kind:        "unclean_shutdown",
		Reason:      reason,
		MonitorMode: meta.MonitorMode,
		ClientAt:    time.Now().Format(time.RFC3339),
		StudentRkID: meta.StudentRkID,
		ClassRkID:   meta.ClassRkID,
		ExamRoomID:  meta.ExamRoomID,
	})
}

func (a *App) enqueuePendingViolation(v pendingViolation) {
	list := a.loadPendingViolations()
	list = append(list, v)
	raw, _ := json.Marshal(list)
	_ = os.WriteFile(a.pendingViolPath, raw, 0644)
}

func (a *App) loadPendingViolations() []pendingViolation {
	data, err := os.ReadFile(a.pendingViolPath)
	if err != nil {
		return nil
	}
	var list []pendingViolation
	if json.Unmarshal(data, &list) != nil {
		return nil
	}
	return list
}

func (a *App) flushPendingViolations() {
	list := a.loadPendingViolations()
	if len(list) == 0 {
		return
	}
	remaining := make([]pendingViolation, 0)
	for _, v := range list {
		if !a.postViolation(v) {
			remaining = append(remaining, v)
		}
	}
	if len(remaining) == 0 {
		_ = os.Remove(a.pendingViolPath)
		return
	}
	raw, _ := json.Marshal(remaining)
	_ = os.WriteFile(a.pendingViolPath, raw, 0644)
}

func (a *App) postViolation(v pendingViolation) bool {
	if v.StudentRkID <= 0 || !shouldRecordViolation(v.MonitorMode) {
		return true // bỏ qua — không thi / không học thì không lưu
	}
	payload := map[string]any{
		"studentRkId": v.StudentRkID,
		"kind":        v.Kind,
		"reason":      v.Reason,
		"monitorMode": v.MonitorMode,
		"clientAt":    v.ClientAt,
		"classRkId":   v.ClassRkID,
		"examRoomId":  v.ExamRoomID,
		"screenshot":  v.Screenshot,
	}
	bodyBytes, _ := json.Marshal(payload)
	client := http.Client{Timeout: 8 * time.Second}
	resp, err := client.Post(API_BASE+"/api/student/report-violation", "application/json", bytes.NewBuffer(bodyBytes))
	if err != nil {
		log.Printf("[VIOLATION] report failed: %v", err)
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[VIOLATION] report status %d: %s", resp.StatusCode, string(body))
		return false
	}
	log.Printf("[VIOLATION] reported kind=%s student=%d class=%d exam=%d", v.Kind, v.StudentRkID, v.ClassRkID, v.ExamRoomID)
	return true
}

func (a *App) reportViolation(kind, reason string) {
	a.reportViolationWithScreenshot(kind, reason, "")
}

// checkAllowedWifi: lớp/phòng thi đặt danh sách WiFi cho phép theo BSSID (12 hex,
// khó giả). SV dùng AP ngoài danh sách -> ghi 1 vi phạm (mỗi BSSID chỉ ghi 1 lần).
// Rỗng = không giới hạn. Không đọc được BSSID (chưa cấp quyền vị trí) -> bỏ qua để
// tránh báo nhầm. Chỉ ghi khi đang giám sát.
func (a *App) checkAllowedWifi(ssid, bssid string) {
	if !a.isMonitoringActive() {
		return
	}
	a.mu.Lock()
	allowed := a.allowedWifi
	last := a.wifiViolatedSSID
	a.mu.Unlock()

	if len(allowed) == 0 {
		a.mu.Lock()
		a.wifiViolatedSSID = ""
		a.mu.Unlock()
		return
	}
	bKey := winapi.BSSIDKey(bssid)
	if len(bKey) != 12 { // không xác định được AP -> không kết luận
		return
	}
	for _, w := range allowed {
		if w == bKey {
			a.mu.Lock()
			a.wifiViolatedSSID = ""
			a.mu.Unlock()
			return // đúng WiFi cho phép
		}
	}
	if last == bKey {
		return // đã ghi vi phạm cho AP này rồi
	}
	a.mu.Lock()
	a.wifiViolatedSSID = bKey
	a.mu.Unlock()
	name := strings.TrimSpace(ssid)
	if name == "" {
		name = bKey
	}
	go a.reportViolation("wrong_wifi", fmt.Sprintf("Dùng WiFi không được phép: %s (BSSID %s ngoài danh sách cho phép)", name, bKey))
}

// reportViolationWithScreenshot — như reportViolation nhưng kèm ảnh chụp màn hình làm bằng chứng (tùy chọn).
func (a *App) reportViolationWithScreenshot(kind, reason, screenshot string) {
	a.mu.Lock()
	studentID := int64(0)
	mode := a.dashboard.MonitorMode
	classID := a.dashboard.ClassRkID
	examRoomID := uint(0)
	if a.dashboard.Exam != nil {
		examRoomID = a.dashboard.Exam.ExamRoomID
	}
	if a.student != nil {
		studentID = a.student.StudentID
	}
	a.mu.Unlock()
	if studentID <= 0 || !shouldRecordViolation(mode) {
		return
	}
	v := pendingViolation{
		Kind:        kind,
		Reason:      reason,
		MonitorMode: mode,
		ClientAt:    time.Now().Format(time.RFC3339),
		StudentRkID: studentID,
		ClassRkID:   classID,
		ExamRoomID:  examRoomID,
		Screenshot:  screenshot,
	}
	if !a.postViolation(v) {
		a.enqueuePendingViolation(v)
	}
}

// startLocalServer khởi chạy server lắng nghe callback nhận thông tin sinh viên từ webview
func (a *App) startLocalServer() {
	mux := http.NewServeMux()
	// Rikkei Ide gửi link web sang trình duyệt tích hợp (IDE không có trình duyệt riêng).
	// Chỉ nhận POST có header X-Rikkei-Ide, không bật CORS -> trang web chạy trong
	// trình duyệt khác không gọi được (trình duyệt sẽ chặn ở bước preflight).
	mux.HandleFunc("/browser/open", a.handleBrowserOpenFromIDE)
	mux.HandleFunc("/ide/status", a.handleIDEStatus)
	mux.HandleFunc("/login-success", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			return
		}

		data := r.URL.Query().Get("data")
		if data == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		a.mu.Lock()
		expecting := a.expectingLogin
		a.mu.Unlock()
		if !expecting {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ignored"))
			return
		}

		var st StudentData
		if err := json.Unmarshal([]byte(data), &st); err == nil && st.StudentID > 0 {
			a.saveSession(&st)
			log.Printf("[LOCALSERVER] Student %s logged in successfully.", st.FullName)

			a.mu.Lock()
			a.expectingLogin = false
			a.mu.Unlock()

			// Kiểm tra điều kiện app được phép và giờ học trước khi tải giao diện
			go a.refreshStudentData()

			// Chuyển hướng WebView về trang dashboard của app bằng cách reload app assets
			guard.SuppressFor(5 * time.Second)
			runtime.WindowReloadApp(a.ctx)
			w.Write([]byte("ok"))
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
	})

	// /login-lms — portal LMS mới (lms-student) chỉ có access token + hồ sơ
	// ObjectId, KHÔNG có studentId int64. Đổi token qua SC /api/student/resolve
	// để lấy studentRkId (int64) rồi mới lưu phiên — giữ nguyên giao thức cũ.
	mux.HandleFunc("/login-lms", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == "OPTIONS" {
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			return
		}
		token := strings.TrimSpace(r.URL.Query().Get("token"))
		if token == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		a.mu.Lock()
		expecting := a.expectingLogin
		a.mu.Unlock()
		if !expecting {
			w.Write([]byte("ignored"))
			return
		}

		st, err := a.resolveStudentViaSC(token)
		if err != nil || st == nil || st.StudentID <= 0 {
			log.Printf("[LOGIN-LMS] resolve thất bại: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		a.saveSession(st)
		log.Printf("[LOGIN-LMS] %s (%s / rk=%d) đăng nhập", st.FullName, st.StudentCode, st.StudentID)
		a.mu.Lock()
		a.expectingLogin = false
		a.mu.Unlock()
		go a.refreshStudentData()
		guard.SuppressFor(5 * time.Second)
		runtime.WindowReloadApp(a.ctx)
		w.Write([]byte("ok"))
	})

	// /login-done — trang login CỦA SC ({sc}/login) sau khi đăng nhập chuyển tới
	// {sc}/login/done?studentRkId=...; scanner đẩy query về đây. SC đã resolve sẵn
	// nên chỉ dựng phiên từ danh tính (không cần token).
	mux.HandleFunc("/login-done", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		a.mu.Lock()
		expecting := a.expectingLogin
		a.mu.Unlock()
		if !expecting {
			w.Write([]byte("ignored"))
			return
		}
		q := r.URL.Query()
		rk, _ := strconv.ParseInt(strings.TrimSpace(q.Get("studentRkId")), 10, 64)
		if rk <= 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		st := &StudentData{
			StudentID:   rk,
			FullName:    q.Get("fullName"),
			StudentCode: q.Get("studentCode"),
			Email:       q.Get("email"),
		}
		a.saveSession(st)
		log.Printf("[LOGIN-DONE] %s (%s / rk=%d) đăng nhập qua SC", st.FullName, st.StudentCode, rk)
		a.mu.Lock()
		a.expectingLogin = false
		a.mu.Unlock()
		go a.refreshStudentData()
		guard.SuppressFor(5 * time.Second)
		runtime.WindowReloadApp(a.ctx)
		w.Write([]byte("ok"))
	})

	server := &http.Server{
		Addr:    "127.0.0.1:34115",
		Handler: mux,
	}

	mux.HandleFunc("/exam-view", func(w http.ResponseWriter, r *http.Request) {
		target := strings.TrimSpace(r.URL.Query().Get("url"))
		title := strings.TrimSpace(r.URL.Query().Get("title"))
		if target == "" {
			http.Error(w, "missing url", http.StatusBadRequest)
			return
		}
		if title == "" {
			title = "Xem trong Rikkei Lms Connect"
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, examViewHTML, html.EscapeString(title), html.EscapeString(title), html.EscapeString(target))
	})
	mux.HandleFunc("/exam-close", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == "OPTIONS" {
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			return
		}
		a.setLocalBrowserActive(false)
		go func() {
			guard.SuppressFor(5 * time.Second)
			runtime.WindowReloadApp(a.ctx)
		}()
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/exam-clear-cache", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == "OPTIONS" {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			return
		}
		// Chỉ xóa HTTP disk cache — không đụng cookie / localStorage (giữ phiên đăng nhập trang thi).
		go a.purgeWebViewDiskCache()
		w.Write([]byte("ok"))
	})
	go func() {
		_ = server.ListenAndServe()
	}()
	log.Println("[LOCALSERVER] Callback server started on http://127.0.0.1:34115")
}

const examViewHTML = `<!DOCTYPE html>
<html lang="vi">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:Segoe UI,system-ui,sans-serif;background:#f5f7fa;color:#1a2332;height:100vh;display:flex;flex-direction:column}
.bar{display:flex;align-items:center;gap:8px;padding:10px 14px;background:#fff;border-bottom:1px solid #e4e9f0;flex-shrink:0}
.bar-title{font-size:14px;font-weight:600;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;margin-right:auto;min-width:0;color:#1a2332}
.bar button{border-radius:8px;padding:8px 12px;font-size:13px;cursor:pointer;font-weight:600;white-space:nowrap}
.btn-back{background:#fff;color:#bb2126;border:1px solid #bb2126}
.btn-back:hover{background:#fde8e9}
.btn-refresh{background:#bb2126;color:#fff;border:1px solid #bb2126}
.btn-refresh:hover{background:#9e1c22;border-color:#9e1c22}
.btn-cache{background:#fff;color:#bb2126;border:1px solid #bb2126}
.btn-cache:hover{background:#fde8e9}
.btn-cache:disabled,.btn-refresh:disabled{opacity:.65;cursor:wait}
.hint{font-size:11px;color:#8b99a8;flex-shrink:0}
.frame-wrap{flex:1;min-height:0;background:#fff}
iframe{width:100%%;height:100%%;border:0;display:block}
</style>
</head>
<body>
<div class="bar">
<button type="button" class="btn-back" onclick="goBack()">← Quay lại</button>
<span class="bar-title">%s</span>
<span class="hint">Trang trắng/lỗi: F5 hoặc Xóa cache (giữ đăng nhập)</span>
<button type="button" class="btn-refresh" id="btn-refresh" onclick="refreshExam()">⟳ F5 Làm mới</button>
<button type="button" class="btn-cache" id="btn-cache" onclick="clearExamCache()">🗑 Xóa cache</button>
</div>
<div class="frame-wrap">
<iframe id="exam-frame" src="%s" title="exam-content"></iframe>
</div>
<script>
function goBack(){
  fetch('http://127.0.0.1:34115/exam-close').catch(function(){});
}
function reloadFrameSoft(){
  var f = document.getElementById('exam-frame');
  if (!f) return;
  try {
    f.contentWindow.location.reload();
  } catch (e) {
    var src = f.getAttribute('src') || f.src;
    f.src = 'about:blank';
    setTimeout(function(){ f.src = src; }, 50);
  }
}
function refreshExam(){
  var btn = document.getElementById('btn-refresh');
  if (btn) btn.disabled = true;
  reloadFrameSoft();
  setTimeout(function(){ if (btn) btn.disabled = false; }, 800);
}
async function clearExamCache(){
  var btn = document.getElementById('btn-cache');
  if (btn) btn.disabled = true;
  // Không xóa localStorage/sessionStorage/cookie — tránh văng đăng nhập trang thi.
  try {
    await fetch('http://127.0.0.1:34115/exam-clear-cache');
  } catch (e) {}
  // Chờ một nhịp để disk cache kịp xóa rồi reload cùng URL (không thêm query bust).
  setTimeout(function(){
    reloadFrameSoft();
    if (btn) btn.disabled = false;
  }, 400);
}
document.addEventListener('keydown', function(e){
  if (e.key === 'F5') {
    e.preventDefault();
    refreshExam();
  }
});
</script>
</body>
</html>`

// loadSession nạp session từ file cục bộ
func (a *App) loadSession() {
	a.mu.Lock()
	defer a.mu.Unlock()

	data, err := os.ReadFile(a.sessionPath)
	if err == nil {
		decrypted, errDec := decrypt(data)
		if errDec == nil {
			data = decrypted
		}
		var s StudentData
		if err := json.Unmarshal(data, &s); err == nil {
			a.student = &s
			log.Printf("[APP] Loaded local session: %s (%s)", s.FullName, s.StudentCode)

			go a.refreshStudentData()
			go a.fetchWifiPolicy()
			go a.syncProfileToServer(&s)
		}
	}
}

// saveSession lưu session ra file
// resolveStudentViaSC đổi access token LMS lấy danh tính SC (studentRkId int64).
// Gọi SC /api/student/resolve — SC tự kiểm chứng token với LMS và cấp/khớp rk_id.
func (a *App) resolveStudentViaSC(accessToken string) (*StudentData, error) {
	payload, _ := json.Marshal(map[string]string{"accessToken": accessToken})
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Post(API_BASE+"/api/student/resolve", "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("resolve HTTP %d: %s", resp.StatusCode, string(body))
	}
	var st StudentData
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (a *App) saveSession(s *StudentData) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.student = s
	data, _ := json.MarshalIndent(s, "", "  ")
	encrypted, err := encrypt(data)
	if err == nil {
		_ = os.WriteFile(a.sessionPath, encrypted, 0644)
	} else {
		_ = os.WriteFile(a.sessionPath, data, 0644)
	}
	go a.syncProfileToServer(s)
}

func (a *App) syncProfileToServer(s *StudentData) {
	if s == nil || s.Avatar == "" {
		return
	}
	payload := map[string]any{
		"studentRkId": s.StudentID,
		"classRkId":   s.SystemID,
		"sessionDate": time.Now().Format("2006-01-02"),
		"avatar":      s.Avatar,
	}
	bodyBytes, _ := json.Marshal(payload)
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(API_BASE+"/api/student/sync-log", "application/json", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

// loadStats nạp stats
func (a *App) loadStats() {
	a.mu.Lock()
	defer a.mu.Unlock()

	data, err := os.ReadFile(a.statsPath)
	if err == nil {
		var ls LocalStats
		if err := json.Unmarshal(data, &ls); err == nil {
			a.onlineSecs = ls.OnlineSecs
			a.offlineSecs = ls.OfflineSecs
			a.unsyncedOn = ls.UnsyncedOn
			a.unsyncedOff = ls.UnsyncedOff
		}
	}
}

// saveStats lưu stats ra file
func (a *App) saveStats() {
	a.mu.Lock()
	defer a.mu.Unlock()

	ls := LocalStats{
		OnlineSecs:  a.onlineSecs,
		OfflineSecs: a.offlineSecs,
		UnsyncedOn:  a.unsyncedOn,
		UnsyncedOff: a.unsyncedOff,
	}
	data, _ := json.Marshal(ls)
	_ = os.WriteFile(a.statsPath, data, 0644)
}

// CheckLoginStatus kiểm tra xem sinh viên đã đăng nhập chưa
func (a *App) CheckLoginStatus() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.student != nil
}

// GetStudentInfo trả về thông tin sinh viên
func (a *App) GetStudentInfo() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.student == nil {
		return nil
	}
	return map[string]any{
		"studentId":   a.student.StudentID,
		"fullName":    a.student.FullName,
		"email":       a.student.Email,
		"avatar":      a.student.Avatar,
		"dob":         a.student.Dob,
		"studentCode": a.student.StudentCode,
		"phone":       a.student.Phone,
		"systemId":    a.student.SystemID,
	}
}

// NavigateToLogin chuyển WebView đến trang đăng nhập Rikkei Portal
func (a *App) NavigateToLogin() {
	a.setLocalBrowserActive(false)
	a.mu.Lock()
	a.expectingLogin = true
	a.mu.Unlock()
	guard.SuppressFor(5 * time.Second)
	runtime.WindowExecJS(a.ctx, "window.location.href = '"+STUDENT_PORTAL_URL+"'")
}

// CheckPermissions trả trạng thái các quyền BẮT BUỘC để chạy giám sát:
//   - wifi:  đọc được SSID (Windows/macOS cần bật Vị trí) -> để đối chiếu Wi-Fi lớp.
//   - proxy: đặt được proxy hệ thống -> để bắt URL/giám sát mạng.
//
// Frontend gọi lúc mở app; thiếu quyền nào thì chặn, không cho vào đăng nhập.
func (a *App) CheckPermissions() map[string]bool {
	return map[string]bool{
		"wifi":  winapi.HasWifiPermission(),
		"proxy": netproxy.CanSetSystemProxy(),
	}
}

// OpenLocationSettings mở trang cài đặt Vị trí của hệ điều hành để SV tự bật.
func (a *App) OpenLocationSettings() {
	winapi.OpenLocationSettings()
}

// Logout đăng xuất sinh viên
func (a *App) Logout() {
	a.mu.Lock()
	a.student = nil
	a.expectingLogin = true
	a.needsClearPortalStorage = true
	a.mu.Unlock()

	_ = os.Remove(a.sessionPath)
	_ = os.Remove(a.statsPath)

	a.mu.Lock()
	a.onlineSecs = 0
	a.offlineSecs = 0
	a.unsyncedOn = 0
	a.unsyncedOff = 0
	a.mu.Unlock()

	blocker.Instance.Stop()
	screenrecord.Stop()
	netproxy.Stop()
	_ = netproxy.DisableSystemProxy()
	a.disconnectWS()
	a.stopScreenshotStream()
	a.stopWebcamStream()

	guard.SuppressFor(5 * time.Second)
	runtime.WindowExecJS(a.ctx, "window.location.href = '"+STUDENT_PORTAL_URL+"'")
}

// GetStats trả về Wifi, thời gian online/offline và trạng thái giám sát
func (a *App) GetStats() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()

	shifts := make([]StudentShiftSnapshot, len(a.dashboard.Shifts))
	copy(shifts, a.dashboard.Shifts)
	for i := range shifts {
		if shifts[i].IsActiveNow {
			shifts[i].OnlineSeconds += a.unsyncedOn
			shifts[i].OfflineSeconds += a.unsyncedOff
		}
	}

	return map[string]any{
		"wifiSSID":          a.wifiSSID,
		"onlineSecs":        a.onlineSecs,
		"offlineSecs":       a.offlineSecs,
		"wsConnected":       a.wsConnected,
		"serverReachable":   a.backendOnline,
		"allowedApps":       a.allowedApps,
		"statusMsg":         a.statusMsg,
		"sessionDate":       a.dashboard.SessionDate,
		"monitorMode":       a.dashboard.MonitorMode,
		"monitorLabel":      a.dashboard.MonitorLabel,
		"className":         a.dashboard.ClassName,
		"classCode":         a.dashboard.ClassCode,
		"currentPeriod":     a.dashboard.CurrentPeriod,
		"currentCourseName": a.dashboard.CurrentCourseName,
		"currentShiftStart": a.dashboard.CurrentShiftStart,
		"currentShiftEnd":   a.dashboard.CurrentShiftEnd,
		"inScheduleNow":     a.dashboard.InScheduleNow,
		"blockerActive":     a.dashboard.BlockerActive,
		"shifts":            shifts,
		"exam":              a.dashboard.Exam,
	}
}

// safeWriteWS writes a message to the WebSocket connection thread-safely
func (a *App) safeWriteWS(msg any) error {
	a.mu.Lock()
	conn := a.wsConn
	a.mu.Unlock()

	if conn == nil {
		return errors.New("websocket connection is nil")
	}

	a.wsWriteMu.Lock()
	defer a.wsWriteMu.Unlock()
	return conn.WriteJSON(msg)
}

// uploadScreenRecord — server yêu cầu (screen_record:request_upload): nén timelapse
// màn hình ra 1 FILE zip tạm (streaming, nhẹ RAM) rồi UPLOAD HTTP đa phần lên SC.
// KHÔNG còn gửi base64 qua WebSocket (cách cũ giữ cả khối + base64 + JSON trong
// RAM -> dễ hết bộ nhớ/crash khi buổi dài).
func (a *App) uploadScreenRecord() {
	if screenRecordUploading.Swap(true) {
		return // đang xử lý một yêu cầu khác — bỏ qua để tránh nén chồng
	}
	defer screenRecordUploading.Store(false)

	// Bảo hiểm: panic ở đây không được giết cả app (kẹt proxy). Watchdog là lớp cuối.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[SCREENREC] recovered from panic during upload: %v", r)
		}
	}()

	screenrecord.Stop()
	defer screenrecord.Start() // vẫn còn trong giờ giám sát thì tiếp tục ghi sau khi upload

	a.mu.Lock()
	code := ""
	if a.student != nil {
		code = a.student.StudentCode
	}
	a.mu.Unlock()
	if code == "" {
		log.Println("[SCREENREC] chưa có studentCode, bỏ qua upload")
		return
	}

	// Chia thành ĐOẠN tối đa perSegment khung để mỗi lần upload không quá lớn (tránh
	// vượt body limit) và để backlog lớn được rút cạn dần qua nhiều đoạn.
	const perSegment = 900
	for seg := 0; seg < 40; seg++ {
		zipPath, date, names, err := screenrecord.ZipTodayToFile(perSegment)
		if err != nil {
			if seg == 0 {
				log.Printf("[SCREENREC] zip: %v", err)
			}
			return // hết khung để gửi
		}
		if err := a.uploadZipHTTP(zipPath, code, date); err != nil {
			_ = os.Remove(zipPath)
			log.Printf("[SCREENREC] HTTP upload failed: %v — giữ lại khung để thử lại sau", err)
			return
		}
		_ = os.Remove(zipPath)
		screenrecord.RemoveFrames(names) // upload xong mới xoá khung đã gửi
		log.Printf("[SCREENREC] uploaded segment %d: %d frames (%s)", seg+1, len(names), date)
		if len(names) < perSegment {
			return // đã rút cạn backlog
		}
	}
}

// uploadZipHTTP gửi file zip lên SC bằng multipart, STREAMING từ đĩa qua io.Pipe
// (không nạp cả file vào RAM).
func (a *App) uploadZipHTTP(zipPath, code, date string) error {
	f, err := os.Open(zipPath)
	if err != nil {
		return err
	}
	defer f.Close()

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		var werr error
		defer func() { _ = pw.CloseWithError(werr) }()
		if werr = mw.WriteField("studentCode", code); werr != nil {
			return
		}
		if werr = mw.WriteField("date", date); werr != nil {
			return
		}
		part, e := mw.CreateFormFile("file", "frames.zip")
		if e != nil {
			werr = e
			return
		}
		if _, e := io.Copy(part, f); e != nil {
			werr = e
			return
		}
		werr = mw.Close()
	}()

	req, err := http.NewRequest(http.MethodPost, API_BASE+"/api/student/screen-record/upload", pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	cl := &http.Client{Timeout: 5 * time.Minute}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server trả %d", resp.StatusCode)
	}
	return nil
}

// SendWebcamFrame truyền webcam frame từ JS frontend lên máy chủ qua WS
func (a *App) SendWebcamFrame(frameBase64 string) {
	if !a.isMonitoringActive() {
		return
	}
	_ = a.safeWriteWS(map[string]any{
		"event": "webcam_stream_frame",
		"data": map[string]any{
			"imageBuffer": frameBase64,
		},
	})
}

// authStorageScanner chỉ quét localStorage khi người dùng chủ động mở trang đăng nhập
// authStorageScanner — quét nhanh (1s) chỉ khi đang chờ đăng nhập; ngoài ra ngủ dài (8s) để
// không thức dậy vô ích suốt vòng đời app.
func (a *App) authStorageScanner() {
	for {
		a.mu.Lock()
		expecting := a.expectingLogin
		needsClear := a.needsClearPortalStorage
		loggedIn := a.student != nil
		a.mu.Unlock()

		if needsClear && expecting {
			runtime.WindowExecJS(a.ctx, `
				try {
					localStorage.removeItem("student");
					localStorage.removeItem("token");
					localStorage.removeItem("lms_auth_session");
					document.cookie = "lms_access_token=; Max-Age=0; path=/";
				} catch(e) {}
			`)
			a.mu.Lock()
			a.needsClearPortalStorage = false
			a.mu.Unlock()
			time.Sleep(1 * time.Second)
			continue
		}

		if loggedIn || !expecting {
			time.Sleep(8 * time.Second)
			continue
		}

		runtime.WindowExecJS(a.ctx, `
			try {
				if (!window.__redirecting) {
					// Trang login của SC ({sc}/login): sau khi đăng nhập chuyển tới
					// {sc}/login/done?studentRkId=... -> đẩy query về local /login-done.
					if (location.pathname.indexOf("/login/done") !== -1) {
						window.__redirecting = true;
						window.location.href = "http://127.0.0.1:34115/login-done" + location.search;
					} else {
						// Fallback: portal LMS cũ (lms-student) — token trong cookie.
						const sess = localStorage.getItem("lms_auth_session");
						const m = document.cookie.match(/(?:^|;\s*)lms_access_token=([^;]+)/);
						const token = m ? decodeURIComponent(m[1]) : "";
						if (sess && token) {
							const u = (JSON.parse(sess) || {}).user || {};
							if (!u.userProfile || u.userProfile === "STUDENT") {
								window.__redirecting = true;
								window.location.href = "http://127.0.0.1:34115/login-lms?token=" + encodeURIComponent(token);
							}
						}
					}
				}
			} catch(e) {}
		`)
		time.Sleep(1 * time.Second)
	}
}

// monitorLoop định kỳ 10s: check wifi, ping backend, cập nhật online/offline seconds và đồng bộ lên server
func (a *App) monitorLoop() {
	for {
		a.runMonitorTick()

		a.mu.Lock()
		wifiEmpty := a.wifiSSID == "" || strings.Contains(a.wifiSSID, "Chưa cấp quyền") || strings.Contains(a.wifiSSID, "redacted")
		expecting := a.expectingLogin
		a.mu.Unlock()

		sleepInterval := 10 * time.Second
		if wifiEmpty && !expecting {
			sleepInterval = 2 * time.Second
		}

		time.Sleep(sleepInterval)
	}
}

func (a *App) runMonitorTick() {
	if !a.isMonitoringActive() || !a.CheckLoginStatus() {
		return
	}

	wifi := winapi.GetWifiConnection()
	a.mu.Lock()
	if strings.Contains(strings.ToLower(wifi.SSID), "redacted") {
		a.wifiSSID = "Chưa cấp quyền Vị Trí (macOS)"
	} else {
		a.wifiSSID = wifi.SSID
	}
	if strings.Contains(strings.ToLower(wifi.BSSID), "redacted") {
		a.wifiBSSID = "Chưa cấp quyền Vị Trí (macOS)"
	} else {
		a.wifiBSSID = wifi.BSSID
	}
	a.mu.Unlock()

	// WiFi cho phép theo lớp/phòng thi (khóa BSSID): sai -> ghi vi phạm (mềm, không chặn).
	a.checkAllowedWifi(wifi.SSID, wifi.BSSID)

	backendOnline := a.pingBackend()

	if backendOnline && wifi.SSID != "" && wifi.BSSID != "" {
		go a.reportWifi(wifi.SSID, wifi.BSSID)
		a.fetchWifiPolicy()
		if !a.isWifiAllowed(wifi.SSID, wifi.BSSID) {
			a.rejectUnauthorizedWifi(wifi.SSID, wifi.BSSID)
			return
		}
	}

	a.mu.Lock()
	a.backendOnline = backendOnline
	if backendOnline {
		a.onlineSecs += 10
		a.unsyncedOn += 10
	} else {
		a.offlineSecs += 10
		a.unsyncedOff += 10
	}
	a.mu.Unlock()

	a.saveStats()

	if backendOnline {
		a.connectWS()
		a.syncLogsToServer()
	} else {
		a.disconnectWS()
	}
}

// refreshNetworkStatus — đọc WiFi + ping server ngay (không cộng thời gian online/offline).
func (a *App) resolveClassRkID() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.dashboard.ClassRkID > 0 {
		return a.dashboard.ClassRkID
	}
	return 0
}

func (a *App) refreshStudentData() {
	a.mu.Lock()
	student := a.student
	a.mu.Unlock()
	if student == nil {
		return
	}
	a.fetchStudentStatus(student.StudentID)
	classID := a.resolveClassRkID()
	if classID > 0 {
		a.fetchAllowedApps(classID)
	} else {
		a.fetchAllowedApps(0)
	}
	a.refreshNetworkStatus()
}

// refreshNetworkStatus — đọc WiFi + ping server ngay (không cộng thời gian online/offline).
func (a *App) refreshNetworkStatus() {
	if !a.isMonitoringActive() || !a.CheckLoginStatus() {
		return
	}
	wifi := winapi.GetWifiConnection()
	backendOnline := a.pingBackend()
	a.mu.Lock()
	if strings.Contains(strings.ToLower(wifi.SSID), "redacted") {
		a.wifiSSID = "Chưa cấp quyền Vị Trí (macOS)"
	} else {
		a.wifiSSID = wifi.SSID
	}
	if strings.Contains(strings.ToLower(wifi.BSSID), "redacted") {
		a.wifiBSSID = "Chưa cấp quyền Vị Trí (macOS)"
	} else {
		a.wifiBSSID = wifi.BSSID
	}
	a.backendOnline = backendOnline
	a.mu.Unlock()
	if backendOnline {
		a.connectWS()
	}
}

func (a *App) pingBackend() bool {
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(API_BASE + "/api/health")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (a *App) syncLogsToServer() {
	if !a.isMonitoringActive() {
		return
	}
	a.mu.Lock()
	student := a.student
	unsyncedOn := a.unsyncedOn
	unsyncedOff := a.unsyncedOff
	ssid := a.wifiSSID
	a.mu.Unlock()

	if student == nil {
		return
	}

	go func() {
		a.fetchStudentStatus(student.StudentID)
		classID := a.resolveClassRkID()
		if classID <= 0 {
			classID = student.SystemID
		}
		a.fetchAllowedApps(classID)
	}()

	payload := map[string]any{
		"studentRkId":       student.StudentID,
		"classRkId":         a.resolveClassRkID(),
		"sessionDate":       time.Now().Format("2006-01-02"),
		"addOnlineSeconds":  unsyncedOn,
		"addOfflineSeconds": unsyncedOff,
		"wifiSsid":          ssid,
		"avatar":            student.Avatar,
	}

	bodyBytes, _ := json.Marshal(payload)
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(API_BASE+"/api/student/sync-log", "application/json", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		a.mu.Lock()
		a.unsyncedOn -= unsyncedOn
		a.unsyncedOff -= unsyncedOff
		a.mu.Unlock()
		a.saveStats()
		log.Printf("[SYNC] Synced %ds online, %ds offline to server.", unsyncedOn, unsyncedOff)
	}
}

func (a *App) reportBlockedApp(procName string, title string) {
	if !a.isMonitoringActive() {
		return
	}
	a.mu.Lock()
	student := a.student
	a.mu.Unlock()
	if student == nil || student.StudentID <= 0 {
		return
	}

	procKey := strings.ToLower(strings.TrimSpace(procName))
	if procKey == "" {
		return
	}
	if v, ok := blockedReportLast.Load(procKey); ok {
		if time.Since(v.(time.Time)) < 20*time.Second {
			return
		}
	}
	blockedReportLast.Store(procKey, time.Now())

	payload := map[string]any{
		"studentRkId": student.StudentID,
		"processName": procName,
		"windowTitle": title,
	}
	bodyBytes, _ := json.Marshal(payload)
	client := http.Client{Timeout: 4 * time.Second}
	resp, err := client.Post(API_BASE+"/api/student/report-blocked-app", "application/json", bytes.NewBuffer(bodyBytes))
	if err != nil {
		log.Printf("[CLIENT] report-blocked-app failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[CLIENT] report-blocked-app status %d: %s", resp.StatusCode, string(body))
		return
	}
	log.Printf("[CLIENT] report-blocked-app ok: %s", procName)
}

// reportUnauthorizedApp chụp ảnh màn hình làm bằng chứng rồi ghi nhận vi phạm "unauthorized_app"
// lên server — không tự động đóng ứng dụng của sinh viên.
func (a *App) reportUnauthorizedApp(procName string, title string) {
	a.mu.Lock()
	mode := a.dashboard.MonitorMode
	a.mu.Unlock()
	if !shouldRecordViolation(mode) {
		return
	}

	procKey := strings.ToLower(strings.TrimSpace(procName))
	if procKey == "" {
		return
	}
	if v, ok := unauthorizedAppViolationLast.Load(procKey); ok {
		if time.Since(v.(time.Time)) < 30*time.Second {
			return
		}
	}
	unauthorizedAppViolationLast.Store(procKey, time.Now())

	shot, err := screen.CaptureScreen()
	if err != nil {
		log.Printf("[CLIENT] CaptureScreen for unauthorized_app failed: %v", err)
		shot = ""
	}

	reason := fmt.Sprintf("Mở ứng dụng không được phép: %s (%s)", procName, title)
	a.reportViolationWithScreenshot("unauthorized_app", reason, shot)
}

func (a *App) reportWifi(ssid, bssid string) {
	if !a.isMonitoringActive() {
		return
	}
	a.mu.Lock()
	student := a.student
	a.mu.Unlock()
	if student == nil || strings.TrimSpace(ssid) == "" || strings.TrimSpace(bssid) == "" {
		return
	}
	key := winapi.BSSIDKey(bssid)
	if len(key) != 12 {
		return
	}
	if v, ok := wifiReportLast.Load(key); ok {
		if time.Since(v.(time.Time)) < 30*time.Second {
			return
		}
	}
	wifiReportLast.Store(key, time.Now())

	a.mu.Lock()
	classRkID := a.dashboard.ClassRkID
	examRoomID := uint(0)
	if a.dashboard.Exam != nil {
		examRoomID = a.dashboard.Exam.ExamRoomID
	}
	a.mu.Unlock()

	payload := map[string]any{
		"studentRkId": student.StudentID,
		"classRkId":   classRkID,
		"examRoomId":  examRoomID,
		"ssid":        ssid,
		"bssid":       bssid,
	}
	bodyBytes, _ := json.Marshal(payload)
	client := http.Client{Timeout: 4 * time.Second}
	resp, err := client.Post(API_BASE+"/api/student/report-wifi", "application/json", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

var (
	capturedURLsMu  sync.Mutex
	capturedURLsBuf []netproxy.CapturedURL
)

// handleCapturedURL — callback của netproxy mỗi khi bắt được 1 kết nối ra Internet; gom lại để gửi theo lô.
func (a *App) handleCapturedURL(cap netproxy.CapturedURL) {
	capturedURLsMu.Lock()
	capturedURLsBuf = append(capturedURLsBuf, cap)
	overflow := len(capturedURLsBuf) > 500
	capturedURLsMu.Unlock()
	if overflow {
		go a.flushCapturedURLs()
	}
	if cap.Blocked {
		// Host bị CẤM HẲN (proxy đã từ chối) -> ghi vi phạm kèm ảnh.
		a.reportBlockedHost(cap.Host)
		return
	}
	// Bắt vi phạm website cấm (chỉ ghi, không chặn): host chứa keyword blockedSites.
	a.checkBlockedSite(cap.Host)
}

// reportBlockedHost ghi vi phạm khi truy cập host bị cấm hẳn (throttle 30s/host).
func (a *App) reportBlockedHost(host string) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return
	}
	a.mu.Lock()
	mode := a.dashboard.MonitorMode
	a.mu.Unlock()
	if !shouldRecordViolation(mode) {
		return
	}
	if v, ok := blockedSiteViolationLast.Load("host:" + host); ok {
		if time.Since(v.(time.Time)) < 30*time.Second {
			return
		}
	}
	blockedSiteViolationLast.Store("host:"+host, time.Now())
	shot, err := screen.CaptureScreen()
	if err != nil {
		shot = ""
	}
	a.reportViolationWithScreenshot("blocked_host", fmt.Sprintf("Truy cập domain/IP bị cấm (đã chặn): %s", host), shot)
}

// splitCSVLower tách chuỗi CSV -> slice keyword thường, bỏ rỗng/trùng.
func splitCSVLower(s string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		k := strings.ToLower(strings.TrimSpace(part))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}

// checkBlockedSite so host với danh sách website cấm; khớp -> ghi vi phạm 1 lần/30s/keyword.
func (a *App) checkBlockedSite(host string) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return
	}
	a.mu.Lock()
	sites := a.blockedSites
	mode := a.dashboard.MonitorMode
	a.mu.Unlock()
	if len(sites) == 0 || !shouldRecordViolation(mode) {
		return
	}
	for _, kw := range sites {
		if kw == "" || !strings.Contains(host, kw) {
			continue
		}
		if v, ok := blockedSiteViolationLast.Load(kw); ok {
			if time.Since(v.(time.Time)) < 30*time.Second {
				return
			}
		}
		blockedSiteViolationLast.Store(kw, time.Now())
		shot, err := screen.CaptureScreen()
		if err != nil {
			shot = ""
		}
		reason := fmt.Sprintf("Truy cập website bị cấm: %s", host)
		a.reportViolationWithScreenshot("blocked_site", reason, shot)
		return
	}
}

// capturedURLReporterLoop định kỳ gửi lô URL đã bắt được qua local proxy lên server (mỗi 15 giây).
func (a *App) capturedURLReporterLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		a.flushCapturedURLs()
	}
}

func (a *App) flushCapturedURLs() {
	capturedURLsMu.Lock()
	batch := capturedURLsBuf
	capturedURLsBuf = nil
	capturedURLsMu.Unlock()

	if len(batch) == 0 || !a.isMonitoringActive() || !a.CheckLoginStatus() {
		return
	}
	a.mu.Lock()
	student := a.student
	classRkID := a.dashboard.ClassRkID
	examRoomID := uint(0)
	if a.dashboard.Exam != nil {
		examRoomID = a.dashboard.Exam.ExamRoomID
	}
	a.mu.Unlock()
	if student == nil || student.StudentID <= 0 {
		return
	}

	items := make([]map[string]any, 0, len(batch))
	for _, c := range batch {
		items = append(items, map[string]any{
			"method":   c.Method,
			"host":     c.Host,
			"url":      c.URL,
			"clientAt": c.Time.Format(time.RFC3339),
		})
	}

	payload := map[string]any{
		"studentRkId": student.StudentID,
		"classRkId":   classRkID,
		"examRoomId":  examRoomID,
		"items":       items,
	}
	bodyBytes, _ := json.Marshal(payload)
	client := http.Client{Timeout: 8 * time.Second}
	resp, err := client.Post(API_BASE+"/api/student/report-visited-urls", "application/json", bytes.NewBuffer(bodyBytes))
	if err != nil {
		log.Printf("[NETPROXY] report-visited-urls failed: %v", err)
		return
	}
	_ = resp.Body.Close()
}

// openAppsReporterLoop định kỳ lấy danh sách ứng dụng đang mở (có giao diện) và gửi về server.
func (a *App) openAppsReporterLoop() {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		a.reportOpenApps()
	}
}

func (a *App) reportOpenApps() {
	if !a.isMonitoringActive() || !a.CheckLoginStatus() {
		return
	}
	a.mu.Lock()
	student := a.student
	classRkID := a.dashboard.ClassRkID
	examRoomID := uint(0)
	if a.dashboard.Exam != nil {
		examRoomID = a.dashboard.Exam.ExamRoomID
	}
	a.mu.Unlock()
	if student == nil || student.StudentID <= 0 {
		return
	}

	windows := blocker.Instance.SnapshotOpenApps()
	apps := make([]map[string]string, 0, len(windows))
	for _, w := range windows {
		apps = append(apps, map[string]string{
			"processName": w.ProcessName,
			"windowTitle": w.Title,
		})
	}

	payload := map[string]any{
		"studentRkId": student.StudentID,
		"classRkId":   classRkID,
		"examRoomId":  examRoomID,
		"apps":        apps,
	}
	bodyBytes, _ := json.Marshal(payload)
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(API_BASE+"/api/student/report-open-apps", "application/json", bytes.NewBuffer(bodyBytes))
	if err != nil {
		log.Printf("[CLIENT] report-open-apps failed: %v", err)
		return
	}
	_ = resp.Body.Close()
}

func (a *App) fetchWifiPolicy() {
	client := http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(API_BASE + "/api/student/wifi-policy")
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var res struct {
		Enforce  bool `json:"enforce"`
		Accepted []struct {
			SSID  string `json:"ssid"`
			BSSID string `json:"bssid"`
		} `json:"accepted"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return
	}
	allowed := make(map[string]bool, len(res.Accepted))
	for _, item := range res.Accepted {
		k := winapi.BSSIDKey(item.BSSID)
		if len(k) == 12 {
			allowed[k] = true
		}
	}
	a.mu.Lock()
	a.wifiEnforce = res.Enforce
	a.acceptedWifi = allowed
	a.mu.Unlock()
}

func (a *App) isWifiAllowed(ssid, bssid string) bool {
	a.mu.Lock()
	enforce := a.wifiEnforce
	allowed := a.acceptedWifi
	a.mu.Unlock()

	if !enforce || len(allowed) == 0 {
		return true
	}
	// Bypass Wi-Fi check if it's redacted by macOS privacy controls
	if strings.Contains(strings.ToLower(ssid), "redacted") || strings.Contains(strings.ToLower(bssid), "redacted") {
		return true
	}
	bKey := winapi.BSSIDKey(bssid)
	if bKey == "" || len(bKey) != 12 {
		return false
	}
	return allowed[bKey]
}

func (a *App) rejectUnauthorizedWifi(ssid, bssid string) {
	a.mu.Lock()
	if a.wifiRejected {
		a.mu.Unlock()
		return
	}
	a.wifiRejected = true
	a.mu.Unlock()

	a.reportViolation("wifi", fmt.Sprintf("WiFi không được phép: %s (%s)", ssid, bssid))
	a.showQuitDialog(
		"WiFi không được phép",
		fmt.Sprintf("Điểm phát WiFi \"%s\" (%s) không được phép.\n\nCó thể là mạng giả mạo (hotspot trùng tên). Hãy kết nối đúng WiFi của trường rồi mở lại ứng dụng.", ssid, bssid),
	)
}

func (a *App) fetchAllowedApps(classId int64) {
	a.fetchAppsMu.Lock()
	defer a.fetchAppsMu.Unlock()

	a.mu.Lock()
	studentID := int64(0)
	if a.student != nil {
		studentID = a.student.StudentID
	}
	a.mu.Unlock()

	url := fmt.Sprintf("%s/api/classes/%d/allowed-apps?studentId=%d", API_BASE, classId, studentID)
	client := http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var res struct {
			Keywords     string `json:"keywords"`
			BlockedSites string `json:"blockedSites"`
			BlockedHosts string `json:"blockedHosts"`
			AllowedWifi  string `json:"allowedWifi"`
			Exit         bool   `json:"exit"`
			Reason       string `json:"reason"`
			ExamMode     bool   `json:"examMode"`
			browserConfig
		}
		if err := json.NewDecoder(resp.Body).Decode(&res); err == nil {
			if res.Exit {
				a.mu.Lock()
				stillExam := a.dashboard.MonitorMode == "exam"
				a.mu.Unlock()
				if stillExam {
					return
				}
				reason := res.Reason
				if reason == "" {
					reason = "Không đáp ứng điều kiện học tập (chưa cấu hình ứng dụng được phép hoặc ngoài giờ học)."
				}
				log.Printf("[CLIENT] Conditions not met: %s. Blocker suspended.", reason)

				a.mu.Lock()
				a.allowedApps = ""
				a.blockedSites = nil
				a.allowedWifi = nil
				a.statusMsg = reason
				a.mu.Unlock()
				netproxy.SetBlockedHosts(nil)
				a.applyBrowserPolicy(browser.ModeFree, res.browserConfig)

				blocker.Instance.Stop()
				screenrecord.Stop()
				netproxy.Stop()
				_ = netproxy.DisableSystemProxy()
				return
			}
			a.mu.Lock()
			a.allowedApps = res.Keywords
			a.blockedSites = splitCSVLower(res.BlockedSites)
			a.allowedWifi = splitCSVLower(res.AllowedWifi)
			if a.dashboard.MonitorLabel != "" {
				a.statusMsg = a.dashboard.MonitorLabel
			} else {
				a.statusMsg = "Đang trong giờ học"
			}
			a.mu.Unlock()
			netproxy.SetBlockedHosts(splitCSVLower(res.BlockedHosts))
			if res.ExamMode {
				a.applyBrowserPolicy(browser.ModeExam, res.browserConfig)
			} else {
				a.applyBrowserPolicy(browser.ModeLearning, res.browserConfig)
			}

			log.Printf("[CLIENT] Allowed apps fetched from server: %s", res.Keywords)
			blocker.Instance.SetKeywords(res.Keywords)
			blocker.Instance.Start()
			screenrecord.Start()
			if err := netproxy.Start(a.handleCapturedURL); err != nil {
				log.Printf("[NETPROXY] failed to start local proxy: %v", err)
			} else if err := netproxy.EnableSystemProxy(netproxy.ListenAddr); err != nil {
				log.Printf("[NETPROXY] failed to enable system proxy: %v", err)
			}
		}
	}
}

func (a *App) fetchStudentStatus(studentID int64) {
	if studentID <= 0 {
		return
	}
	client := http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(fmt.Sprintf("%s/api/student/status?studentRkId=%d", API_BASE, studentID))
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}

	var res StudentDashboardSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return
	}

	a.mu.Lock()
	prevMode := a.dashboard.MonitorMode
	a.dashboard = res
	if res.MonitorLabel != "" {
		a.statusMsg = res.MonitorLabel
	}
	if res.ClassName != "" && res.MonitorMode == "learning" {
		a.statusMsg = fmt.Sprintf("%s · %s", res.ClassName, res.MonitorLabel)
	} else if res.ClassName != "" && res.MonitorMode == "outside_schedule" {
		a.statusMsg = fmt.Sprintf("%s · Ngoài giờ học", res.ClassName)
	} else if res.ClassName != "" && res.MonitorMode == "not_configured" {
		a.statusMsg = fmt.Sprintf("%s · %s", res.ClassName, res.MonitorLabel)
	} else if res.MonitorMode == "exam" {
		a.statusMsg = res.MonitorLabel
	}
	a.mu.Unlock()

	if prevMode != res.MonitorMode {
		guard.SuppressFor(4 * time.Second)
	}
}

func (a *App) connectWS() {
	if !a.CheckLoginStatus() {
		return
	}
	a.mu.Lock()
	if a.wsConnected || a.wsConnecting || a.student == nil {
		a.mu.Unlock()
		return
	}
	a.wsConnecting = true
	student := a.student
	classID := a.dashboard.ClassRkID
	if classID <= 0 {
		classID = student.SystemID
	}
	backoff := a.wsBackoff
	if backoff <= 0 {
		backoff = 500 * time.Millisecond
	}
	examRoomID := uint(0)
	if a.dashboard.Exam != nil {
		examRoomID = a.dashboard.Exam.ExamRoomID
	}
	wifi := a.wifiSSID
	a.mu.Unlock()

	wsUrl := fmt.Sprintf("%s/ws?role=student&studentId=%d&classId=%d&examRoomId=%d&wifi=%s", getWsUrl(API_BASE), student.StudentID, classID, examRoomID, url.QueryEscape(wifi))
	dialer := websocket.Dialer{HandshakeTimeout: 4 * time.Second}
	conn, _, err := dialer.Dial(wsUrl, nil)
	if err != nil {
		a.mu.Lock()
		a.wsConnecting = false
		delay := backoff
		next := delay * 2
		if next > 10*time.Second {
			next = 10 * time.Second
		}
		a.wsBackoff = next
		a.mu.Unlock()
		log.Printf("[WS] Dial failed: %v — retry in %v", err, delay)
		time.AfterFunc(delay, func() {
			if a.isMonitoringActive() && a.CheckLoginStatus() {
				a.connectWS()
			}
		})
		return
	}

	_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	})

	a.mu.Lock()
	a.wsConn = conn
	a.wsConnected = true
	a.wsConnecting = false
	a.wsBackoff = 500 * time.Millisecond
	a.mu.Unlock()

	log.Println("[WS] Connected to proctor websocket hub.")
	a.markRunLock()

	pingDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-pingDone:
				return
			case <-ticker.C:
				a.wsWriteMu.Lock()
				err := conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
				a.wsWriteMu.Unlock()
				if err != nil {
					return
				}
				// App-level ping (một số proxy không forward WS control frames đúng).
				_ = a.safeWriteWS(map[string]any{"event": "client:ping", "data": map[string]any{}})
			}
		}
	}()

	go func() {
		defer close(pingDone)
		defer func() {
			a.disconnectWS(conn)
			a.mu.Lock()
			delay := a.wsBackoff
			if delay <= 0 {
				delay = 500 * time.Millisecond
			}
			next := delay * 2
			if next > 10*time.Second {
				next = 10 * time.Second
			}
			a.wsBackoff = next
			a.mu.Unlock()
			log.Printf("[WS] Disconnected — reconnect in %v", delay)
			time.AfterFunc(delay, func() {
				if a.isMonitoringActive() && a.CheckLoginStatus() {
					a.connectWS()
				}
			})
		}()
		for {
			var msg struct {
				Event string         `json:"event"`
				Data  map[string]any `json:"data"`
			}
			err := conn.ReadJSON(&msg)
			if err != nil {
				log.Printf("[WS] ReadJSON error: %v", err)
				break
			}
			_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
			if msg.Event == "client:pong" {
				continue
			}
			log.Printf("[WS] Received event: %s", msg.Event)

			switch msg.Event {
			case "start_screenshot_stream":
				a.startScreenshotStream(fpsFromMsg(msg.Data), qualityFromMsg(msg.Data))
			case "stop_screenshot_stream":
				a.stopScreenshotStream()
			case "start_webcam_stream":
				a.startWebcamStream()
			case "stop_webcam_stream":
				a.stopWebcamStream()
			case "screen_record:request_upload":
				go a.uploadScreenRecord()
			case "chat:message":
				senderRole, _ := msg.Data["senderRole"].(string)
				if senderRole == "staff" {
					from := "Giảng viên"
					if n, ok := msg.Data["staffName"].(string); ok && n != "" {
						from = n
					}
					preview := ""
					if b, ok := msg.Data["body"].(string); ok {
						preview = b
					}
					if staffVal, ok := msg.Data["staffId"].(float64); ok {
						a.mu.Lock()
						a.replyStaffID = uint(staffVal)
						a.chatUnread++
						a.mu.Unlock()
					}
					a.alertChatIncoming(from, preview)
				}
				runtime.EventsEmit(a.ctx, "chat:message", msg.Data)
			case "exam:paper-sent":
				runtime.EventsEmit(a.ctx, "exam:paper-sent", msg.Data)
				a.mu.Lock()
				if a.dashboard.Exam != nil {
					a.dashboard.Exam.PaperSent = true
					if t, ok := msg.Data["paperTitle"].(string); ok {
						a.dashboard.Exam.PaperTitle = t
					}
				}
				a.mu.Unlock()
			}
		}
	}()
}

func (a *App) disconnectWS(expectedConn ...*websocket.Conn) {
	var shouldStopStreams bool

	a.mu.Lock()
	if len(expectedConn) > 0 && expectedConn[0] != nil {
		if a.wsConn != expectedConn[0] {
			a.mu.Unlock()
			return
		}
	}
	if a.wsConn != nil {
		_ = a.wsConn.Close()
		a.wsConn = nil
	}
	a.wsConnected = false
	a.wsConnecting = false
	shouldStopStreams = a.isStreamingSc || a.isStreamingCam
	a.mu.Unlock()

	if shouldStopStreams {
		a.stopScreenshotStream()
		a.stopWebcamStream()
	}
}

// fpsFromMsg đọc fps giám sát viên yêu cầu (kèm trong lệnh start). Rỗng -> 0 =
// dùng mặc định (nhịp thấp) phía chọn interval.
func fpsFromMsg(data map[string]any) int {
	if data == nil {
		return 0
	}
	switch v := data["fps"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

// fpsToIntervalMs quy FPS -> chu kỳ ms, kẹp [1..30] fps. 0/không hợp lệ -> 2 fps
// (mặc định thấp, nhẹ băng thông).
func fpsToIntervalMs(fps int) int64 {
	if fps <= 0 {
		fps = 2
	}
	if fps > 30 {
		fps = 30
	}
	return int64(1000 / fps)
}

// qualityFromMsg đọc chất lượng JPEG yêu cầu (kèm lệnh start). Rỗng -> 0 = default.
func qualityFromMsg(data map[string]any) int {
	if data == nil {
		return 0
	}
	switch v := data["quality"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

// clampQuality kẹp [20..95]; 0/không hợp lệ -> 50 (mặc định).
func clampQuality(q int) int64 {
	if q <= 0 {
		q = 50
	}
	if q < 20 {
		q = 20
	}
	if q > 95 {
		q = 95
	}
	return int64(q)
}

func (a *App) startScreenshotStream(fps int, quality int) {
	// Cập nhật nhịp + chất lượng trước — nếu đang stream thì đổi LIVE (không dừng/mở lại).
	a.scIntervalMs.Store(fpsToIntervalMs(fps))
	a.scQuality.Store(clampQuality(quality))

	a.mu.Lock()
	if a.isStreamingSc {
		a.mu.Unlock()
		log.Printf("[WS] Screenshot updated -> %d fps, quality %d", fps, quality)
		return
	}
	a.isStreamingSc = true
	a.streamScStop = make(chan struct{})
	stop := a.streamScStop
	a.mu.Unlock()

	go func() {
		for {
			q := int(a.scQuality.Load())
			frame, err := screen.CaptureScreenQ(q)
			if err == nil {
				_ = a.safeWriteWS(map[string]any{
					"event": "screenshot_stream_frame",
					"data": map[string]any{
						"imageBuffer": frame,
					},
				})
			}
			d := time.Duration(a.scIntervalMs.Load()) * time.Millisecond
			if d < 50*time.Millisecond {
				d = 500 * time.Millisecond
			}
			select {
			case <-stop:
				return
			case <-time.After(d):
			}
		}
	}()
	log.Printf("[WS] Screenshot screen-streaming started (%d fps).", fps)
}

func (a *App) stopScreenshotStream() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.isStreamingSc {
		return
	}
	a.isStreamingSc = false
	close(a.streamScStop)
	log.Println("[WS] Screenshot screen-streaming stopped.")
}

func (a *App) startWebcamStream() {
	a.mu.Lock()
	if a.isStreamingCam {
		a.mu.Unlock()
		return
	}
	a.isStreamingCam = true
	a.streamCamStop = make(chan struct{})
	a.mu.Unlock()

	if !camera.IsNative {
		log.Println("[WS] Platform is non-darwin, delegating webcam stream to frontend events")
		runtime.EventsEmit(a.ctx, "start_webcam_stream")
		return
	}

	if err := camera.StartCapture(); err != nil {
		log.Printf("[WS] Failed to start native camera: %v", err)
		a.mu.Lock()
		a.isStreamingCam = false
		a.mu.Unlock()
		return
	}

	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		emptyCount := 0
		sentCount := 0

		for {
			select {
			case <-ticker.C:
				frame := camera.GetFrame()
				if frame == "" {
					emptyCount++
					if emptyCount <= 20 || emptyCount%100 == 0 {
						log.Printf("[WS] Webcam: no frame available (empty count: %d)", emptyCount)
					}
					continue
				}
				emptyCount = 0
				sentCount++

				err := a.safeWriteWS(map[string]any{
					"event": "webcam_stream_frame",
					"data": map[string]any{
						"imageBuffer": frame,
					},
				})
				if sentCount <= 5 {
					log.Printf("[WS] Webcam frame #%d sent (len=%d, err=%v)", sentCount, len(frame), err)
				}
			case <-a.streamCamStop:
				return
			}
		}
	}()
	log.Println("[WS] Webcam native streaming started.")
}

func (a *App) stopWebcamStream() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.isStreamingCam {
		return
	}
	a.isStreamingCam = false
	if a.streamCamStop != nil {
		close(a.streamCamStop)
	}

	if !camera.IsNative {
		log.Println("[WS] Platform is non-darwin, stopping frontend webcam stream via event")
		runtime.EventsEmit(a.ctx, "stop_webcam_stream")
		return
	}

	camera.StopCapture()
	log.Println("[WS] Webcam native streaming stopped.")
}

func (a *App) alertChatIncoming(from, preview string) {
	if preview == "" {
		preview = "Bạn có tin nhắn mới"
	}
	if len(preview) > 120 {
		preview = preview[:120] + "…"
	}
	winapi.PlayNotifySound()
	winapi.ActivateAppWindow("Rikkei Lms Connect")
	runtime.WindowUnminimise(a.ctx)
	runtime.WindowShow(a.ctx)
	a.mu.Lock()
	unread := a.chatUnread
	staffID := a.replyStaffID
	a.mu.Unlock()
	runtime.EventsEmit(a.ctx, "chat:notify", map[string]any{
		"unread":  unread,
		"preview": preview,
		"from":    from,
		"staffId": staffID,
	})
}

// UnlockChatAudio — gọi từ UI sau lần click đầu để mở khóa Web Audio (dự phòng)
func (a *App) UnlockChatAudio() {}

func (a *App) GetChatUnread() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.chatUnread
}

func (a *App) ClearChatUnread() {
	a.mu.Lock()
	a.chatUnread = 0
	a.mu.Unlock()
}

func (a *App) GetChatConversations() ([]map[string]any, error) {
	a.mu.Lock()
	student := a.student
	a.mu.Unlock()
	if student == nil {
		return nil, errors.New("chưa đăng nhập")
	}
	url := fmt.Sprintf("%s/api/student/chat/conversations?studentRkId=%d", API_BASE, student.StudentID)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Data, nil
}

func (a *App) GetChatMessages(staffID uint) ([]map[string]any, error) {
	a.mu.Lock()
	student := a.student
	a.mu.Unlock()
	if student == nil {
		return nil, errors.New("chưa đăng nhập")
	}
	url := fmt.Sprintf("%s/api/student/chat/messages?studentRkId=%d&staffId=%d", API_BASE, student.StudentID, staffID)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload struct {
		Data         []map[string]any `json:"data"`
		ReplyStaffID uint             `json:"replyStaffId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if staffID > 0 {
		a.mu.Lock()
		a.replyStaffID = staffID
		a.mu.Unlock()
	} else if payload.ReplyStaffID > 0 {
		a.mu.Lock()
		a.replyStaffID = payload.ReplyStaffID
		a.mu.Unlock()
	}
	a.ClearChatUnread()
	return payload.Data, nil
}

func (a *App) SendChatMessage(body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return errors.New("tin nhắn trống")
	}
	a.mu.Lock()
	student := a.student
	staffID := a.replyStaffID
	a.mu.Unlock()
	if student == nil {
		return errors.New("chưa đăng nhập")
	}
	payload := map[string]any{
		"studentRkId": student.StudentID,
		"staffId":     staffID,
		"body":        body,
	}
	b, _ := json.Marshal(payload)
	resp, err := http.Post(API_BASE+"/api/student/chat/messages", "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var errBody map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		if msg, ok := errBody["error"].(string); ok {
			return errors.New(msg)
		}
		return fmt.Errorf("gửi tin thất bại (%d)", resp.StatusCode)
	}
	return nil
}

func (a *App) examStudentContext() (int64, *StudentExamSnapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.student == nil {
		return 0, nil, errors.New("chưa đăng nhập")
	}
	if a.dashboard.Exam == nil {
		return 0, nil, errors.New("không trong giờ thi")
	}
	return a.student.StudentID, a.dashboard.Exam, nil
}

// ReturnToDashboard quay lại giao diện chính của app (giữ cookie WebView2).
func (a *App) ReturnToDashboard() {
	a.setLocalBrowserActive(false)
	guard.SuppressFor(5 * time.Second)
	runtime.WindowReloadApp(a.ctx)
}

func (a *App) setLocalBrowserActive(on bool) {
	a.mu.Lock()
	a.localBrowserActive = on
	a.mu.Unlock()
}

// ReloadExamPage — F5: làm mới trang / iframe đang mở (trắc nghiệm).
func (a *App) ReloadExamPage() {
	guard.SuppressFor(3 * time.Second)
	runtime.WindowExecJS(a.ctx, `
		(function(){
			try {
				if (typeof window.refreshExam === 'function') { window.refreshExam(); return; }
				var f = document.getElementById('exam-frame');
				if (f) {
					try { f.contentWindow.location.reload(); }
					catch (e) { var s = f.src; f.src = 'about:blank'; setTimeout(function(){ f.src = s; }, 50); }
					return;
				}
			} catch (e) {}
			location.reload();
		})();
	`)
}

// ClearExamBrowserCache — chỉ xóa HTTP disk cache, giữ cookie/localStorage (không văng đăng nhập trang thi).
func (a *App) ClearExamBrowserCache() {
	guard.SuppressFor(8 * time.Second)
	a.purgeWebViewDiskCache()
	runtime.WindowExecJS(a.ctx, `
		(function(){
			try {
				if (typeof window.reloadFrameSoft === 'function') {
					window.reloadFrameSoft();
					return;
				}
				var f = document.getElementById('exam-frame');
				if (f) {
					try { f.contentWindow.location.reload(); }
					catch (e) {
						var s = f.getAttribute('src') || f.src;
						f.src = 'about:blank';
						setTimeout(function(){ f.src = s; }, 50);
					}
					return;
				}
			} catch (e) {}
			location.reload();
		})();
	`)
}

func (a *App) purgeWebViewDiskCache() {
	if a.webviewDataPath == "" {
		return
	}
	// Chỉ Cache tải tài nguyên — KHÔNG xóa Cookies / Local Storage / Service Worker (giữ phiên đăng nhập).
	subs := []string{
		filepath.Join("EBWebView", "Default", "Cache"),
		filepath.Join("EBWebView", "Default", "Code Cache"),
		filepath.Join("EBWebView", "Default", "GPUCache"),
	}
	for _, sub := range subs {
		path := filepath.Join(a.webviewDataPath, sub)
		if err := os.RemoveAll(path); err != nil {
			log.Printf("[CACHE] purge %s: %v", path, err)
		} else {
			log.Printf("[CACHE] purged %s", path)
		}
	}
}

func isLocalExamURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "127.0.0.1" || host == "localhost"
}

func (a *App) openExamWebView(targetURL, title string) error {
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return errors.New("không có nội dung để mở")
	}
	a.setLocalBrowserActive(false)
	// URL local (PDF/resource): khung exam-view. URL ngoài (quiz): mở top-level giữ first-party cookies/session.
	if isLocalExamURL(targetURL) {
		wrapper := fmt.Sprintf(
			"http://127.0.0.1:34115/exam-view?url=%s&title=%s",
			url.QueryEscape(targetURL),
			url.QueryEscape(title),
		)
		guard.SuppressFor(5 * time.Second)
		runtime.WindowExecJS(a.ctx, fmt.Sprintf("window.location.href = %q", wrapper))
		return nil
	}
	guard.SuppressFor(5 * time.Second)
	runtime.WindowExecJS(a.ctx, fmt.Sprintf("window.location.href = %q", targetURL))
	return nil
}

func (a *App) GetExamPaperViewURL() (string, error) {
	studentID, exam, err := a.examStudentContext()
	if err != nil {
		return "", err
	}
	if !exam.PaperSent {
		return "", errors.New("Giảng viên chưa gửi đề")
	}
	return fmt.Sprintf("%s/api/student/exam/download?studentRkId=%d&kind=pdf&view=1", API_BASE, studentID), nil
}

func (a *App) GetExamQuizViewURL() (string, error) {
	_, exam, err := a.examStudentContext()
	if err != nil {
		return "", err
	}
	quizURL := strings.TrimSpace(exam.QuizURL)
	if quizURL == "" {
		return "", errors.New("Phòng thi chưa cấu hình link trắc nghiệm")
	}
	return quizURL, nil
}

func (a *App) GetExamPaperFiles() (map[string]any, error) {
	studentID, exam, err := a.examStudentContext()
	if err != nil {
		return nil, err
	}
	if !exam.PaperSent {
		return nil, errors.New("Giảng viên chưa gửi đề")
	}
	apiURL := fmt.Sprintf("%s/api/student/exam/paper-files?studentRkId=%d", API_BASE, studentID)
	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("không tải được danh sách tài nguyên")
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	base := API_BASE
	if pdf, ok := payload["pdfUrl"].(string); ok && pdf != "" && !strings.HasPrefix(pdf, "http") {
		payload["pdfUrl"] = base + pdf
	}
	if raw, ok := payload["resources"].([]any); ok {
		for i, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if u, ok := m["url"].(string); ok && u != "" && !strings.HasPrefix(u, "http") {
				m["url"] = base + u
			}
			if u, ok := m["downloadUrl"].(string); ok && u != "" && !strings.HasPrefix(u, "http") {
				m["downloadUrl"] = base + u
			}
			raw[i] = m
		}
		payload["resources"] = raw
	}
	return payload, nil
}

func (a *App) OpenExamPaper() error {
	viewURL, err := a.GetExamPaperViewURL()
	if err != nil {
		return err
	}
	title := "Đề thi"
	a.mu.Lock()
	if a.dashboard.Exam != nil && strings.TrimSpace(a.dashboard.Exam.PaperTitle) != "" {
		title = strings.TrimSpace(a.dashboard.Exam.PaperTitle)
	}
	a.mu.Unlock()
	return a.openExamWebView(viewURL, title)
}

func (a *App) OpenExamQuiz() error {
	viewURL, err := a.GetExamQuizViewURL()
	if err != nil {
		return err
	}
	// Mở trực tiếp (không iframe) để cookie/localStorage đăng nhập web quiz là first-party.
	// F5 / Xóa cache / Về trang chính: menu Rikkei Lms Connect.
	return a.openExamWebView(viewURL, "Làm trắc nghiệm")
}

func (a *App) OpenExamResource(fileID uint) error {
	studentID, _, err := a.examStudentContext()
	if err != nil {
		return err
	}
	if fileID == 0 {
		return errors.New("tài nguyên không hợp lệ")
	}
	viewURL := fmt.Sprintf(
		"%s/api/student/exam/download?studentRkId=%d&kind=resource&fileId=%d&view=1",
		API_BASE, studentID, fileID,
	)
	title := "Tài nguyên đề thi"
	files, err := a.GetExamPaperFiles()
	if err == nil {
		if raw, ok := files["resources"].([]any); ok {
			for _, item := range raw {
				m, ok := item.(map[string]any)
				if !ok {
					continue
				}
				id, _ := m["id"].(float64)
				if uint(id) == fileID {
					if name, ok := m["fileName"].(string); ok && name != "" {
						title = name
					}
					break
				}
			}
		}
	}
	return a.openExamWebView(viewURL, title)
}

func examResourceDownloadDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	candidates := []string{
		filepath.Join(home, "Downloads"),
		filepath.Join(home, "Desktop"),
		home,
	}
	for _, dir := range candidates {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return dir, nil
		}
	}
	return "", errors.New("không tìm thấy thư mục Downloads hoặc Desktop")
}

func uniqueFilePath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(filepath.Base(path), ext)
	dir := filepath.Dir(path)
	for i := 1; i < 100; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return filepath.Join(dir, fmt.Sprintf("%s_%d%s", base, time.Now().Unix(), ext))
}

func openFolderInExplorer(filePath string) {
	guard.SuppressFor(5 * time.Second)
	dir := filepath.Dir(filePath)
	_ = exec.Command("explorer", dir).Start()
}

func (a *App) DownloadExamResource(fileID uint) (string, error) {
	studentID, _, err := a.examStudentContext()
	if err != nil {
		return "", err
	}
	if fileID == 0 {
		return "", errors.New("tài nguyên không hợp lệ")
	}
	fileName := fmt.Sprintf("resource_%d", fileID)
	files, err := a.GetExamPaperFiles()
	if err == nil {
		if raw, ok := files["resources"].([]any); ok {
			for _, item := range raw {
				m, ok := item.(map[string]any)
				if !ok {
					continue
				}
				id, _ := m["id"].(float64)
				if uint(id) == fileID {
					if name, ok := m["fileName"].(string); ok && name != "" {
						fileName = filepath.Base(name)
					}
					break
				}
			}
		}
	}
	dlURL := fmt.Sprintf(
		"%s/api/student/exam/download?studentRkId=%d&kind=resource&fileId=%d",
		API_BASE, studentID, fileID,
	)
	resp, err := http.Get(dlURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("không tải được tài nguyên")
	}
	dir, err := examResourceDownloadDir()
	if err != nil {
		return "", err
	}
	dest := uniqueFilePath(filepath.Join(dir, fileName))
	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return "", err
	}
	f.Close()
	openFolderInExplorer(dest)
	return dest, nil
}

// LoadExamViewFile tải file đề/tài nguyên qua Go (không mở URL trực tiếp trong WebView).
func (a *App) LoadExamViewFile(fileURL string) (map[string]any, error) {
	fileURL = strings.TrimSpace(fileURL)
	if !strings.HasPrefix(fileURL, API_BASE+"/api/student/exam/download") {
		return nil, errors.New("URL không hợp lệ")
	}
	if _, _, err := a.examStudentContext(); err != nil {
		return nil, err
	}
	resp, err := http.Get(fileURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("không tải được file")
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	mime := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if mime == "" {
		mime = "application/octet-stream"
	}
	// Bỏ charset nếu có
	if i := strings.Index(mime, ";"); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	return map[string]any{
		"data": base64.StdEncoding.EncodeToString(body),
		"mime": mime,
	}, nil
}

func (a *App) SubmitExamWork() (string, error) {
	a.mu.Lock()
	student := a.student
	exam := a.dashboard.Exam
	a.mu.Unlock()
	if student == nil {
		return "", errors.New("chưa đăng nhập")
	}
	if exam == nil {
		return "", errors.New("không trong giờ thi")
	}
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Chọn folder bài làm để nộp",
	})
	if err != nil {
		return "", err
	}
	if dir == "" {
		return "", errors.New("đã hủy")
	}
	zipPath := filepath.Join(os.TempDir(), fmt.Sprintf("exam_submit_%d_%d.zip", exam.ExamRoomID, time.Now().Unix()))
	if err := zipFolder(dir, zipPath); err != nil {
		return "", err
	}
	defer os.Remove(zipPath)

	f, err := os.Open(zipPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("studentRkId", fmt.Sprintf("%d", student.StudentID))
	part, err := w.CreateFormFile("submission", filepath.Base(zipPath))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", err
	}
	_ = w.Close()

	req, err := http.NewRequest("POST", API_BASE+"/api/student/exam/submit", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var res struct {
		OK       bool   `json:"ok"`
		FileName string `json:"fileName"`
		Error    string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&res)
	if resp.StatusCode >= 300 || !res.OK {
		if res.Error != "" {
			return "", errors.New(res.Error)
		}
		return "", errors.New("nộp bài thất bại")
	}
	a.mu.Lock()
	if a.dashboard.Exam != nil {
		a.dashboard.Exam.Submitted = true
	}
	a.mu.Unlock()
	return res.FileName, nil
}

func zipFolder(srcDir, destZip string) error {
	out, err := os.Create(destZip)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	defer zw.Close()
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = rel
		hdr.Method = zip.Deflate
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		rf, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(w, rf)
		rf.Close()
		return copyErr
	})
}
