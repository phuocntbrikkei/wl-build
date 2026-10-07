package browser

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
)

// Launcher chạy ở Client: mở/giữ tiến trình trình duyệt, đẩy chính sách xuống và
// nhận lại các lượt truy cập.
type Launcher struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	policy  Policy
	hasPol  bool
	onVisit func(Visit)
}

// NewLauncher: onVisit được gọi (ở goroutine riêng) cho mỗi lượt mở trang.
func NewLauncher(onVisit func(Visit)) *Launcher {
	return &Launcher{onVisit: onVisit}
}

// Running cho biết cửa sổ trình duyệt đang mở.
func (l *Launcher) Running() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cmd != nil
}

// Open mở trình duyệt (nếu chưa mở) tại url ("" = trang đầu); đã mở thì đưa cửa
// sổ lên trước và mở url.
func (l *Launcher) Open(url string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cmd != nil {
		return l.writeLocked(parentMsg{Type: "open", URL: url})
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), envChild+"=1", "RK_BROWSER_URL="+url)
	hideChildConsole(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	l.cmd, l.stdin = cmd, stdin
	log.Printf("[BROWSER] started pid=%d", cmd.Process.Pid)
	if l.hasPol {
		_ = l.writeLocked(parentMsg{Type: "policy", Policy: &l.policy})
	}
	go l.readVisits(stdout)
	go func() {
		err := cmd.Wait()
		log.Printf("[BROWSER] exited: %v", err)
		l.mu.Lock()
		if l.cmd == cmd {
			l.cmd, l.stdin = nil, nil
		}
		l.mu.Unlock()
	}()
	return nil
}

// SetPolicy lưu chính sách hiện hành và đẩy ngay xuống trình duyệt nếu đang mở.
func (l *Launcher) SetPolicy(p Policy) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hasPol && samePolicy(l.policy, p) {
		return
	}
	l.policy, l.hasPol = p, true
	if l.cmd != nil {
		_ = l.writeLocked(parentMsg{Type: "policy", Policy: &p})
	}
}

// Close đóng trình duyệt (khi Client thoát). Đóng stdin là đủ: trình duyệt tự thoát.
func (l *Launcher) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cmd == nil {
		return
	}
	_ = l.writeLocked(parentMsg{Type: "quit"})
	_ = l.stdin.Close()
}

func (l *Launcher) writeLocked(m parentMsg) error {
	if l.stdin == nil {
		return errors.New("browser not running")
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = l.stdin.Write(append(b, '\n'))
	return err
}

func (l *Launcher) readVisits(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var v Visit
		if json.Unmarshal(sc.Bytes(), &v) != nil || v.Type != "visit" {
			continue
		}
		if l.onVisit != nil {
			l.onVisit(v)
		}
	}
}

func samePolicy(a, b Policy) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
