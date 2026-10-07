//go:build darwin

package guard

import (
	"sync"
	"time"

	"github.com/kbinani/screenshot"
)

var (
	guardOnce               sync.Once
	guardViolation           func(kind, reason string)
	guardStop               chan struct{}
	suppressMu              sync.Mutex
	suppressViolationsUntil time.Time
)

// SuppressFor tạm không thoát app khi WebView/Explorer chuyển màn hình nội bộ.
func SuppressFor(d time.Duration) {
	if d <= 0 {
		return
	}
	suppressMu.Lock()
	next := time.Now().Add(d)
	if next.After(suppressViolationsUntil) {
		suppressViolationsUntil = next
	}
	suppressMu.Unlock()
}

func violationsSuppressed() bool {
	suppressMu.Lock()
	defer suppressMu.Unlock()
	return time.Now().Before(suppressViolationsUntil)
}

// Start giám sát môi trường macOS — vi phạm thì gọi onViolation(kind, reason).
func Start(onViolation func(kind, reason string)) {
	guardOnce.Do(func() {
		if onViolation == nil {
			return
		}
		guardViolation = onViolation
		guardStop = make(chan struct{})

		if screenshot.NumActiveDisplays() > 1 {
			onViolation("multi_monitor", "Phát hiện nhiều hơn 1 màn hình. Vui lòng chỉ dùng một màn hình khi chạy Rikkei Lms Connect.")
			return
		}

		go pollLoop()
	})
}

func Stop() {
	if guardStop != nil {
		select {
		case <-guardStop:
			// already closed
		default:
			close(guardStop)
		}
	}
}

func pollLoop() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-guardStop:
			return
		case <-ticker.C:
			checkEnvironment()
		}
	}
}

func checkEnvironment() {
	if guardViolation == nil || violationsSuppressed() {
		return
	}
	if n := screenshot.NumActiveDisplays(); n > 1 {
		guardViolation("multi_monitor", "Phát hiện nhiều hơn 1 màn hình. Vui lòng rút/bật tắt màn hình phụ.")
		return
	}
}
