package main

import (
	"net/http"
	"testing"
)

func TestAppVersionFromFile(t *testing.T) {
	if AppVersion == "" || AppVersion != versionLabel()[1:] {
		t.Fatalf("AppVersion=%q label=%q", AppVersion, versionLabel())
	}
}

func TestSCRequestCarriesVersion(t *testing.T) {
	req, err := scRequest(http.MethodGet, "https://sc.example/api/student/status")
	if err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("X-App-Version") != AppVersion || req.Header.Get("X-App-Platform") == "" {
		t.Fatalf("missing headers: %v", req.Header)
	}
}

func TestMarkVersionBlocked(t *testing.T) {
	a := &App{}
	a.version.Required = true // đã chặn -> không gọi mạng lần nữa
	a.markVersionBlocked("x")
	if st := a.GetVersionStatus(); !st.Required || st.Current != AppVersion {
		t.Fatalf("unexpected: %+v", st)
	}
}
