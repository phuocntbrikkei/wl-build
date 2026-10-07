// Package netproxy chạy một local forward-proxy (HTTP + CONNECT tunnel) để "bắt" các URL/domain
// mà máy sinh viên truy cập ra Internet trong lúc đang giám sát. Với HTTPS (đa số trang hiện nay),
// proxy chỉ tunnel byte thô qua CONNECT — không giải mã nội dung (không MITM) — nên chỉ biết được
// domain:port đích, không biết đường dẫn/nội dung trang. Với HTTP thường thì thấy được URL đầy đủ.
package netproxy

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ListenAddr — địa chỉ local proxy lắng nghe. Cổng riêng, khác cổng 34115 của local callback server.
const ListenAddr = "127.0.0.1:34116"

// CapturedURL mô tả 1 lượt kết nối ra ngoài bị bắt được.
type CapturedURL struct {
	Method  string
	Host    string
	URL     string
	Time    time.Time
	Blocked bool // host trúng danh sách cấm -> proxy đã TỪ CHỐI kết nối
}

var (
	mu           sync.Mutex
	server       *http.Server
	running      bool
	onCapture    func(CapturedURL)
	blockedHosts []string // keyword domain/IP cấm hẳn (khớp theo chuỗi con của host)
)

// SetBlockedHosts cập nhật danh sách domain/IP cấm hẳn (client gọi mỗi lần lấy
// cấu hình lớp). Rỗng -> không chặn gì.
func SetBlockedHosts(hosts []string) {
	mu.Lock()
	blockedHosts = hosts
	mu.Unlock()
}

// hostname bỏ ":port" để khớp keyword.
func hostname(hostPort string) string {
	h := strings.ToLower(strings.TrimSpace(hostPort))
	if i := strings.LastIndexByte(h, ':'); i > 0 {
		// tránh cắt nhầm IPv6 [::]:443 — chỉ cắt nếu phần sau là số cổng
		if _, err := strconv.Atoi(h[i+1:]); err == nil {
			h = h[:i]
		}
	}
	return h
}

func isBlockedHost(hostPort string) bool {
	h := hostname(hostPort)
	if h == "" {
		return false
	}
	mu.Lock()
	list := blockedHosts
	mu.Unlock()
	for _, kw := range list {
		if kw != "" && strings.Contains(h, kw) {
			return true
		}
	}
	return false
}

// Start khởi động local capture proxy (no-op nếu đã chạy).
func Start(callback func(CapturedURL)) error {
	mu.Lock()
	if running {
		mu.Unlock()
		return nil
	}
	onCapture = callback
	mu.Unlock()

	ln, err := net.Listen("tcp", ListenAddr)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Handler:           http.HandlerFunc(handleProxyRequest),
		ReadHeaderTimeout: 10 * time.Second,
	}

	mu.Lock()
	server = srv
	running = true
	mu.Unlock()

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("[NETPROXY] serve error: %v", err)
		}
	}()
	log.Printf("[NETPROXY] Local capture proxy started on %s", ListenAddr)
	return nil
}

// Stop dừng local capture proxy (no-op nếu chưa chạy).
func Stop() {
	mu.Lock()
	if !running {
		mu.Unlock()
		return
	}
	running = false
	srv := server
	server = nil
	mu.Unlock()

	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
	log.Println("[NETPROXY] Local capture proxy stopped")
}

func handleProxyRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		handleConnect(w, r)
		return
	}
	handleHTTP(w, r)
}

// handleConnect — tunnel HTTPS: chỉ ghi nhận host:port đích, chuyển tiếp byte thô 2 chiều, không đọc/sửa nội dung.
func handleConnect(w http.ResponseWriter, r *http.Request) {
	// Cấm hẳn: từ chối CONNECT -> trình duyệt không mở được kết nối tới host này.
	if isBlockedHost(r.Host) {
		reportBlocked("CONNECT", r.Host, "https://"+r.Host)
		http.Error(w, "Blocked by exam/monitoring policy", http.StatusForbidden)
		return
	}
	reportCapture("CONNECT", r.Host, "https://"+r.Host)

	destConn, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		destConn.Close()
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		destConn.Close()
		return
	}

	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		clientConn.Close()
		destConn.Close()
		return
	}

	// Tunnel 2 chiều. QUAN TRỌNG: khi MỘT chiều kết thúc (EOF/đóng) phải đóng CẢ
	// HAI để chiều còn lại thoát ngay. Trước đây chờ cả hai io.Copy xong (wg.Wait)
	// nên với HTTPS keep-alive (YouTube...) chiều client→dest kẹt đọc mãi -> rò rỉ
	// kết nối/goroutine, chồng chất khiến trang nặng không vào được.
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			clientConn.Close()
			destConn.Close()
		})
	}
	go func() {
		_, _ = io.Copy(destConn, clientConn)
		closeBoth()
	}()
	_, _ = io.Copy(clientConn, destConn)
	closeBoth()
}

// handleHTTP — forward request HTTP không mã hoá — thấy được URL đầy đủ (method + host + path + query).
func handleHTTP(w http.ResponseWriter, r *http.Request) {
	if isBlockedHost(r.Host) {
		reportBlocked(r.Method, r.Host, r.URL.String())
		http.Error(w, "Blocked by exam/monitoring policy", http.StatusForbidden)
		return
	}
	reportCapture(r.Method, r.Host, r.URL.String())

	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""

	resp, err := http.DefaultTransport.RoundTrip(outReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func reportCapture(method, host, rawURL string) {
	mu.Lock()
	cb := onCapture
	mu.Unlock()
	if cb == nil || host == "" {
		return
	}
	cb(CapturedURL{Method: method, Host: host, URL: rawURL, Time: time.Now()})
}

// reportBlocked báo 1 kết nối bị proxy CHẶN HẲN (Blocked=true) -> client ghi vi phạm.
func reportBlocked(method, host, rawURL string) {
	mu.Lock()
	cb := onCapture
	mu.Unlock()
	if cb == nil || host == "" {
		return
	}
	cb(CapturedURL{Method: method, Host: host, URL: rawURL, Time: time.Now(), Blocked: true})
}
