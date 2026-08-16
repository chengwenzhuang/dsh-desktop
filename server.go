package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var urlRe = regexp.MustCompile(`http://(?:127\.0\.0\.1|localhost):\d+`)

// ErrNodeMissing marks failures caused by a missing/broken Node.js install,
// which the UI turns into an in-window install guide with a restart button.
var ErrNodeMissing = errors.New("node missing")

// Server owns the dsh web child process.
type Server struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	job      windows.Handle
	url      string
	ready    bool
	quitting bool

	tailMu sync.Mutex
	tail   []string // most recent server output lines (for error pages)
}

func newServer() *Server { return &Server{} }

func (s *Server) pushTail(line string) {
	s.tailMu.Lock()
	defer s.tailMu.Unlock()
	s.tail = append(s.tail, line)
	if len(s.tail) > 12 {
		s.tail = s.tail[len(s.tail)-12:]
	}
}

func (s *Server) tailText() string {
	s.tailMu.Lock()
	defer s.tailMu.Unlock()
	return strings.Join(s.tail, "\n")
}

// autoRepair removes a broken locally-installed dsh and reinstalls it (or
// falls back to the npx-cached copy). It runs at most once per process;
// returns true when the caller should retry starting the server.
func (a *App) autoRepair(nodePath, npmCli string) bool {
	if os.Getenv("DSH_DISABLE_AUTO_REPAIR") != "" {
		return false
	}
	a.repairMu.Lock()
	defer a.repairMu.Unlock()
	if a.repairCount >= 1 || nodePath == "" {
		return false
	}
	a.repairCount++
	log.Printf("auto-repair: dsh install broken, reinstalling")
	a.setStatus(Status{Kind: StatusInstalling, Text: "检测到 dsh 组件异常，正在自动修复（重新安装，请稍候）…"})
	if err := os.RemoveAll(dshInstallDir()); err != nil {
		log.Printf("auto-repair: remove failed: %v", err)
	}
	if _, err := a.ensureDshInstalled(nodePath, npmCli); err != nil {
		log.Printf("auto-repair: reinstall failed: %v", err)
		return false
	}
	return true
}

func (s *Server) setProc(cmd *exec.Cmd) {
	s.mu.Lock()
	s.cmd = cmd
	s.mu.Unlock()
}

func (s *Server) setURL(u string) {
	s.mu.Lock()
	s.url = u
	s.ready = true
	s.mu.Unlock()
}

func (s *Server) getURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.url
}

func (s *Server) markQuitting() {
	s.mu.Lock()
	s.quitting = true
	s.mu.Unlock()
}

func (s *Server) isQuitting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.quitting
}

// kill terminates the whole process tree. The job object with
// KILL_ON_JOB_CLOSE additionally guarantees no orphans even if we crash.
func (s *Server) kill() {
	s.mu.Lock()
	cmd := s.cmd
	s.cmd = nil
	job := s.job
	s.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		// 1) whole tree via taskkill, 2) direct TerminateProcess fallback.
		_ = exec.Command("taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run()
		_ = cmd.Process.Kill()
	}
	if job != 0 {
		_ = windows.TerminateJobObject(job, 1)
		_ = windows.CloseHandle(job)
	}
}

// assignJob puts the child into a job object that kills the whole tree when
// this process exits (crash-safe cleanup).
func (s *Server) assignJob(cmd *exec.Cmd) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	if err := windows.AssignProcessToJobObject(job, procHandle(cmd)); err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	s.mu.Lock()
	s.job = job
	s.mu.Unlock()
	return nil
}

type runReason int

const (
	reasonError runReason = iota
	reasonRestart
	reasonQuit
	reasonExit
)

