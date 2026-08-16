package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		w.Write([]byte(`{"tag_name":"v1.2.0","name":"DSH Desktop v1.2.0","body":"- 新增 Release 标题与说明展示\n- 修复若干问题","published_at":"2026-01-01T00:00:00Z","assets":[{"name":"DSH.exe","browser_download_url":"http://x/DSH.exe","size":123}]}`))
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
	if state.LatestTitle != "DSH Desktop v1.2.0" {
		t.Fatalf("title = %q", state.LatestTitle)
	}
	if !strings.Contains(state.LatestNotes, "Release 标题") {
		t.Fatalf("notes = %q, want release notes body", state.LatestNotes)
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

// chunkReader returns at most chunk bytes per Read (progress throttling relies
// on multiple small reads).
type chunkReader struct {
	data  []byte
	pos   int
	chunk int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.pos >= len(c.data) {
		return 0, io.EOF
	}
	n := len(c.data) - c.pos
	if n > c.chunk {
		n = c.chunk
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, c.data[c.pos:c.pos+n])
	c.pos += n
	return n, nil
}

// TestProgressReader verifies progressReader writes intermediate and final
// percentages into the shared state.
func TestProgressReader(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	statePath = filepath.Join(updateStateDir(), "state.json")
	state = UpdateState{CurrentVersion: appVersion, Status: "downloading"}

	payload := bytes.Repeat([]byte("x"), 1000)
	cr := &chunkReader{data: payload, chunk: 250}
	pr := &progressReader{src: cr, total: 1000, lastFlush: time.Now()}
	buf := make([]byte, 4096)

	// 把 lastFlush 拨回过去，确保每次 Read 都会触发写盘（节流条件满足）。
	readAndCheck := func(want int) {
		pr.lastFlush = time.Now().Add(-time.Second)
		if _, err := pr.Read(buf); err != nil && err != io.EOF {
			t.Fatalf("read: %v", err)
		}
		stateMu.Lock()
		got := state.Progress
		stateMu.Unlock()
		if got != want {
			t.Fatalf("progress after %d bytes = %d, want %d", pr.done, got, want)
		}
	}

	readAndCheck(25) // 250/1000
	readAndCheck(50) // 500/1000
	readAndCheck(75) // 750/1000
	readAndCheck(100)
}

// TestDownloadProgress runs downloadUpdate end-to-end against a local server:
// status must move to downloading, progress to 100, and the file must land
// with the expected size. A wrong asset size must fail and remove the file.
func TestDownloadProgress(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	statePath = filepath.Join(updateStateDir(), "state.json")
	state = UpdateState{CurrentVersion: appVersion, Status: "idle"}
	saveUpdateState()

	payload := bytes.Repeat([]byte("x"), 1024*64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		w.Write(payload)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "DSH.exe.new")
	if err := downloadUpdate(srv.URL, int64(len(payload)), dest); err != nil {
		t.Fatalf("downloadUpdate: %v", err)
	}
	if state.Status != "downloading" {
		t.Fatalf("status = %s, want downloading (ready is set by applyUpdate)", state.Status)
	}
	if state.Progress != 100 {
		t.Fatalf("progress = %d, want 100", state.Progress)
	}
	if fi, err := os.Stat(dest); err != nil || fi.Size() != int64(len(payload)) {
		t.Fatalf("downloaded file size = %v (err %v), want %d", fi, err, len(payload))
	}

	// 大小校验失败：声明的大小与实际不符 → error，残留文件被删除。
	dest2 := filepath.Join(t.TempDir(), "DSH.exe.new")
	err := downloadUpdate(srv.URL, int64(len(payload))+1, dest2)
	if err == nil {
		t.Fatalf("size mismatch should fail")
	}
	if state.Status != "error" {
		t.Fatalf("status = %s, want error after size mismatch", state.Status)
	}
	if !strings.Contains(state.Error, "校验失败") {
		t.Fatalf("error = %q, want size-check message", state.Error)
	}
	if _, statErr := os.Stat(dest2); !os.IsNotExist(statErr) {
		t.Fatalf("stale file should be removed after failed download")
	}
}
