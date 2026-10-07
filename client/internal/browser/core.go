package browser

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"
)

// engine là phần riêng từng hệ điều hành (WebView2 / WKWebView / WebKitGTK).
// Mọi hàm (trừ Dispatch) chỉ được gọi trên luồng giao diện.
type engine interface {
	Navigate(url string)
	LoadHTML(html string) // trang nội bộ: trang chặn, trang bắt đầu
	Back()
	Forward()
	Reload()
	Stop()
	ToolbarEval(js string)
	SetWindowTitle(title string)
	Focus()
	Quit()
	Dispatch(f func()) // chạy f trên luồng giao diện, gọi được từ goroutine bất kỳ
}

// Tin nhắn Client -> trình duyệt (stdin, mỗi dòng một JSON).
type parentMsg struct {
	Type   string  `json:"type"` // policy | open | focus | quit
	Policy *Policy `json:"policy,omitempty"`
	URL    string  `json:"url,omitempty"`
}

// Visit là một lượt mở trang (trình duyệt -> Client qua stdout).
type Visit struct {
	Type    string `json:"type"` // visit
	URL     string `json:"url"`
	Title   string `json:"title,omitempty"`
	Blocked bool   `json:"blocked"`
	Reason  string `json:"reason,omitempty"`
	Mode    string `json:"mode"`
	At      string `json:"at"`
}

// Core giữ trạng thái trình duyệt và quyết định cho/chặn; không phụ thuộc HĐH.
type Core struct {
	eng engine

	mu          sync.Mutex
	pol         Policy
	havePolicy  bool
	internalNav int    // số lượt LoadHTML đang chờ (cho phép about:blank/data: tương ứng)
	shownURL    string // URL hiện trên thanh địa chỉ
	title       string
	canBack     bool
	canFwd      bool
	blockedURL  string // đang hiện trang chặn cho URL này
	lastReport  map[string]time.Time

	initial string // URL mở lúc khởi động (Client yêu cầu), mở sau khi có chính sách

	outMu sync.Mutex
	out   io.Writer
}

func newCore(out io.Writer) *Core {
	return &Core{out: out, pol: LockedPolicy(DefaultAlwaysHosts), lastReport: map[string]time.Time{}}
}

func (c *Core) attach(e engine) { c.eng = e }

func (c *Core) policy() Policy {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pol
}

// ---------------------------------------------------------------- Client <-> trình duyệt

// start: engine đã sẵn sàng -> bắt đầu nghe Client. Nếu 3 giây chưa có chính sách
// thì vẫn hiện trang đầu (đang ở khóa an toàn: chỉ tên miền hệ thống).
func (c *Core) start(in io.Reader, initial string) {
	c.initial = initial
	go c.readParent(in)
	time.AfterFunc(3*time.Second, func() {
		c.eng.Dispatch(func() {
			c.mu.Lock()
			waiting := !c.havePolicy && c.shownURL == "" && c.internalNav == 0
			c.mu.Unlock()
			if waiting {
				c.showStart()
			}
		})
	})
}

// readParent đọc lệnh từ Client. Hết stdin = Client đã thoát -> đóng trình duyệt.
func (c *Core) readParent(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var m parentMsg
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch m.Type {
		case "policy":
			if m.Policy != nil {
				p := *m.Policy
				c.eng.Dispatch(func() { c.applyPolicy(p) })
			}
		case "open":
			u := m.URL
			c.eng.Dispatch(func() {
				c.eng.Focus()
				if u != "" {
					c.go_(u)
				}
			})
		case "focus":
			c.eng.Dispatch(c.eng.Focus)
		case "quit":
			c.eng.Dispatch(c.eng.Quit)
		}
	}
	log.Printf("[BROWSER] parent stdin closed -> quit")
	c.eng.Dispatch(c.eng.Quit)
}

func (c *Core) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	c.outMu.Lock()
	defer c.outMu.Unlock()
	_, _ = c.out.Write(append(b, '\n'))
}

func (c *Core) report(u, title string, blocked bool, reason string) {
	// Gom lượt trùng (trang tự tải lại, iframe chặn liên tục) trong 5 giây.
	key := fmt.Sprintf("%t|%s", blocked, u)
	c.mu.Lock()
	if t, ok := c.lastReport[key]; ok && time.Since(t) < 5*time.Second {
		c.mu.Unlock()
		return
	}
	c.lastReport[key] = time.Now()
	if len(c.lastReport) > 500 {
		c.lastReport = map[string]time.Time{}
	}
	mode := c.pol.Mode
	c.mu.Unlock()
	c.send(Visit{Type: "visit", URL: u, Title: title, Blocked: blocked, Reason: reason, Mode: mode, At: time.Now().Format(time.RFC3339)})
}