// runServerOnce boots the server and blocks until it is ready, exits, is
// restarted, or the app quits.
func (a *App) runServerOnce() runReason {
	a.setStatus(Status{Kind: StatusStarting, Text: "正在启动 DeepSeek Harness 服务…"})

	nodePath, npmCli, bin, err := a.resolveRuntime()
	if err != nil {
		return a.fail(err)
	}

	// Install the plugins bundled inside this exe (dsh-local-plugins) into the
	// web profile before the server boots. Idempotent; failures are non-fatal.
	ensureBundledPlugins(bin)

	cmd := exec.Command(nodePath, bin, "web", "--port", "0")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return a.fail(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return a.fail(err)
	}
	if err := cmd.Start(); err != nil {
		return a.fail(err)
	}
	a.server.setProc(cmd)
	_ = a.server.assignJob(cmd) // best effort

	urlCh := make(chan string, 4)
	logw := a.logWriter("server")
	scan := func(r io.Reader) {
		scanPipe(r, func(line string) {
			logw(line)
			a.server.pushTail(line)
			if u := urlRe.FindString(line); u != "" {
				select {
				case urlCh <- u:
				default:
				}
			}
		})
	}
	go scan(stdout)
	go scan(stderr)

	done := make(chan struct{})
	var exitErr error
	go func() {
		exitErr = cmd.Wait()
		close(done)
	}()

	// Phase 1: wait until the server prints its URL.
	select {
	case u := <-urlCh:
		a.server.setURL(u)
		a.markReady(u)
	case <-time.After(240 * time.Second):
		a.server.kill()
		a.setStatus(Status{Kind: StatusError, Text: "服务启动超时（240 秒）。请右击托盘图标 → 重启服务。"})
		a.waitForProc(done)
		return reasonError
	case <-done:
		// The server exited before reporting its URL. Most likely the local
		// dsh install is broken — repair it once, then retry automatically.
		if a.server.isQuitting() {
			return reasonQuit
		}
		if a.autoRepair(nodePath, npmCli) {
			return reasonRestart
		}
		tail := a.server.tailText()
		if tail != "" {
			a.setStatus(Status{Kind: StatusError, Text: "服务启动失败（详见下方输出）。\n\n" + tail})
		} else {
			a.setStatus(Status{Kind: StatusError, Text: "服务启动失败（无输出）。请右击托盘图标 → 重启服务。"})
		}
		return reasonError
	case <-a.quitCh:
		a.server.kill()
		a.waitForProc(done)
		return reasonQuit
	case <-a.restartCh:
		a.server.kill()
		a.waitForProc(done)
		return reasonRestart
	}

	// Phase 2: server is up; wait for exit / restart / quit.
	select {
	case <-done:
		if a.server.isQuitting() {
			return reasonQuit
		}
		if exitErr == nil {
			// 干净退出（exit code 0，例如应用内“重启服务”请求）：自动拉起，不卡错误页。
			a.setStatus(Status{Kind: StatusStarting, Text: "服务已停止，正在自动重启…"})
			return reasonRestart
		}
		a.setStatus(Status{Kind: StatusError, Text: "服务已退出。"})
		if a.awaitRestartOrQuit() {
			return reasonRestart
		}
		return reasonExit
	case <-a.quitCh:
		a.server.kill()
		a.waitForProc(done)
		return reasonQuit
	case <-a.restartCh:
		a.server.kill()
		a.waitForProc(done)
		return reasonRestart
	}
}

// ---------------------------------------------------------------------------
// dsh / Node discovery and bootstrap
// ---------------------------------------------------------------------------

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// procHandle opens a handle to the child process for job-object assignment.
func procHandle(cmd *exec.Cmd) windows.Handle {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return 0
	}
	return h
}

func dshRoot() string {
	if p := os.Getenv("DSH_ROOT"); p != "" {
		return p
	}
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "DSH")
}

func dshInstallDir() string {
	return filepath.Join(dshRoot(), "dsh")
}

