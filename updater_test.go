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
	updaterTransport = http.DefaultTransport // 测试直连 httptest，不受本机系统代理影响

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
	updaterTransport = http.DefaultTransport // 测试直连 httptest，不受本机系统代理影响

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

func TestProxyParsing(t *testing.T) {
	cases := []struct{ server, scheme, want string }{
		{"127.0.0.1:10809", "https", "127.0.0.1:10809"},
		{"127.0.0.1:10809", "http", "127.0.0.1:10809"},
		{"http=127.0.0.1:10808;https=127.0.0.1:10809", "https", "127.0.0.1:10809"},
		{"http=127.0.0.1:10808;https=127.0.0.1:10809", "http", "127.0.0.1:10808"},
		{"http=127.0.0.1:10808", "https", ""},
		{"", "https", ""},
	}
	for _, c := range cases {
		if got := proxyForHost(c.server, c.scheme); got != c.want {
			t.Errorf("proxyForHost(%q, %q) = %q, want %q", c.server, c.scheme, got, c.want)
		}
	}
}

func TestBypassHost(t *testing.T) {
	bypass := []string{"localhost", "127.*", "10.*", "*.example.com", "<local>", "api.github.com"}
	cases := []struct{ host string; want bool }{
		{"127.0.0.1", true},
		{"localhost", true},
		{"10.1.2.3", true},
		{"sub.example.com", true},
		{"api.github.com", true},
		{"intranet-host", true}, // <local>：无点主机
		{"github.com", false},
		{"objects.githubusercontent.com", false},
	}
	for _, c := range cases {
		if got := bypassHost(bypass, c.host); got != c.want {
			t.Errorf("bypassHost(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

// TestGetWithRetry verifies transient 5xx failures are retried and a
// permanently failing endpoint eventually reports an error.
func TestGetWithRetry(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	client := &http.Client{}
	resp, err := getWithRetry(client, srv.URL, http.Header{"X-Test": {"1"}})
	if err != nil {
		t.Fatalf("getWithRetry: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || attempts != 3 {
		t.Fatalf("resp=%d attempts=%d, want 200/3", resp.StatusCode, attempts)
	}
	if got := resp.Header.Get("X-Test"); got != "" {
		t.Fatalf("server should not echo request header back, got %q", got)
	}

	always := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer always.Close()
	if _, err := getWithRetry(client, always.URL, nil); err == nil {
		t.Fatalf("expected error after retries exhausted")
	}
}

// TestDownloadMirrorEnv verifies DSH_UPDATE_MIRROR prefixes the download URL.
func TestDownloadMirrorEnv(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	statePath = filepath.Join(updateStateDir(), "state.json")
	state = UpdateState{CurrentVersion: appVersion, Status: "idle"}
	saveUpdateState()
	updaterTransport = http.DefaultTransport

	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case got <- r.URL.RequestURI():
		default:
		}
		w.Header().Set("Content-Length", "2")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	t.Setenv("DSH_UPDATE_MIRROR", srv.URL)
	dest := filepath.Join(t.TempDir(), "DSH.exe.new")
	githubURL := "https://github.com/owner/repo/releases/download/v1.0.2/DSH.exe"
	if err := downloadUpdate(githubURL, 0, dest); err != nil {
		t.Fatalf("downloadUpdate with mirror: %v", err)
	}
	select {
	case uri := <-got:
		if uri != "/"+githubURL {
			t.Fatalf("mirror URI = %q, want %q", uri, "/"+githubURL)
		}
	default:
		t.Fatalf("mirror server got no request")
	}
}
