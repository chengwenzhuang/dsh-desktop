package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionLess(t *testing.T) {
	cases := []struct{ a, b string; less bool }{
		{"1.0.0", "1.0.1", true},
		{"1.0.1", "1.0.0", false},
		{"v1.2.3", "1.2.4", true},
		{"1.2.3", "1.2.3", false},
		{"1.0.0", "1.0", false},
		{"2.0.0", "1.9.9", false},
		{"1.10.0", "1.9.0", false},
	}
	for _, c := range cases {
		if got := versionLess(c.a, c.b); got != c.less {
			t.Errorf("versionLess(%q, %q) = %v, want %v", c.a, c.b, got, c.less)
		}
	}
}

func TestCheckForUpdates(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("DSH_UPDATE_REPO", "dummy/repo")
	statePath = filepath.Join(updateStateDir(), "state.json")
	state = UpdateState{CurrentVersion: appVersion, Status: "idle"}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v1.2.0","published_at":"2026-01-01T00:00:00Z","assets":[{"name":"DSH.exe","browser_download_url":"http://x/DSH.exe","size":123}]}`))
	}))
	defer srv.Close()

	orig := githubLatestURL
	githubLatestURL = srv.URL
	defer func() { githubLatestURL = orig }()

	checkForUpdates()
	if state.Status != "available" {
		t.Fatalf("status = %s (err %s), want available", state.Status, state.Error)
	}
	if state.LatestVersion != "1.2.0" {
		t.Fatalf("latest = %s", state.LatestVersion)
	}
	if state.LatestURL != "http://x/DSH.exe" {
		t.Fatalf("url = %s", state.LatestURL)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state file missing: %v", err)
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v0.9.0","assets":[{"name":"DSH.exe","browser_download_url":"http://x/DSH.exe","size":1}]}`))
	}))
	defer srv2.Close()
	githubLatestURL = srv2.URL
	checkForUpdates()
	if state.Status != "up-to-date" {
		t.Fatalf("status = %s, want up-to-date", state.Status)
	}

	// 非版本号 tag（例如把版本号写进 release 标题而 tag 是别的名字）必须报错，
	// 不能静默判定为已是最新。
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"dsh","assets":[{"name":"DSH.exe","browser_download_url":"http://x/DSH.exe","size":1}]}`))
	}))
	defer srv3.Close()
	githubLatestURL = srv3.URL
	checkForUpdates()
	if state.Status != "error" {
		t.Fatalf("status = %s (err %s), want error for non-version tag", state.Status, state.Error)
	}
	if !strings.Contains(state.Error, "dsh") {
		t.Fatalf("error should mention the tag, got %q", state.Error)
	}

	t.Setenv("DSH_UPDATE_REPO", "")
	updaterRepo = "OWNER/REPO"
	checkForUpdates()
	if state.Status != "unconfigured" {
		t.Fatalf("status = %s, want unconfigured", state.Status)
	}
}
