package browser

import (
	_ "embed"
	"log"
	"os"
	"path/filepath"
)

//go:embed toolbar.html
var toolbarHTML string

// envChild đánh dấu tiến trình được Client chạy ở chế độ trình duyệt.
const envChild = "RK_BROWSER_CHILD"

// IsChild: main() gọi sớm để rẽ sang Run() thay vì mở Client.
func IsChild() bool { return os.Getenv(envChild) == "1" }

// dataDir: thư mục hồ sơ trình duyệt (cookie, cache) — tách khỏi WebView của Client.
func dataDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	d := filepath.Join(base, "SimpleCare", "Browser")
	_ = os.MkdirAll(d, 0o755)
	return d
}

// Run chạy trình duyệt (trong tiến trình con) tới khi cửa sổ đóng. stdout dành cho
// giao tiếp với Client nên log ghi ra file riêng.
func Run() {
	if f, err := os.OpenFile(filepath.Join(dataDir(), "browser.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		log.SetOutput(f)
		defer f.Close()
	}
	log.Printf("--- browser started pid=%d ---", os.Getpid())
	c := newCore(os.Stdout)
	if err := runHost(c, os.Getenv("RK_BROWSER_URL")); err != nil {
		log.Printf("[BROWSER] host error: %v", err)
	}
	log.Printf("--- browser exited ---")
}