func dshBinPath() string {
	return filepath.Join(dshInstallDir(), "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
}

func logsDir() string {
	if p := os.Getenv("DSH_LOG_DIR"); p != "" {
		return p
	}
	return filepath.Join(dshRoot(), "logs")
}

func locateNode() (string, error) {
	if os.Getenv("DSH_FORCE_NODE_MISSING") != "" {
		return "", fmt.Errorf("%w: 未检测到 Node.js（测试开关）", ErrNodeMissing)
	}
	dirs := []string{
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("LOCALAPPDATA"),
	}
	for _, d := range dirs {
		if d == "" {
			continue
		}
		c := filepath.Join(d, "nodejs", "node.exe")
		if fileExists(c) {
			return c, nil
		}
	}
	if p, err := exec.LookPath("node.exe"); err == nil && fileExists(p) {
		return p, nil
	}
	return "", fmt.Errorf("%w: 未找到 Node.js。", ErrNodeMissing)
}

func locateNpmCli(nodePath string) (string, error) {
	c := filepath.Join(filepath.Dir(nodePath), "node_modules", "npm", "bin", "npm-cli.js")
	if fileExists(c) {
		return c, nil
	}
	// Fallback: search adjacent installations.
	dirs := []string{
		filepath.Join(os.Getenv("ProgramFiles"), "nodejs"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "nodejs"),
	}
	for _, d := range dirs {
		c := filepath.Join(d, "node_modules", "npm", "bin", "npm-cli.js")
		if fileExists(c) {
			return c, nil
		}
	}
	return "", fmt.Errorf("%w: 未找到 npm（Node.js 安装不完整）。", ErrNodeMissing)
}

// ensureDshInstalled locates a usable dsh installation, installing it into
// %LOCALAPPDATA%\DSH\dsh on first run if necessary.
func (a *App) ensureDshInstalled(nodePath, npmCli string) (string, error) {
	if fileExists(dshBinPath()) {
		return dshBinPath(), nil
	}
	if bin, ok := findInNpxCache(); ok {
		return bin, nil
	}
	if bin, ok := findInNpmGlobal(nodePath, npmCli); ok {
		return bin, nil
	}

	a.setStatus(Status{Kind: StatusInstalling, Text: "首次运行：正在安装 @deepseek-ai/dsh…"})
	if err := a.runNpm(nodePath, npmCli, "install", "--prefix", dshInstallDir(), "@deepseek-ai/dsh"); err != nil {
		return "", fmt.Errorf("安装 dsh 失败：%v。请检查网络后，右击托盘图标 → 重启服务。", err)
	}
	if !fileExists(dshBinPath()) {
		return "", errors.New("安装 dsh 失败：未找到安装产物。请检查网络后重试。")
	}
	return dshBinPath(), nil
}

// findInNpxCache scans npm's npx cache for an existing dsh package (so a
// previously cached copy is reused instead of re-downloading).
func findInNpxCache() (string, bool) {
	base := filepath.Join(os.Getenv("LOCALAPPDATA"), "npm-cache", "_npx")
	matches, _ := filepath.Glob(filepath.Join(base, "*", "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js"))
	if len(matches) > 0 {
		return matches[0], true
	}
	return "", false
}

// findInNpmGlobal checks the global npm installation root.
func findInNpmGlobal(nodePath, npmCli string) (string, bool) {
	out, err := exec.Command(nodePath, npmCli, "root", "-g").Output()
	if err != nil {
		return "", false
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", false
	}
	bin := filepath.Join(root, "@deepseek-ai", "dsh", "lib", "bin.js")
	if fileExists(bin) {
		return bin, true
	}
	return "", false
}

// runNpm executes an npm command via node, streaming progress lines into the
// window.
func (a *App) runNpm(nodePath, npmCli string, args ...string) error {
	full := append([]string{npmCli}, args...)
	cmd := exec.Command(nodePath, full...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var mu sync.Mutex
	lines := []string{}
	cb := func(line string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, line)
		if len(lines) > 6 {
			lines = lines[1:]
		}
		cp := make([]string, len(lines))
		copy(cp, lines)
		a.setStatus(Status{Kind: StatusInstalling, Lines: cp})
	}
	scanPipe(stdout, cb)
	scanPipe(stderr, cb)
	return cmd.Wait()
}

func scanPipe(r io.Reader, fn func(string)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fn(sc.Text())
	}
}