// applyPolicy nhận chính sách mới. Nếu trang đang mở không còn được phép (vd vừa
// vào giờ thi) thì chuyển ngay sang trang chặn.
func (c *Core) applyPolicy(p Policy) {
	if len(p.AlwaysHosts) == 0 {
		p.AlwaysHosts = DefaultAlwaysHosts
	}
	c.mu.Lock()
	first := !c.havePolicy
	c.pol = p
	c.havePolicy = true
	cur := c.shownURL
	blocked := c.blockedURL
	c.mu.Unlock()
	log.Printf("[BROWSER] policy mode=%s allow=%d", p.Mode, len(p.Allow))
	c.pushToolbar()
	switch {
	case first && cur == "":
		if c.initial != "" {
			c.go_(c.initial)
		} else {
			c.home()
		}
	case blocked != "" && p.Decide(blocked).Allowed:
		c.go_(blocked) // hết giờ học: trang vừa bị chặn giờ mở được
	case cur != "" && blocked == "" && isWebURL(cur):
		if d := p.Decide(cur); !d.Allowed {
			c.showBlocked(cur, d.Reason)
		}
	default:
		if cur == "" {
			c.home()
		}
	}
}

// ---------------------------------------------------------------- sự kiện từ engine

// AllowNavigation được engine gọi TRƯỚC mỗi lượt điều hướng (khung chính lẫn khung
// con, kể cả chuyển hướng). Trả false = hủy.
func (c *Core) AllowNavigation(raw string, mainFrame bool) bool {
	low := strings.ToLower(strings.TrimSpace(raw))
	if low == "about:blank" || strings.HasPrefix(low, "about:blank#") {
		return true
	}
	if strings.HasPrefix(low, "data:") || strings.HasPrefix(low, "about:") {
		c.mu.Lock()
		ok := mainFrame && c.internalNav > 0
		if ok {
			c.internalNav--
		}
		c.mu.Unlock()
		return ok
	}
	d := c.policy().Decide(raw)
	if d.Allowed {
		if mainFrame {
			c.mu.Lock()
			c.blockedURL = ""
			c.internalNav = 0 // trang thật đã mở: không còn lượt trang nội bộ nào chờ
			c.mu.Unlock()
		}
		return true
	}
	if mainFrame {
		c.report(raw, "", true, d.Reason)
		// Không gọi LoadHTML ngay trong handler (một số engine không cho điều hướng
		// lồng trong lúc đang xét) -> đẩy sang lượt sau.
		c.eng.Dispatch(func() { c.showBlocked(raw, d.Reason) })
	}
	// Khung nhúng (quảng cáo, đo lường, video nhúng…) bị chặn lặng lẽ: không báo
	// về Client để log truy cập và vi phạm không bị nhiễu bởi thứ sinh viên không bấm.
	return false
}

// NewWindow: trang yêu cầu mở cửa sổ mới (target=_blank, window.open). Mở ngay
// trong khung hiện tại để mọi trang đều đi qua cùng một cửa kiểm tra.
func (c *Core) NewWindow(raw string) {
	if raw == "" || strings.HasPrefix(strings.ToLower(raw), "about:") {
		return
	}
	c.eng.Dispatch(func() { c.go_(raw) })
}

// Committed: khung chính đã chuyển sang URL mới (sau chuyển hướng). Kiểm lại lần
// nữa phòng engine bỏ sót một bước chuyển hướng.
func (c *Core) Committed(raw string) {
	low := strings.ToLower(raw)
	if strings.HasPrefix(low, "data:") || strings.HasPrefix(low, "about:") {
		return // trang nội bộ: giữ URL hiển thị đã đặt
	}
	if d := c.policy().Decide(raw); !d.Allowed {
		c.eng.Dispatch(func() { c.showBlocked(raw, d.Reason) })
		return
	}
	c.mu.Lock()
	c.shownURL = raw
	c.blockedURL = ""
	c.mu.Unlock()
	c.pushToolbar()
}

// Loaded: khung chính tải xong (để ghi lượt truy cập kèm tiêu đề).
func (c *Core) Loaded(raw string) {
	low := strings.ToLower(raw)
	if !isWebURL(low) {
		return
	}
	if !c.policy().Decide(raw).Allowed {
		return
	}
	c.mu.Lock()
	title := c.title
	c.mu.Unlock()
	c.report(raw, title, false, "")
}

func (c *Core) TitleChanged(t string) {
	c.mu.Lock()
	c.title = t
	c.mu.Unlock()
	if t == "" {
		c.eng.SetWindowTitle("Trình duyệt — Rikkei LMS Connect")
	} else {
		c.eng.SetWindowTitle(t + " — Trình duyệt Rikkei LMS Connect")
	}
	c.pushToolbar()
}

