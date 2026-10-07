package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// /browser/open chỉ nhận POST kèm header X-Rikkei-Ide và URL http(s).
func TestBrowserOpenFromIDERejects(t *testing.T) {
	a := &App{}
	cases := []struct {
		name, method, body string
		header             bool
		want               int
	}{
		{"GET từ trang web", http.MethodGet, "", false, http.StatusForbidden},
		{"POST thiếu header", http.MethodPost, `{"url":"https://a.com"}`, false, http.StatusForbidden},
		{"JSON hỏng", http.MethodPost, `{`, true, http.StatusBadRequest},
		{"không phải http", http.MethodPost, `{"url":"file:///etc/passwd"}`, true, http.StatusBadRequest},
		{"javascript:", http.MethodPost, `{"url":"javascript:alert(1)"}`, true, http.StatusBadRequest},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, "/browser/open", strings.NewReader(c.body))
		if c.header {
			req.Header.Set("X-Rikkei-Ide", "1")
		}
		rec := httptest.NewRecorder()
		a.handleBrowserOpenFromIDE(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: code %d, want %d", c.name, rec.Code, c.want)
		}
	}
}

func TestIDEStatus(t *testing.T) {
	a := &App{student: &StudentData{StudentID: 7, StudentCode: "SV7", FullName: "An"}}
	a.dashboard.MonitorMode = "exam"
	a.dashboard.Exam = &StudentExamSnapshot{ExamRoomID: 3, ExamName: "Giữa kỳ"}
	st := a.currentIDEStatus()
	if st.Mode != "exam" || st.ExamRoomID != 3 || st.Label != "Giờ thi · Giữa kỳ" || st.StudentRkID != 7 {
		t.Fatalf("unexpected status: %+v", st)
	}
	a.browserMode = "free" // cấu hình lớp (allowed-apps) thắng dashboard
	if a.currentIDEStatus().Mode != "free" {
		t.Fatal("browserMode must win")
	}
	rec := httptest.NewRecorder()
	a.handleIDEStatus(rec, httptest.NewRequest(http.MethodGet, "/ide/status", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing header must be forbidden, got %d", rec.Code)
	}
}
