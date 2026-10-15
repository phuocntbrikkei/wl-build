package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSCAuthTransport_AddsTokenOnlyForSC(t *testing.T) {
	var gotToken, gotVersion atomic.Value
	sc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken.Store(r.Header.Get(clientTokenHeaderName))
		gotVersion.Store(r.Header.Get("X-App-Version"))
		if r.URL.Path == "/api/student/status" && r.Header.Get(clientTokenHeaderName) == "bad" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/api/student/login" {
			w.WriteHeader(http.StatusUnauthorized) // sai mật khẩu, không phải token hỏng
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer sc.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken.Store(r.Header.Get(clientTokenHeaderName))
	}))
	defer other.Close()

	u, _ := url.Parse(sc.URL)
	client := &http.Client{Transport: &scAuthTransport{base: http.DefaultTransport, host: u.Host}}

	var fired atomic.Int32
	authInvalidMu.Lock()
	authInvalidHook = func() { fired.Add(1) }
	authInvalidLast = time.Time{}
	authInvalidMu.Unlock()
	defer func() {
		authInvalidMu.Lock()
		authInvalidHook = nil
		authInvalidMu.Unlock()
		setClientToken("", "")
	}()

	setClientToken("tok-1", "")
	if _, err := client.Get(sc.URL + "/api/student/status?studentRkId=1"); err != nil {
		t.Fatal(err)
	}
	if gotToken.Load() != "tok-1" || gotVersion.Load() != AppVersion {
		t.Fatalf("request tới SC phải kèm token + phiên bản, got token=%v version=%v", gotToken.Load(), gotVersion.Load())
	}
	if _, err := client.Get(other.URL + "/x"); err != nil {
		t.Fatal(err)
	}
	if gotToken.Load() != "" {
		t.Fatal("không được gửi token sang máy chủ khác")
	}

	// 401 ở API đăng nhập (sai mật khẩu) -> không đăng xuất.
	_, _ = client.Post(sc.URL+"/api/student/login", "application/json", strings.NewReader("{}"))
	time.Sleep(50 * time.Millisecond)
	if fired.Load() != 0 {
		t.Fatal("401 của /api/student/login không được kích hoạt đăng xuất")
	}
	// 401 ở API của SV -> đăng xuất (1 lần trong thời gian chờ).
	setClientToken("bad", "")
	_, _ = client.Get(sc.URL + "/api/student/status")
	_, _ = client.Get(sc.URL + "/api/student/status")
	time.Sleep(50 * time.Millisecond)
	if fired.Load() != 1 {
		t.Fatalf("token hỏng phải đăng xuất đúng 1 lần, got %d", fired.Load())
	}
}

func TestClientTokenHelpers(t *testing.T) {
	if clientTokenUsable("", "") {
		t.Fatal("không có token -> không dùng được")
	}
	if !clientTokenUsable("x", "") || !clientTokenUsable("x", time.Now().Add(time.Hour).Format(time.RFC3339)) {
		t.Fatal("token còn hạn / không rõ hạn -> dùng được")
	}
	if clientTokenUsable("x", time.Now().Add(-time.Hour).Format(time.RFC3339)) {
		t.Fatal("token hết hạn -> không dùng được")
	}
	setClientToken("a b", "")
	defer setClientToken("", "")
	if got := withClientTokenQuery("https://sc/x?y=1"); got != "https://sc/x?y=1&ct=a+b" {
		t.Fatalf("gắn ct sai: %s", got)
	}
	if got := withClientTokenQuery("https://sc/x"); got != "https://sc/x?ct=a+b" {
		t.Fatalf("gắn ct sai: %s", got)
	}
}
