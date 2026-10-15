package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TOKEN PHIÊN CLIENT (từ 1.4.6). SC cấp lúc đăng nhập (ký HMAC: id SV + hạn 30 ngày); Client gửi
// kèm MỌI request tới SC ở header X-Client-Token (link mở trong webview / WebSocket: ?ct=).
// SC từ chối request mang id SV khác token, nên không còn mạo danh bằng studentRkId được.
// Token hỏng / hết hạn (SC trả 401) -> đăng xuất, mở lại trang đăng nhập.

const (
	clientTokenHeaderName = "X-Client-Token"
	clientTokenQueryName  = "ct"
	clientTokenRefreshAt  = 7 * 24 * time.Hour // còn dưới ngần này thì xin token mới
	clientTokenCheckEvery = time.Hour
	authInvalidCooldown   = time.Minute
)

var (
	clientTokenMu  sync.RWMutex
	clientTokenVal string
	clientTokenExp time.Time

	authInvalidMu   sync.Mutex
	authInvalidLast time.Time
	authInvalidHook func() // App đăng ký: đăng xuất khi SC báo token hỏng
)

// setClientToken cập nhật token đang dùng (exp dạng RFC3339; rỗng = không rõ hạn).
func setClientToken(tok, exp string) {
	clientTokenMu.Lock()
	defer clientTokenMu.Unlock()
	clientTokenVal = strings.TrimSpace(tok)
	clientTokenExp = time.Time{}
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(exp)); err == nil {
		clientTokenExp = t
	}
}

func currentClientToken() string {
	clientTokenMu.RLock()
	defer clientTokenMu.RUnlock()
	return clientTokenVal
}

// clientTokenUsable: có token và chưa hết hạn (hạn không rõ thì coi là còn).
func clientTokenUsable(tok, exp string) bool {
	if strings.TrimSpace(tok) == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(exp))
	return err != nil || time.Now().Before(t)
}

// withClientTokenQuery gắn ?ct= vào link SC mở trong webview (webview không mang header của Go).
func withClientTokenQuery(rawURL string) string {
	tok := currentClientToken()
	if tok == "" {
		return rawURL
	}
	sep := "?"
	if strings.Contains(rawURL, "?") {
		sep = "&"
	}
	return rawURL + sep + clientTokenQueryName + "=" + url.QueryEscape(tok)
}

// scAuthTransport: gắn token + phiên bản vào request tới SC; SC trả 401 cho API của SV -> báo
// App đăng xuất (trừ chính các API đăng nhập, nơi 401 = sai mật khẩu).
type scAuthTransport struct {
	base http.RoundTripper
	host string
}

func (t *scAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	isSC := req.URL.Host == t.host && strings.HasPrefix(req.URL.Path, "/api/")
	if isSC {
		tok := currentClientToken()
		if (tok != "" && req.Header.Get(clientTokenHeaderName) == "") || req.Header.Get("X-App-Version") == "" {
			req = req.Clone(req.Context())
			if tok != "" && req.Header.Get(clientTokenHeaderName) == "" {
				req.Header.Set(clientTokenHeaderName, tok)
			}
			if req.Header.Get("X-App-Version") == "" {
				req.Header.Set("X-App-Version", AppVersion)
				req.Header.Set("X-App-Platform", appPlatform())
			}
		}
	}
	resp, err := t.base.RoundTrip(req)
	if err == nil && isSC && resp.StatusCode == http.StatusUnauthorized && studentAuthPath(req.URL.Path) {
		notifyClientAuthInvalid(req.URL.Path)
	}
	return resp, err
}

// studentAuthPath: API của SV có kiểm token phiên client (không gồm đăng nhập / đổi token LMS).
func studentAuthPath(p string) bool {
	switch p {
	case "/api/student/login", "/api/student/resolve":
		return false
	}
	return strings.HasPrefix(p, "/api/student/") || strings.HasPrefix(p, "/api/ide/")
}

func notifyClientAuthInvalid(path string) {
	authInvalidMu.Lock()
	if time.Since(authInvalidLast) < authInvalidCooldown || authInvalidHook == nil {
		authInvalidMu.Unlock()
		return
	}
	authInvalidLast = time.Now()
	hook := authInvalidHook
	authInvalidMu.Unlock()
	log.Printf("[AUTH] SC từ chối phiên đăng nhập (%s) -> đăng xuất để đăng nhập lại", path)
	go hook()
}

// installSCAuthTransport bọc http.DefaultTransport (mọi http.Client không tự đặt Transport).
func installSCAuthTransport() {
	u, err := url.Parse(API_BASE)
	if err != nil || u.Host == "" {
		return
	}
	if _, ok := http.DefaultTransport.(*scAuthTransport); ok {
		return
	}
	http.DefaultTransport = &scAuthTransport{base: http.DefaultTransport, host: u.Host}
}

// refreshClientTokenLoop: còn dưới clientTokenRefreshAt thì xin token mới, lưu lại phiên.
func (a *App) refreshClientTokenLoop() {
	ticker := time.NewTicker(clientTokenCheckEvery)
	defer ticker.Stop()
	for {
		a.refreshClientTokenIfNeeded()
		<-ticker.C
	}
}

func (a *App) refreshClientTokenIfNeeded() {
	clientTokenMu.RLock()
	tok, exp := clientTokenVal, clientTokenExp
	clientTokenMu.RUnlock()
	if tok == "" || (!exp.IsZero() && time.Until(exp) > clientTokenRefreshAt) {
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := scPostJSON(client, API_BASE+"/api/student/token/refresh", []byte("{}"))
	if err != nil {
		return // mất mạng: thử lại lần sau
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return // 401 đã được transport xử lý (đăng xuất)
	}
	var out struct {
		ClientToken    string `json:"clientToken"`
		ClientTokenExp string `json:"clientTokenExp"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil || out.ClientToken == "" {
		return
	}
	a.mu.Lock()
	st := a.student
	a.mu.Unlock()
	if st == nil {
		return
	}
	cp := *st
	cp.ClientToken, cp.ClientTokenExp = out.ClientToken, out.ClientTokenExp
	a.saveSession(&cp)
	log.Printf("[AUTH] đã gia hạn phiên đăng nhập tới %s", out.ClientTokenExp)
}
