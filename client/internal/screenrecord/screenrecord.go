// Package screenrecord ghi lại timelapse màn hình (1 khung hình/giây, JPEG chất lượng thấp)
// vào một thư mục ẩn trên máy — dùng làm bằng chứng khi giáo viên yêu cầu xem lại. Không tự
// mã hoá video (giữ client gọn nhẹ) — server sẽ ghép các khung hình này thành video thật khi
// nhận được ZIP upload theo yêu cầu.
package screenrecord

import (
	"archive/zip"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"client/internal/screen"
)

const (
	captureInterval = 1 * time.Second
	retentionDays   = 3
)

var (
	mu         sync.Mutex
	running    bool
	stopChan   chan struct{}
	rootDir    string
	dayDir     string
	frameIndex int
	initOnce   sync.Once
)

// Init khởi tạo thư mục lưu trữ ẩn (gọi 1 lần khi app khởi động) và dọn các phiên đã quá hạn.
func Init() {
	initOnce.Do(func() {
		configDir, err := os.UserConfigDir()
		if err != nil {
			configDir = os.TempDir()
		}
		mu.Lock()
		rootDir = filepath.Join(configDir, "SimpleCare", hiddenFolderName)
		mu.Unlock()

		if err := os.MkdirAll(rootDir, 0755); err != nil {
			log.Printf("[SCREENREC] failed to create storage dir: %v", err)
			return
		}
		hideDir(rootDir)
		CleanupOld()
	})
}

// Start bắt đầu ghi timelapse (no-op nếu đang chạy hoặc chưa Init).
func Start() {
	mu.Lock()
	if running || rootDir == "" {
		mu.Unlock()
		return
	}
	day := time.Now().Format("20060102")
	dir := filepath.Join(rootDir, day)
	if err := os.MkdirAll(dir, 0755); err != nil {
		mu.Unlock()
		log.Printf("[SCREENREC] failed to create day dir: %v", err)
		return
	}
	hideDir(dir)
	dayDir = dir
	frameIndex = countExistingFrames(dir)
	running = true
	stopChan = make(chan struct{})
	stop := stopChan
	mu.Unlock()

	go captureLoop(stop)
	log.Println("[SCREENREC] Started")
}

// Stop dừng ghi timelapse (no-op nếu chưa chạy).
func Stop() {
	mu.Lock()
	if !running {
		mu.Unlock()
		return
	}
	running = false
	close(stopChan)
	mu.Unlock()
	log.Println("[SCREENREC] Stopped")
}

func captureLoop(stop chan struct{}) {
	ticker := time.NewTicker(captureInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			captureFrame()
		}
	}
}

func captureFrame() {
	mu.Lock()
	dir := dayDir
	idx := frameIndex
	frameIndex++
	mu.Unlock()
	if dir == "" {
		return
	}

	dataURL, err := screen.CaptureScreen()
	if err != nil {
		return
	}
	b64 := dataURL
	if i := strings.Index(dataURL, ","); i != -1 {
		b64 = dataURL[i+1:]
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return
	}

	fileName := fmt.Sprintf("frame_%06d.jpg", idx)
	_ = os.WriteFile(filepath.Join(dir, fileName), raw, 0644)
}

func countExistingFrames(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	return len(entries)
}

// ZipTodayToFile ghi các khung hình hiện có ra 1 FILE zip tạm, nén STREAMING từng
// ảnh (chỉ giữ 1 ảnh trong RAM tại một thời điểm) — tránh hết bộ nhớ/crash khi
// buổi dài (khác ZipToday nén cả khối vào RAM). Trả path zip tạm + ngày + danh
// sách tên khung đã nén (xoá sau khi upload thành công). Caller tự xoá file tạm.
// maxFrames <= 0 = tất cả; > 0 = chỉ lấy tối đa maxFrames khung CŨ NHẤT (1 đoạn) để
// mỗi lần upload không quá lớn (tránh vượt giới hạn body của server / kẹt backlog).
func ZipTodayToFile(maxFrames int) (string, string, []string, error) {
	mu.Lock()
	dir := dayDir
	mu.Unlock()
	if dir == "" {
		return "", "", nil, fmt.Errorf("no active recording session")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", nil, err
	}
	tmp, err := os.CreateTemp("", "screenrec_*.zip")
	if err != nil {
		return "", "", nil, err
	}
	zw := zip.NewWriter(tmp)
	names := []string{}
	for _, e := range entries {
		name := strings.ToLower(e.Name())
		if e.IsDir() || !strings.HasSuffix(name, ".jpg") {
			continue
		}
		if maxFrames > 0 && len(names) >= maxFrames {
			break // đủ 1 đoạn — phần còn lại để lần upload sau
		}
		src, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		w, err := zw.Create(e.Name())
		if err != nil {
			src.Close()
			continue
		}
		_, cpErr := io.Copy(w, src)
		src.Close()
		if cpErr == nil {
			names = append(names, e.Name())
		}
	}
	cerr := zw.Close()
	_ = tmp.Close()
	if cerr != nil {
		_ = os.Remove(tmp.Name())
		return "", "", nil, cerr
	}
	if len(names) == 0 {
		_ = os.Remove(tmp.Name())
		return "", "", nil, fmt.Errorf("no frames to upload")
	}
	return tmp.Name(), filepath.Base(dir), names, nil
}

// RemoveFrames xoá các khung hình đã upload xong (gọi SAU khi gửi thành công) để
// lần sau chỉ gửi phần mới. Chỉ xoá đúng các tên đã nén, không đụng khung mới.
func RemoveFrames(names []string) {
	mu.Lock()
	dir := dayDir
	mu.Unlock()
	if dir == "" {
		return
	}
	for _, n := range names {
		_ = os.Remove(filepath.Join(dir, n))
	}
}

// CleanupOld xoá các thư mục phiên ghi (theo ngày) đã cũ hơn retentionDays.
func CleanupOld() {
	mu.Lock()
	dir := rootDir
	mu.Unlock()
	if dir == "" {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	for _, e := range entries {
		if !e.IsDir() || len(e.Name()) != 8 {
			continue
		}
		t, err := time.ParseInLocation("20060102", e.Name(), time.Local)
		if err != nil {
			continue
		}
		if t.Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}
