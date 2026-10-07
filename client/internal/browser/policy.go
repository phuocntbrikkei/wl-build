// Package browser là trình duyệt tích hợp của Rikkei LMS Connect: dùng như một
// trình duyệt thường, nhưng trong giờ học / giờ thi chỉ mở được các URL nằm trong
// danh sách prefix được phép (cấu hình ở SC). Trình duyệt tự nắm từng lượt điều
// hướng với URL đầy đủ nên chặn được tới mức đường dẫn — điều proxy hệ thống không
// làm được với HTTPS (proxy chỉ thấy tên miền).
//
// Trình duyệt chạy thành tiến trình con của Client (cùng file exe), nhận chính sách
// qua stdin và báo lượt truy cập qua stdout (JSON từng dòng). Xem launcher.go.
package browser

import (
	"net"
	"net/url"
	"path"
	"strings"
)

// Chế độ áp dụng.
const (
	ModeFree     = "free"     // ngoài giờ: mở mọi trang web
	ModeLearning = "learning" // giờ học: chỉ prefix được phép
	ModeExam     = "exam"     // giờ thi: chỉ prefix được phép
)

// Policy là chính sách Client gửi xuống trình duyệt.
type Policy struct {
	Mode  string   `json:"mode"`
	Label string   `json:"label"` // vd "Giờ học · IT108"
	Allow []string `json:"allow"` // prefix URL được phép (chỉ dùng khi Mode != free)
	// Tên miền hệ thống luôn được mở (khớp chính xác hoặc tên miền con), vd
	// "rikkei.edu.vn" -> lms.rikkei.edu.vn. Để sinh viên luôn vào được LMS.
	AlwaysHosts []string `json:"alwaysHosts"`
	HomeURL     string   `json:"homeUrl"`
}

// Restricted: đang trong giờ học / giờ thi.
func (p Policy) Restricted() bool {
	return p.Mode == ModeLearning || p.Mode == ModeExam
}

// LockedPolicy: khóa an toàn khi chưa nhận được chính sách hoặc mất kết nối với
// Client — chỉ mở tên miền hệ thống, không nới lỏng.
func LockedPolicy(always []string) Policy {
	return Policy{Mode: ModeExam, Label: "Đang chờ cấu hình", AlwaysHosts: always}
}

// DefaultAlwaysHosts: tên miền hệ thống mặc định (SC gửi kèm danh sách thật).
var DefaultAlwaysHosts = []string{"rikkei.edu.vn", "rikkeiedu.com"}

// Decision là kết quả xét một URL.
type Decision struct {
	Allowed bool
	// Reason ngắn gọn để hiện cho sinh viên khi bị chặn.
	Reason string
}

// target là URL đã chuẩn hóa để so khớp.
type target struct {
	scheme string
	host   string
	port   string // rỗng nếu là cổng mặc định của scheme
	path   string // đã giải mã %xx và Clean, luôn bắt đầu bằng "/"
}

// parseTarget chuẩn hóa URL trình duyệt sắp mở. ok=false nếu không phải URL web hợp lệ.
func parseTarget(raw string) (target, bool, string) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return target{}, false, "Địa chỉ không hợp lệ"
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return target{}, false, "Chỉ mở được trang web (http/https)"
	}
	if u.User != nil {
		// "https://docs.python.org@evil.com/" — phần trước @ là tên đăng nhập, host
		// thật là evil.com. Không có lý do chính đáng để dùng trong giờ học.
		return target{}, false, "Địa chỉ có chứa thông tin đăng nhập"
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return target{}, false, "Địa chỉ không có tên miền"
	}
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	return target{scheme: scheme, host: host, port: port, path: cleanPath(u)}, true, ""
}

// cleanPath giải mã %2e%2e, %2f… rồi Clean để "/3/tutorial/../../evil" không lọt
// qua prefix "/3/tutorial". Giữ "/" cuối nếu URL gốc có.
func cleanPath(u *url.URL) string {
	p := u.Path // url.Parse đã giải mã %xx một lượt
	// Giải mã thêm 1 lượt cho trường hợp mã hóa kép (%252e).
	if strings.Contains(p, "%") {
		if d, err := url.PathUnescape(p); err == nil {
			p = d
		}
	}
	p = strings.ReplaceAll(p, "\\", "/")
	if p == "" {
		return "/"
	}
	trailing := strings.HasSuffix(p, "/")
	p = path.Clean("/" + p)
	if trailing && p != "/" {
		p += "/"
	}
	return p
}

