package main

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	webview "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

// UI manages the embedded WebView2 window.
type UI struct {
	mu        sync.Mutex
	wv        webview.WebView
	open      bool
	webviewOK atomic.Bool
	lastPaint time.Time
	hicon     windows.Handle
}

func newUI() *UI {
	u := &UI{}
	u.webviewOK.Store(true)
	return u
}

func (u *UI) set(wv webview.WebView) {
	u.mu.Lock()
	u.wv = wv
	u.open = true
	u.mu.Unlock()
}

func (u *UI) clear(wv webview.WebView) {
	u.mu.Lock()
	if u.wv == wv {
		u.wv = nil
		u.open = false
	}
	u.mu.Unlock()
}

func (u *UI) isOpen() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.open && u.wv != nil
}

func (u *UI) hwnd() uintptr {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.wv == nil {
		return 0
	}
	return uintptr(u.wv.Window())
}

// render shows a status HTML page, throttled to avoid flooding the browser.
func (u *UI) render(page string) {
	u.mu.Lock()
	wv := u.wv
	open := u.open
	now := time.Now()
	if now.Sub(u.lastPaint) < 150*time.Millisecond {
		u.mu.Unlock()
		return
	}
	u.lastPaint = now
	u.mu.Unlock()
	if !open || wv == nil {
		return
	}
	wv.Dispatch(func() { wv.SetHtml(page) })
}

func (u *UI) navigate(url string) {
	u.mu.Lock()
	wv := u.wv
	open := u.open
	u.mu.Unlock()
	if !open || wv == nil {
		return
	}
	wv.Dispatch(func() { wv.Navigate(url) })
}

// close destroys the window; the webview loop then parks until shown again.
func (u *UI) close() {
	u.mu.Lock()
	wv := u.wv
	u.mu.Unlock()
	if wv != nil {
		wv.Destroy()
	}
}

// webviewLoop runs on its own locked thread; it owns the window lifecycle.
func (a *App) webviewLoop() {
	runtime.LockOSThread()
	for {
		select {
		case <-a.quitCh:
			return
		case <-a.showCh:
			a.showWindow()
		}
	}
}

func (a *App) showWindow() {
	debugMode := os.Getenv("DSH_DEBUG") != ""
	dataPath, ok := webviewDataPath()
	if !ok {
		// Data directory is not writable — fall back to the system browser.
		a.ui.webviewOK.Store(false)
		if url := a.server.getURL(); url != "" {
			openBrowser(url)
		} else {
			msgBox("无法初始化内嵌浏览器（数据目录不可写），将改用系统浏览器打开。")
		}
		return
	}
	opts := webview.WebViewOptions{
		Debug:    debugMode,
		DataPath: dataPath,
		WindowOptions: webview.WindowOptions{
			Title:  windowTitle,
			Width:  windowW,
			Height: windowH,
			Center: true,
		},
	}
	var wv webview.WebView
	func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("webview init panic: %v\n%s", r, debug.Stack())
				wv = nil
			}
		}()
		wv = webview.NewWithOptions(opts)
	}()
	if wv == nil {
		a.ui.webviewOK.Store(false)
		if url := a.server.getURL(); url != "" {
			openBrowser(url)
		} else {
			msgBox("无法创建内嵌窗口（WebView2 运行时不可用）。\n\n服务就绪后将自动改用系统浏览器打开。")
		}
		return
	}

	a.ui.set(wv)
	wv.SetSize(windowW, windowH, webview.HintNone)
	// In-window action buttons (used by the "install Node.js" guide page).
	_ = wv.Bind("dshOpenNodeDownload", func() {
		openBrowser("https://nodejs.org/zh-cn/download")
	})
	_ = wv.Bind("dshRetryAfterInstall", func() {
		if _, err := locateNode(); err != nil {
			// Still not installed — tell the user explicitly instead of
			// silently showing the guide page again.
			msgBox("仍未检测到 Node.js，无法启动服务。\n\n" +
				"请先完成 Node.js 安装，再点击「我已安装，重启服务」。\n\n" +
				"验证是否安装成功：按 Win+R 输入 cmd 回车，执行：\n" +
				"    node -v\n" +
				"显示 v 开头版本号（如 v24.19.0）即为安装成功。\n" +
				"若刚安装完仍检测不到，请重启电脑后再试。")
			return
		}
		a.requestRestart()
	})
	if hicon := iconHandle(); hicon != 0 {
		setWindowIcon(uintptr(wv.Window()), hicon)
	}
	if url := a.server.getURL(); url != "" {
		wv.Navigate(url)
	} else {
		wv.SetHtml(a.pageForStatus())
	}
	wv.Run()
	a.ui.clear(wv)
}

// webviewDataPath returns the WebView2 user-data folder (verified writable)
// so the embedded browser has a clean, dedicated profile.
func webviewDataPath() (string, bool) {
	p := os.Getenv("DSH_WEBVIEW2_DATAPATH")
	if p == "" {
		p = filepath.Join(dshRoot(), "WebView2")
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		return "", false
	}
	probe := filepath.Join(p, ".probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", false
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return p, true
}

// ---------------------------------------------------------------------------
// HTML status pages
// ---------------------------------------------------------------------------

// htmlPage renders a status page with the loading spinner.
func htmlPage(body string) string {
	return htmlShell(true, body)
}

// htmlActionPage renders an interactive page (buttons) without the spinner.
func htmlActionPage(body string) string {
	return htmlShell(false, body)
}

func htmlShell(spinner bool, body string) string {
	spinnerHTML := ""
	if spinner {
		spinnerHTML = `<div class="spinner"></div>`
	}
	return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<style>
  html,body{height:100%;margin:0;background:#0b1220;color:#e5e7eb;
    font-family:"Segoe UI","Microsoft YaHei",system-ui,sans-serif;}
  .wrap{height:100%;display:flex;flex-direction:column;align-items:center;
    justify-content:center;text-align:center;padding:24px;}
  h1{font-size:22px;font-weight:600;margin:0 0 12px;}
  p{font-size:14px;margin:4px 0;}
  p.dim{color:#9ca3af;}
  button{font-size:14px;font-family:inherit;margin-top:14px;padding:10px 22px;
    border:1px solid #334155;border-radius:8px;background:#1e293b;color:#e2e8f0;
    cursor:pointer;}
  button:hover{background:#273449;}
  button.primary{background:#2563eb;border-color:#2563eb;color:#fff;font-weight:600;}
  button.primary:hover{background:#1d4ed8;}
  .spinner{width:40px;height:40px;border:4px solid #1f2a44;border-top-color:#3b82f6;
    border-radius:50%;margin-bottom:24px;animation:spin 1s linear infinite;}
  @keyframes spin{to{transform:rotate(360deg);}}
  .line{font-size:12px;color:#94a3b8;font-family:Consolas,monospace;margin:2px 0;}
</style>
</head>
<body>
<div class="wrap">` + spinnerHTML + body + `</div>
</body>
</html>`
}
