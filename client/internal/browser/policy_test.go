package browser

import "testing"

func TestDecideRestricted(t *testing.T) {
	p := Policy{
		Mode:        ModeLearning,
		Allow:       []string{"https://docs.python.org/3/tutorial", "w3schools.com/python/", "*.github.io", "http://example.org:8080/a"},
		AlwaysHosts: []string{"rikkei.edu.vn", "rikkeiedu.com"},
	}
	cases := []struct {
		url  string
		want bool
	}{
		{"https://docs.python.org/3/tutorial", true},
		{"https://docs.python.org/3/tutorial/", true},
		{"https://docs.python.org/3/tutorial/controlflow.html#for", true},
		{"https://docs.python.org/3/tutorial?x=1", true},
		{"https://DOCS.python.org./3/tutorial/index.html", true},
		{"https://docs.python.org:443/3/tutorial/", true},
		{"https://docs.python.org/3/tutorials", false}, // ranh giới đoạn
		{"https://docs.python.org/3/library/", false},  // ngoài prefix
		{"https://docs.python.org/3/tutorial/../library", false},
		{"https://docs.python.org/3/tutorial/%2e%2e/library", false},
		{"https://docs.python.org/3/tutorial/%252e%252e/library", false}, // mã hóa kép
		{"https://docs.python.org/3/tutorial/..%2flibrary", false},
		{"http://docs.python.org/3/tutorial/", false}, // mục ghi rõ https -> không nhận http
		{"http://w3schools.com/python/", true},        // mục không ghi scheme -> cả http
		{"https://docs.python.org.evil.com/3/tutorial/", false},
		{"https://docs.python.org@evil.com/3/tutorial/", false},
		{"https://evil.com/?u=https://docs.python.org/3/tutorial", false},
		{"https://docs.python.org:8443/3/tutorial/", false}, // cổng khác
		{"https://www.w3schools.com/python/", true},         // mục không có www khớp cả www
		{"https://api.w3schools.com/python/", false},        // tên miền con khác thì không
		{"https://w3schools.com/python/python_intro.asp", true},
		{"https://w3schools.com/python", true},
		{"https://w3schools.com/js/", false},
		{"https://abc.github.io/x", true},
		{"https://github.io/x", false}, // *.github.io không gồm github.io
		{"http://example.org:8080/a/b", true},
		{"https://example.org:8080/a/b", false}, // mục ghi rõ http
		{"http://example.org/a/b", false},
		{"https://lms.rikkei.edu.vn/courses", true}, // tên miền hệ thống
		{"https://sc.rikkeiedu.com/x", true},
		{"https://rikkei.edu.vn.evil.com/", false},
		{"https://fakerikkei.edu.vn/", false},
		{"http://127.0.0.1:34115/exam-view", true},
		{"file:///C:/Windows/win.ini", false},
		{"javascript:alert(1)", false},
		{"data:text/html,hi", false},
		{"", false},
	}
	for _, c := range cases {
		if got := p.Decide(c.url).Allowed; got != c.want {
			t.Errorf("Decide(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestDecideFree(t *testing.T) {
	p := Policy{Mode: ModeFree}
	for _, u := range []string{"https://youtube.com/watch?v=1", "http://example.com/"} {
		if !p.Decide(u).Allowed {
			t.Errorf("free mode should allow %s", u)
		}
	}
	for _, u := range []string{"file:///etc/passwd", "javascript:alert(1)", "https://a@b.com/"} {
		if p.Decide(u).Allowed {
			t.Errorf("free mode should still block %s", u)
		}
	}
}

func TestLockedPolicyOnlySystemHosts(t *testing.T) {
	p := LockedPolicy(DefaultAlwaysHosts)
	if !p.Decide("https://lms.rikkei.edu.vn/").Allowed {
		t.Error("locked policy must allow LMS")
	}
	if p.Decide("https://google.com/").Allowed {
		t.Error("locked policy must block other sites")
	}
}

func TestValidRule(t *testing.T) {
	for _, s := range []string{"docs.python.org", "https://docs.python.org/3/", "*.github.io", "localhost:3000/x", "10.0.0.5/app"} {
		if !ValidRule(s) {
			t.Errorf("ValidRule(%q) = false", s)
		}
	}
	for _, s := range []string{"", "   ", "python", "https://", "https://a@b.com/"} {
		if ValidRule(s) {
			t.Errorf("ValidRule(%q) = true", s)
		}
	}
}
