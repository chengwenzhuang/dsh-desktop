package main

import (
	"encoding/json"
	"log"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows/registry"
)

// appVersion is the current desktop version. It must match the
// winres.json file_version and the GitHub release tag (v<version>).
const appVersion = "1.0.2"

// updaterRepo is the GitHub repository holding the releases
// (OWNER/REPO). Publish: tag the commit v<version> and attach the DSH.exe
// asset to the release. Overridable via DSH_UPDATE_REPO (testing).
var updaterRepo = "chengwenzhuang/dsh-desktop"

// Update state machine shared with the web UI through the state file.
// Status values: idle | checking | available | up-to-date | unconfigured |
//                downloading | ready | error
type UpdateState struct {
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	LatestTitle    string `json:"latestTitle"`
	LatestNotes    string `json:"latestNotes"`
	LatestURL      string `json:"latestUrl"`
	AssetSize      int64  `json:"assetSize"`
	PublishedAt    string `json:"publishedAt"`
	Status         string `json:"status"`
	Error          string `json:"error"`
	Request        string `json:"request"` // "" | "check" | "apply"
	LastCheckedAt  string `json:"lastCheckedAt"`
	Downloaded     bool   `json:"downloaded"`
	Progress       int    `json:"progress"` // 0-100，status==downloading 时由下载进度上报
}

var (
	stateMu   sync.Mutex
	statePath string
	state     UpdateState
)

// updateStateDir is where the updater state file lives (shared with the
// host updater service): %LOCALAPPDATA%/DSH/update/state.json.
func updateStateDir() string {
	root := os.Getenv("LOCALAPPDATA")
	if strings.TrimSpace(root) == "" {
		root = filepath.Join(os.TempDir(), "DSH")
	}
	return filepath.Join(root, "DSH", "update")
}

// initUpdater loads the persisted state; the desktop owns the fields.
func initUpdater() {
	statePath = filepath.Join(updateStateDir(), "state.json")
	state = UpdateState{CurrentVersion: appVersion, Status: "idle"}
	data, err := os.ReadFile(statePath)
	if err == nil {
		var prev UpdateState
		if json.Unmarshal(data, &prev) == nil {
			state = prev
			state.CurrentVersion = appVersion // 版本以当前 exe 为准
			state.Request = ""
		}
	}
	saveUpdateState()
}

func saveUpdateState() {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		log.Printf("updater: marshal state failed: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		log.Printf("updater: mkdir failed: %v", err)
		return
	}
	tmp := statePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		log.Printf("updater: write state failed: %v", err)
		return
	}
	if err := os.Rename(tmp, statePath); err != nil {
		log.Printf("updater: rename state failed: %v", err)
	}
}

func setUpdateStatus(status, message string) {
	stateMu.Lock()
	state.Status = status
	state.Error = message
	state.Downloaded = status == "ready"
	switch status {
	case "ready":
		state.Progress = 100
	case "downloading":
		state.Progress = 0 // 新一轮下载从头计数，之后由 progressReader 写入
	default:
		state.Progress = 0
	}
	stateMu.Unlock()
	saveUpdateState()
}

// progressFlushInterval 是下载进度写盘的最小间隔，避免高频 io 拖慢下载。
const progressFlushInterval = 100 * time.Millisecond

// progressReader 包装下载响应体，边读边把百分比写入共享状态文件。
type progressReader struct {
	src       io.Reader
	total     int64
	done      int64
	lastFlush time.Time
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	if n > 0 {
		r.done += int64(n)
	}
	if n > 0 && (err != nil || time.Since(r.lastFlush) >= progressFlushInterval) {
		r.flush()
	}
	return n, err
}

func (r *progressReader) flush() {
	pct := 0
	if r.total > 0 {
		pct = int(r.done * 100 / r.total)
		if pct > 100 {
			pct = 100
		}
	}
	stateMu.Lock()
	state.Progress = pct
	stateMu.Unlock()
	saveUpdateState()
	r.lastFlush = time.Now()
}

// ── 网络访问：Windows 系统代理 + 重试 ──────────────────────────────────
//
// Go 的 http.ProxyFromEnvironment 只认 HTTP(S)_PROXY 环境变量，而 Windows
// 上这些变量通常为空——系统代理（WinINET，如 clash/v2ray 的 127.0.0.1:10809）
// 不会被读取，导致更新器绕过代理直连 GitHub，在国内网络下慢或连接被重置。
// 这里读取注册表里的系统代理，探活通过后用于更新请求；不通则退回直连，
// 不会比现状更差。环境变量代理优先级最高。