func (c *Core) HistoryChanged(back, fwd bool) {
	c.mu.Lock()
	c.canBack, c.canFwd = back, fwd
	c.mu.Unlock()
	c.pushToolbar()
}

// ToolbarReady: thanh công cụ vừa tải xong -> đẩy trạng thái.
func (c *Core) ToolbarReady() { c.pushToolbar() }

// ToolbarMessage xử lý lệnh từ thanh công cụ (JSON {"cmd": ..., "url": ...}).
func (c *Core) ToolbarMessage(msg string) {
	var m struct {
		Cmd string `json:"cmd"`
		URL string `json:"url"`
	}
	if json.Unmarshal([]byte(msg), &m) != nil {
		return
	}
	switch m.Cmd {
	case "ready":
		c.pushToolbar()
	case "go":
		c.go_(m.URL)
	case "back":
		c.eng.Back()
	case "forward":
		c.eng.Forward()
	case "reload":
		c.mu.Lock()
		b := c.blockedURL
		c.mu.Unlock()
		if b != "" {
			c.go_(b)
		} else {
			c.eng.Reload()
		}
	case "stop":
		c.eng.Stop()
	case "home":
		c.home()
	case "allowed":
		c.showStart()
	}
}

// ---------------------------------------------------------------- điều hướng

// go_ mở nội dung thanh địa chỉ: URL, tên miền, hoặc từ khóa tìm kiếm.
func (c *Core) go_(input string) {
	target, isSearch := resolveInput(input)
	if target == "" {
		return
	}
	p := c.policy()
	if d := p.Decide(target); !d.Allowed {
		reason := d.Reason
		if isSearch {
			reason = "Không tìm kiếm được trong lúc này. Hãy chọn một trang trong danh sách được phép."
		}
		c.report(target, "", true, reason)
		c.showBlocked(target, reason)
		return
	}
	c.eng.Navigate(target)
}

func (c *Core) home() {
	p := c.policy()
	if p.HomeURL != "" && p.Decide(p.HomeURL).Allowed {
		c.eng.Navigate(p.HomeURL)
		return
	}
	c.showStart()
}

func (c *Core) loadInternal(page, shown string, blocked string) {
	c.mu.Lock()
	c.internalNav++
	c.shownURL = shown
	c.blockedURL = blocked
	c.mu.Unlock()
	c.pushToolbar()
	c.eng.LoadHTML(page)
}

func (c *Core) showBlocked(u, reason string) {
	c.loadInternal(blockedPage(c.policy(), u, reason), u, u)
	c.eng.SetWindowTitle("Trang bị chặn — Trình duyệt Rikkei LMS Connect")
}

func (c *Core) showStart() {
	c.loadInternal(startPage(c.policy()), "", "")
	c.eng.SetWindowTitle("Trình duyệt — Rikkei LMS Connect")
}

func (c *Core) pushToolbar() {
	if c.eng == nil {
		return
	}
	c.mu.Lock()
	st := map[string]any{
		"url":        c.shownURL,
		"title":      c.title,
		"canBack":    c.canBack,
		"canForward": c.canFwd,
		"mode":       c.pol.Mode,
		"label":      c.pol.Label,
		"blocked":    c.blockedURL != "",
		"allowCount": len(c.pol.Allow),
	}
	c.mu.Unlock()
	b, _ := json.Marshal(st)
	c.eng.ToolbarEval("window.rkState&&window.rkState(" + string(b) + ")")
}

// resolveInput: "docs.python.org" -> https://docs.python.org ; "vòng lặp python" -> tìm kiếm.
func resolveInput(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	low := strings.ToLower(s)
	if strings.Contains(low, "://") || strings.HasPrefix(low, "about:") || strings.HasPrefix(low, "data:") || strings.HasPrefix(low, "javascript:") || strings.HasPrefix(low, "file:") {
		return s, false
	}
	if !strings.ContainsAny(s, " \t") && (strings.Contains(s, ".") || strings.HasPrefix(low, "localhost")) {
		host := s
		if i := strings.IndexAny(host, "/?#"); i >= 0 {
			host = host[:i]
		}
		if strings.HasPrefix(low, "localhost") || strings.HasPrefix(low, "127.") {
			return "http://" + s, false
		}
		if strings.Contains(host, ".") {
			return "https://" + s, false
		}
	}
	return "https://www.google.com/search?q=" + url.QueryEscape(s), true
}

