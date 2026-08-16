package main

import (
	"errors"
	"html"
	"log"
	"sync"
	"time"
)

// StatusKind describes what the main window should display.
type StatusKind int

const (
	StatusStarting StatusKind = iota
	StatusInstalling
	StatusReady
	StatusError
	StatusStopped
	StatusNeedsNode
)

// Status is the current state of the dsh server, rendered into the window.
type Status struct {
	Kind  StatusKind
	Text  string
	Lines []string // install/update progress lines
	URL   string
}

// App wires the server manager, the UI and the tray together.
type App struct {
	server    *Server
	ui        *UI
	restartCh chan struct{}
	quitCh    chan struct{}
	showCh    chan struct{}
	quitOnce  sync.Once

	repairMu    sync.Mutex
	repairCount int

	statusMu sync.Mutex
	status   Status
}

func newApp() *App {
	return &App{
		server:    newServer(),
		ui:        newUI(),
		restartCh: make(chan struct{}, 1),
		quitCh:    make(chan struct{}),
		showCh:    make(chan struct{}, 1),
	}
}

func (a *App) setStatus(s Status) {
	a.statusMu.Lock()
	changed := a.status.Kind != s.Kind || a.status.Text != s.Text
	a.status = s
	a.statusMu.Unlock()
	if changed {
		log.Printf("status: kind=%d text=%s", s.Kind, s.Text)
	}
	if s.Kind == StatusReady {
		a.ui.navigate(s.URL)
		return
	}
	a.ui.render(a.pageForStatus())
}

// markReady stores the server URL, navigates the window to it and, when the
// embedded browser is unavailable, falls back to the system browser.
func (a *App) markReady(url string) {
	a.statusMu.Lock()
	a.status = Status{Kind: StatusReady, URL: url}
	a.statusMu.Unlock()
	a.ui.navigate(url)
	if !a.ui.webviewOK.Load() {
		openBrowser(url)
	}
}

func (a *App) pageForStatus() string {
	a.statusMu.Lock()
	s := a.status
	a.statusMu.Unlock()
	switch s.Kind {
	case StatusReady:
		return htmlPage("正在打开 <b>" + html.EscapeString(s.URL) + "</b> …")
	case StatusInstalling:
		lines := ""
		for _, l := range s.Lines {
			lines += `<div class="line">` + html.EscapeString(l) + `</div>`
		}
		body := "<h1>正在安装 / 更新组件…</h1>" + lines
		return htmlPage(body)
	case StatusError:
		body := `<h1 style="color:#f87171">出错了</h1>` +
			"<p>" + html.EscapeString(s.Text) + "</p>" +
			`<p class="dim">右击托盘图标 → 重启服务，或退出。</p>`
		return htmlPage(body)
	case StatusStopped:
		return htmlPage(`<h1 style="color:#f87171">服务已停止</h1><p class="dim">右击托盘图标 → 重启服务，或退出。</p>`)
	case StatusNeedsNode:
		return htmlActionPage(`
			<h1>需要安装 Node.js</h1>
			<p>DeepSeek Harness 需要 Node.js 才能运行，本机目前未检测到。</p>
			<p class="dim">Node.js 是免费开源运行时，安装约需 1-2 分钟。</p>
			<button onclick="dshOpenNodeDownload()">打开 Node.js 下载页</button>
			<p class="dim" style="margin-top:22px">安装完成后，点击下面的按钮重新检测并启动：</p>
			<button class="primary" onclick="dshRetryAfterInstall()">我已安装，重启服务</button>
			<p class="dim">也可右击托盘图标 → 重启服务。</p>`)
	default:
		return htmlPage("<h1>正在启动 DeepSeek Harness…</h1><p class=\"dim\">首次启动可能需要在后台安装组件，请稍候。</p>")
	}
}

// fail reports a server-bootstrap failure. Missing Node.js becomes an
// in-window install guide (StatusNeedsNode); anything else is a plain error.
// It blocks until the user restarts (returns reasonRestart for an immediate
// retry) or quits.
func (a *App) fail(err error) runReason {
	if errors.Is(err, ErrNodeMissing) {
		a.setStatus(Status{Kind: StatusNeedsNode})
	} else {
		a.setStatus(Status{Kind: StatusError, Text: err.Error()})
	}
	if a.awaitRestartOrQuit() {
		return reasonRestart
	}
	return reasonError
}

// serveLoop keeps the dsh server running, restarting on demand or after
// errors. It returns only when the app is quitting.
func (a *App) serveLoop() {
	for {
		reason := a.runServerOnce()
		switch reason {
		case reasonQuit:
			return
		case reasonRestart:
			continue
		default: // reasonError / reasonExit / reasonBootstrapFailed
			select {
			case <-a.restartCh:
				continue
			case <-a.quitCh:
				return
			case <-time.After(5 * time.Second):
				continue // auto retry
			}
		}
	}
}

// awaitRestartOrQuit blocks until a restart is requested or the app quits.
// It returns true when a restart was requested (the caller should retry
// immediately instead of falling into a delay branch).
func (a *App) awaitRestartOrQuit() bool {
	select {
	case <-a.restartCh:
		return true
	case <-a.quitCh:
		return false
	}
}

// onQuitFinal is called once the systray loop has ended.
func (a *App) onQuitFinal() {
	a.server.markQuitting()
	a.ui.close()
	a.quitOnce.Do(func() { close(a.quitCh) })
	a.server.kill()
}

// requestRestart is used by the tray; never blocks.
func (a *App) requestRestart() {
	select {
	case a.restartCh <- struct{}{}:
	default:
	}
}

// waitForProc waits (bounded) until the server process has fully exited.
func (a *App) waitForProc(done <-chan struct{}) {
	select {
	case <-done:
	case <-time.After(8 * time.Second):
	}
}