// rule là một mục trong danh sách cho phép.
type rule struct {
	scheme   string // rỗng = http và https
	host     string
	wildcard bool // "*.example.com": mọi tên miền con (không gồm example.com)
	port     string
	path     string // prefix đường dẫn, "/" = cả trang
}

// parseRule đọc một mục cấu hình: "docs.python.org", "https://docs.python.org/3/",
// "*.w3schools.com/python". Trả ok=false nếu mục không đọc được.
func parseRule(s string) (rule, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return rule{}, false
	}
	r := rule{}
	lower := strings.ToLower(s)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		s = "https://" + s
	} else {
		r.scheme = lower[:strings.Index(lower, ":")]
	}
	if strings.HasPrefix(strings.ToLower(s), "https://*.") || strings.HasPrefix(strings.ToLower(s), "http://*.") {
		r.wildcard = true
		s = strings.Replace(s, "*.", "", 1)
	}
	u, err := url.Parse(s)
	if err != nil || u.User != nil {
		return rule{}, false
	}
	r.host = strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if r.host == "" || net.ParseIP(r.host) == nil && !strings.Contains(r.host, ".") && r.host != "localhost" {
		return rule{}, false
	}
	r.port = u.Port()
	if (r.scheme == "http" && r.port == "80") || (r.scheme == "https" && r.port == "443") || (r.scheme == "" && (r.port == "443" || r.port == "80")) {
		r.port = ""
	}
	r.path = cleanPath(u)
	return r, true
}

func (r rule) match(t target) bool {
	if r.scheme != "" && r.scheme != t.scheme {
		return false
	}
	if r.port != t.port {
		return false
	}
	if r.wildcard {
		if !strings.HasSuffix(t.host, "."+r.host) {
			return false
		}
	} else if t.host != r.host && t.host != "www."+r.host {
		// "w3schools.com" khớp cả "www.w3schools.com" (giáo vụ thường không gõ www).
		return false
	}
	return pathHasPrefix(t.path, r.path)
}

// pathHasPrefix so theo ranh giới đoạn: "/3/tutorial" khớp "/3/tutorial",
// "/3/tutorial/x" nhưng KHÔNG khớp "/3/tutorials".
func pathHasPrefix(p, prefix string) bool {
	if prefix == "/" || prefix == "" {
		return true
	}
	if strings.HasSuffix(prefix, "/") {
		return strings.HasPrefix(p, prefix) || p+"/" == prefix
	}
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}

func hostInSuffixes(host string, suffixes []string) bool {
	for _, s := range suffixes {
		s = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
		if s == "" {
			continue
		}
		if host == s || strings.HasSuffix(host, "."+s) {
			return true
		}
	}
	return false
}

// Decide xét một URL trình duyệt sắp mở (khung chính hoặc khung con).
func (p Policy) Decide(raw string) Decision {
	t, ok, why := parseTarget(raw)
	if !ok {
		return Decision{Allowed: false, Reason: why}
	}
	if !p.Restricted() {
		return Decision{Allowed: true}
	}
	if t.host == "localhost" || t.host == "127.0.0.1" {
		// Trang nội bộ của Client (đề thi PDF, tài nguyên) phục vụ ở 127.0.0.1.
		return Decision{Allowed: true}
	}
	if hostInSuffixes(t.host, p.AlwaysHosts) {
		return Decision{Allowed: true}
	}
	for _, s := range p.Allow {
		if r, ok := parseRule(s); ok && r.match(t) {
			return Decision{Allowed: true}
		}
	}
	return Decision{Allowed: false, Reason: "Trang này không nằm trong danh sách được phép"}
}

// ValidRule cho biết một mục cấu hình có đọc được không (dùng để báo lỗi nhập liệu).
func ValidRule(s string) bool {
	_, ok := parseRule(s)
	return ok
}
