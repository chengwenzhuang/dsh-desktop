package main

import (
	"encoding/json"
	"log"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// appVersion is the current desktop version. It must match the
// winres.json file_version and the GitHub release tag (v<version>).
const appVersion = "1.0.1"

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
	LatestURL      string `json:"latestUrl"`
	AssetSize      int64  `json:"assetSize"`
	PublishedAt    string `json:"publishedAt"`
	Status         string `json:"status"`
	Error          string `json:"error"`
	Request        string `json:"request"` // "" | "check" | "apply"
	LastCheckedAt  string `json:"lastCheckedAt"`
	Downloaded     bool   `json:"downloaded"`
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
	stateMu.Unlock()
	saveUpdateState()
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
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		setUpdateStatus("error", "构造请求失败: "+err.Error())
		return
	}
	req.Header.Set("User-Agent", "DSH-Desktop/"+appVersion)
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
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
	oldExe := filepath.Join(exeDir, "DSH.exe.old")

	setUpdateStatus("downloading", "")
	req, _ := http.NewRequest("GET", latestURL, nil)
	req.Header.Set("User-Agent", "DSH-Desktop/"+appVersion)
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		setUpdateStatus("error", "下载失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		setUpdateStatus("error", "下载失败: HTTP "+resp.Status)
		return
	}
	out, err := os.Create(newExe)
	if err != nil {
		setUpdateStatus("error", "无法创建更新文件: "+err.Error())
		return
	}
	_, copyErr := io.Copy(out, resp.Body)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(newExe)
		setUpdateStatus("error", "下载写入失败")
		return
	}
	if assetSize > 0 {
		if fi, err := os.Stat(newExe); err == nil && fi.Size() != assetSize {
			os.Remove(newExe)
			setUpdateStatus("error", fmt.Sprintf("下载校验失败（期望 %d 字节，实际 %d）", assetSize, fi.Size()))
			return
		}
	}

	setUpdateStatus("ready", "")
	log.Printf("updater: downloaded %s -> %s", latestURL, newExe)

	// 分离助手：等 2 秒（本进程完全退出）→ 换名 → 重新拉起。
	script := fmt.Sprintf(`timeout /t 2 /nobreak >nul & move /y "%s" "%s" & move /y "%s" "%s" & del /q "%s" & start "" "%s"`, exe, oldExe, newExe, exe, oldExe, exe)
	if err := exec.Command("cmd", "/c", "start", "/b", "", "cmd", "/c", script).Start(); err != nil {
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
		time.Sleep(2 * time.Second)
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