// winProxy 保存 Windows 系统（WinINET）代理设置。
type winProxy struct {
	enabled bool
	server  string // ProxyServer："host:port" 或 "http=a;https=b" 形式
	bypass  []string
}

// readWinProxy 从注册表读取当前用户的 Internet Settings 代理配置。
func readWinProxy() winProxy {
	const keyPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	k, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE)
	if err != nil {
		return winProxy{}
	}
	defer k.Close()
	enable, _, _ := k.GetIntegerValue("ProxyEnable")
	server, _, err := k.GetStringValue("ProxyServer")
	if err != nil || server == "" {
		return winProxy{}
	}
	override, _, _ := k.GetStringValue("ProxyOverride")
	var bypass []string
	for _, s := range strings.Split(override, ";") {
		if s = strings.TrimSpace(s); s != "" {
			bypass = append(bypass, s)
		}
	}
	return winProxy{enabled: enable != 0, server: server, bypass: bypass}
}

// proxyForHost 从 WinINET ProxyServer 串中取出某协议的代理地址
// （"host:port" 全局形式，或 "http=a;https=b" 按协议形式）。
func proxyForHost(server, scheme string) string {
	if strings.Contains(server, "=") {
		for _, part := range strings.Split(server, ";") {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) == 2 && strings.EqualFold(strings.TrimSpace(kv[0]), scheme) {
				return strings.TrimSpace(kv[1])
			}
		}
		return "" // 该协议未配置代理
	}
	return strings.TrimSpace(server)
}

// bypassHost 判断主机是否命中 ProxyOverride 绕过列表
// （<local> 内网、*.域后缀、127.* 等 IP 通配前缀、精确主机）。
func bypassHost(bypass []string, host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || host == "localhost" {
		return true
	}
	for _, e := range bypass {
		e = strings.ToLower(strings.TrimSpace(e))
		switch {
		case e == "":
		case e == "<local>":
			if !strings.Contains(host, ".") {
				return true
			}
		case strings.HasPrefix(e, "*."):
			if strings.HasSuffix(host, e[1:]) {
				return true
			}
		case strings.HasSuffix(e, "*"):
			if strings.HasPrefix(host, strings.TrimSuffix(e, "*")) {
				return true
			}
		case e == host:
			return true
		}
	}
	return false
}

// envHasProxy 报告环境变量代理是否已设置（优先级高于系统代理）。
func envHasProxy() bool {
	for _, name := range []string{"HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy"} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true
		}
	}
	return false
}

// updaterTransport 是所有更新网络请求共享的 RoundTripper。
var updaterTransport http.RoundTripper = buildUpdaterTransport()

func buildUpdaterTransport() http.RoundTripper {
	base := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if envHasProxy() {
		return base // 环境变量代理（如 CI/命令行设置）优先
	}
	sys := readWinProxy()
	if !sys.enabled || sys.server == "" {
		return base
	}
	addr := proxyForHost(sys.server, "https")
	if addr == "" {
		addr = proxyForHost(sys.server, "http")
	}
	if addr == "" || !strings.Contains(addr, ":") {
		return base
	}
	// 探活：代理端口可连才启用，否则退回直连（不引入新的失败路径）。
	conn, err := net.DialTimeout("tcp", addr, 400*time.Millisecond)
	if err != nil {
		log.Printf("updater: system proxy %s unreachable, using direct connection", addr)
		return base
	}
	conn.Close()
	log.Printf("updater: using system proxy %s for update checks/downloads", addr)
	base.Proxy = func(req *http.Request) (*url.URL, error) {
		if req.URL == nil || bypassHost(sys.bypass, req.URL.Hostname()) {
			return nil, nil
		}
		if p := proxyForHost(sys.server, req.URL.Scheme); p != "" {
			return url.Parse("http://" + p)
		}
		return nil, nil
	}
	return base
}

// getWithRetry 发起 GET 并附带请求头，网络错误或 HTTP 5xx/429 时最多重试
// 3 次（短退避）——连接被重置往往是瞬时的，重试即可恢复。
func getWithRetry(client *http.Client, url string, header http.Header) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 700 * time.Millisecond)
		}
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		for k, vs := range header {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %s", resp.Status)
			continue
		}
		return resp, nil
	}
	return nil, lastErr
}

// parseVersion splits "v1.2.3" into [1,2,3].
func parseVersion(v string) []int {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	var out []int
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			break
		}
		out = append(out, n)
	}
	return out
}