func isWebURL(s string) bool {
	s = strings.ToLower(s)
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// ---------------------------------------------------------------- trang nội bộ

const pageCSS = `<style>
:root{--bg:#f5f6fa;--card:#fff;--ink:#1e2235;--muted:#5b6175;--line:#e2e5ee;--accent:#4b51c4;--warn:#b42318;--warn-soft:#fdecea}
@media (prefers-color-scheme:dark){:root{--bg:#14161f;--card:#1c1f2b;--ink:#e8eaf2;--muted:#a3a8bb;--line:#2d3243;--accent:#9aa0f5;--warn:#f97066;--warn-soft:#3b1c1c}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--ink);font:15px/1.6 "Segoe UI",-apple-system,"Noto Sans",Ubuntu,sans-serif}
.wrap{max-width:720px;margin:56px auto;padding:0 20px}
h1{font-size:24px;line-height:1.3;margin:0 0 8px}
.muted{color:var(--muted)}.url{font-family:Consolas,Menlo,monospace;font-size:13px;word-break:break-all;background:var(--card);border:1px solid var(--line);border-radius:8px;padding:8px 10px;margin:14px 0}
.tag{display:inline-block;font-size:12px;font-weight:600;letter-spacing:.04em;text-transform:uppercase;color:var(--warn);margin-bottom:10px}
.card{background:var(--card);border:1px solid var(--line);border-radius:12px;padding:16px 18px;margin-top:20px}
.card h2{font-size:15px;margin:0 0 8px}
ul{margin:0;padding-left:18px}li{margin:4px 0}a{color:var(--accent);word-break:break-all}
.btn{display:inline-block;margin-top:18px;padding:8px 14px;border-radius:8px;border:1px solid var(--line);background:var(--card);color:var(--ink);text-decoration:none;cursor:pointer;font:inherit}
</style>`

// linkFor biến một mục cấu hình thành link bấm được (mục *.example.com thì chỉ hiện chữ).
func linkFor(rule string) (href, text string) {
	text = strings.TrimSpace(rule)
	if text == "" || strings.Contains(text, "*") {
		return "", text
	}
	low := strings.ToLower(text)
	if strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") {
		return text, text
	}
	return "https://" + text, text
}

func allowedListHTML(p Policy) string {
	var b strings.Builder
	if len(p.Allow) == 0 {
		b.WriteString(`<p class="muted">Lớp chưa cấu hình trang web nào. Bạn vẫn mở được hệ thống LMS của Rikkei.</p>`)
	} else {
		b.WriteString("<ul>")
		for _, r := range p.Allow {
			href, text := linkFor(r)
			if href == "" {
				fmt.Fprintf(&b, "<li>%s</li>", html.EscapeString(text))
			} else {
				fmt.Fprintf(&b, `<li><a href="%s">%s</a></li>`, html.EscapeString(href), html.EscapeString(text))
			}
		}
		b.WriteString("</ul>")
	}
	if len(p.AlwaysHosts) > 0 {
		fmt.Fprintf(&b, `<p class="muted" style="margin-top:10px">Luôn mở được: %s (và tên miền con).</p>`, html.EscapeString(strings.Join(p.AlwaysHosts, ", ")))
	}
	return b.String()
}

func modeText(p Policy) string {
	switch p.Mode {
	case ModeExam:
		return "Đang trong giờ thi"
	case ModeLearning:
		return "Đang trong giờ học"
	}
	return ""
}

func blockedPage(p Policy, u, reason string) string {
	head := modeText(p)
	if head == "" {
		head = "Không mở được trang"
	}
	return `<!doctype html><html lang="vi"><head><meta charset="utf-8"><title>Trang bị chặn</title>` + pageCSS + `</head><body><div class="wrap">
<div class="tag">` + html.EscapeString(head) + `</div>
<h1>` + html.EscapeString(reason) + `</h1>
<p class="muted">Trình duyệt của Rikkei LMS Connect chỉ mở các trang giảng viên đã cho phép trong lúc này. Lượt mở này đã được ghi lại.</p>
<div class="url">` + html.EscapeString(u) + `</div>
<div class="card"><h2>Trang được phép</h2>` + allowedListHTML(p) + `</div>
<button class="btn" onclick="history.back()">Quay lại</button>
</div></body></html>`
}

func startPage(p Policy) string {
	var body string
	if p.Restricted() {
		body = `<div class="tag" style="color:var(--accent)">` + html.EscapeString(modeText(p)) + `</div>
<h1>Chỉ mở được các trang dưới đây</h1>
<p class="muted">` + html.EscapeString(p.Label) + `</p>
<div class="card"><h2>Trang được phép</h2>` + allowedListHTML(p) + `</div>`
	} else {
		body = `<h1>Trình duyệt Rikkei LMS Connect</h1>
<p class="muted">Ngoài giờ học bạn mở được mọi trang web. Trong giờ học và giờ thi, trình duyệt chỉ mở các trang giảng viên cho phép.</p>
<p class="muted">Gõ địa chỉ hoặc từ khóa vào ô phía trên để bắt đầu.</p>`
	}
	return `<!doctype html><html lang="vi"><head><meta charset="utf-8"><title>Trình duyệt</title>` + pageCSS + `</head><body><div class="wrap">` + body + `</div></body></html>`
}