// versionLess reports whether a < b (dot-separated numeric compare).
func versionLess(a, b string) bool {
	va, vb := parseVersion(a), parseVersion(b)
	for i := 0; i < len(va) || i < len(vb); i++ {
		var x, y int
		if i < len(va) {
			x = va[i]
		}
		if i < len(vb) {
			y = vb[i]
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// githubLatestURL is the GitHub latest-release API base (overridable in tests).
var githubLatestURL = "https://api.github.com/repos/"

func updaterRepoName() string {
	if r := strings.TrimSpace(os.Getenv("DSH_UPDATE_REPO")); r != "" {
		return r
	}
	return updaterRepo
}

// checkForUpdates queries the GitHub latest release and updates the state.
func checkForUpdates() {
	stateMu.Lock()
	state.LastCheckedAt = time.Now().Format(time.RFC3339)
	stateMu.Unlock()
	setUpdateStatus("checking", "")

	repo := updaterRepoName()
	if repo == "" || repo == "OWNER/REPO" {
		setUpdateStatus("unconfigured", "更新源未配置（DSH_UPDATE_REPO / updaterRepo）")
		return
	}

	url := strings.TrimRight(githubLatestURL, "/") + "/" + repo + "/releases/latest"
	client := &http.Client{Transport: updaterTransport, Timeout: 15 * time.Second}
	resp, err := getWithRetry(client, url, http.Header{
		"User-Agent": {"DSH-Desktop/" + appVersion},
		"Accept":     {"application/vnd.github+json"},
	})
	if err != nil {
		setUpdateStatus("error", "网络请求失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		setUpdateStatus("error", "仓库未找到或还没有发布版本")
		return
	}
	if resp.StatusCode != http.StatusOK {
		setUpdateStatus("error", "GitHub 返回 HTTP "+resp.Status)
		return
	}

	var rel struct {
		TagName     string `json:"tag_name"`
		Name        string `json:"name"` // Release 标题
		Body        string `json:"body"` // Release 说明（Markdown 原文）
		PublishedAt string `json:"published_at"`
		Assets      []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		setUpdateStatus("error", "解析版本信息失败: "+err.Error())
		return
	}

	latest := strings.TrimPrefix(strings.TrimSpace(rel.TagName), "v")
	if len(parseVersion(latest)) == 0 {
		// 最新发布 tag 不是形如 v1.2.3 的版本号（例如误把版本号写进 release
		// 标题而 tag 是别的名字）——显式报错，而不是静默判定为“已是最新”。
		setUpdateStatus("error", "最新发布 tag 不是有效版本号: "+rel.TagName+"（发布时 tag 必须形如 v1.2.3）")
		return
	}
	var assetURL string
	var assetSize int64
	for _, a := range rel.Assets {
		if strings.EqualFold(a.Name, "DSH.exe") {
			assetURL = a.URL
			assetSize = a.Size
			break
		}
	}
	if latest == "" || assetURL == "" {
		setUpdateStatus("error", "最新发布缺少 DSH.exe 资产")
		return
	}

	stateMu.Lock()
	state.LatestVersion = latest
	state.LatestTitle = rel.Name
	state.LatestNotes = rel.Body
	state.LatestURL = assetURL
	state.AssetSize = assetSize
	state.PublishedAt = rel.PublishedAt
	stateMu.Unlock()

	if versionLess(appVersion, latest) {
		setUpdateStatus("available", "")
		log.Printf("updater: new version available: %s (current %s)", latest, appVersion)
	} else {
		setUpdateStatus("up-to-date", "")
		log.Printf("updater: up to date (%s)", appVersion)
	}
}

// downloadUpdate downloads the asset at url into dest, streaming download
// progress into the shared state while status == "downloading", then validates
// the downloaded size against the release asset size. All state transitions
// (downloading / error) are written here; on success the caller moves to ready.
func downloadUpdate(url string, assetSize int64, dest string) error {
	setUpdateStatus("downloading", "")
	// 可选下载镜像：DSH_UPDATE_MIRROR 前缀（如 https://ghproxy.net/），
	// 直连 GitHub 慢或被重置时可用镜像加速。
	if m := strings.TrimSpace(os.Getenv("DSH_UPDATE_MIRROR")); m != "" {
		url = strings.TrimRight(m, "/") + "/" + url
	}
	client := &http.Client{Transport: updaterTransport, Timeout: 10 * time.Minute}
	resp, err := getWithRetry(client, url, http.Header{"User-Agent": {"DSH-Desktop/" + appVersion}})
	if err != nil {
		setUpdateStatus("error", "下载失败: "+err.Error())
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("HTTP %s", resp.Status)
		setUpdateStatus("error", "下载失败: "+err.Error())
		return err
	}
	out, err := os.Create(dest)
	if err != nil {
		setUpdateStatus("error", "无法创建更新文件: "+err.Error())
		return err
	}
	total := assetSize
	if total <= 0 {
		total = resp.ContentLength
	}
	pr := &progressReader{src: resp.Body, total: total, lastFlush: time.Now()}
	_, copyErr := io.Copy(out, pr)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(dest)
		setUpdateStatus("error", "下载写入失败")
		return fmt.Errorf("copy/close failed: %v / %v", copyErr, closeErr)
	}
	if assetSize > 0 {
		if fi, err := os.Stat(dest); err == nil && fi.Size() != assetSize {
			os.Remove(dest)
			msg := fmt.Sprintf("下载校验失败（期望 %d 字节，实际 %d）", assetSize, fi.Size())
			setUpdateStatus("error", msg)
			return fmt.Errorf("size mismatch: got %d want %d", fi.Size(), assetSize)
		}
	}
	return nil
}

// applyUpdate downloads the new exe, spawns a detached helper that swaps it
// in place after this process exits, then exits (the helper relaunches).
func (a *App) applyUpdate() {
	stateMu.Lock()
	latestURL := state.LatestURL
	assetSize := state.AssetSize
	stateMu.Unlock()
	if latestURL == "" {
		setUpdateStatus("error", "没有可更新的版本")
		return
	}

	exe, err := os.Executable()
	if err != nil {
		setUpdateStatus("error", "无法定位当前 exe: "+err.Error())
		return
	}
	exeDir := filepath.Dir(exe)
	newExe := filepath.Join(exeDir, "DSH.exe.new")
	os.Remove(newExe) // 清理上次失败的残留
	os.Remove(filepath.Join(exeDir, "DSH.exe.old"))

	if err := downloadUpdate(latestURL, assetSize, newExe); err != nil {
		log.Printf("updater: download failed: %v", err)
		return
	}

	setUpdateStatus("ready", "")
	log.Printf("updater: downloaded %s -> %s", latestURL, newExe)

	// 分离的 PowerShell 助手：旧进程退出后 exe 文件才解锁，这里用重试循环
	// 等到可替换 → 替换 → 重新拉起。不用 cmd 的 timeout（无控制台环境会因
	// 输入重定向立即失败，导致在旧进程仍占用 exe 时执行替换而失败）。
	ps := fmt.Sprintf(
		`$exe = '%s'; $new = '%s'; for ($i = 0; $i -lt 60; $i++) { try { Move-Item -LiteralPath $new -Destination $exe -Force -ErrorAction Stop; break } catch { Start-Sleep -Milliseconds 1000 } }; if (Test-Path -LiteralPath $exe) { Start-Process -FilePath $exe }`,
		strings.ReplaceAll(exe, "'", "''"),
		strings.ReplaceAll(newExe, "'", "''"),
	)
	if err := exec.Command("powershell", "-NoProfile", "-WindowStyle", "Hidden", "-Command", ps).Start(); err != nil {
		setUpdateStatus("error", "无法启动更新助手: "+err.Error())
		return
	}
	setUpdateStatus("idle", "")
	log.Printf("updater: swapping exe and relaunching...")
	// 关掉 server（job 对象连带清理），随后本进程退出，助手完成替换并重启。
	a.server.markQuitting()
	a.server.kill()
	os.Exit(0)
}

// updaterPollLoop watches request.json (written by the host updater service)
// for actions requested from the web UI. A consumed request file is removed.
func (a *App) updaterPollLoop() {
	reqPath := filepath.Join(updateStateDir(), "request.json")
	for {
		time.Sleep(1 * time.Second) // 1s 轮询，点「立即更新」后更快开始下载
		data, err := os.ReadFile(reqPath)
		if err != nil {
			continue
		}
		var req struct {
			Request string `json:"request"`
		}
		if json.Unmarshal(data, &req) != nil || (req.Request != "check" && req.Request != "apply") {
			os.Remove(reqPath)
			continue
		}
		os.Remove(reqPath)
		switch req.Request {
		case "check":
			go checkForUpdates()
		case "apply":
			a.applyUpdate()
		}
	}
}

// updaterStartupCheck runs the auto version check at startup and starts the
// request-poll loop. Non-blocking; failures only affect the update UI.
func updaterStartupCheck(a *App) {
	go checkForUpdates()
	go a.updaterPollLoop()
}
