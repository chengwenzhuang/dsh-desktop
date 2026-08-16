package main

import (
	"encoding/binary"
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/getlantern/systray"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// ---------------------------------------------------------------------------
// Systray
// ---------------------------------------------------------------------------

func (a *App) trayReady() {
	systray.SetIcon(iconICO)
	systray.SetTitle("DSH")
	systray.SetTooltip("DeepSeek Harness 桌面版")

	mShow := systray.AddMenuItem("打开主窗口", "显示或打开主窗口")
	mRestart := systray.AddMenuItem("重启服务", "重启 dsh web 服务")
	mUpdate := systray.AddMenuItem("更新 DSH", "将 @deepseek-ai/dsh 更新到最新版")
	systray.AddSeparator()
	mAuto := systray.AddMenuItem("开机自启", "登录 Windows 时自动启动 DSH")
	if isAutoStart() {
		mAuto.Check()
	}
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("退出", "退出并停止服务")

	go func() {
		for {
			select {
			case <-mShow.ClickedCh:
				a.onShow()
			case <-mRestart.ClickedCh:
				a.onRestart()
			case <-mUpdate.ClickedCh:
				a.onUpdate()
			case <-mAuto.ClickedCh:
				a.onToggleAutoStart(mAuto)
			case <-mQuit.ClickedCh:
				a.onQuit()
			}
		}
	}()
}

func (a *App) trayExit() {}

func (a *App) onShow() {
	if !a.ui.webviewOK.Load() {
		if url := a.server.getURL(); url != "" {
			openBrowser(url)
		} else {
			msgBox("服务尚未就绪，请稍后再试。")
		}
		return
	}
	if a.ui.isOpen() {
		bringToFront(a.ui.hwnd())
		return
	}
	select {
	case a.showCh <- struct{}{}:
	default:
	}
}

func (a *App) onRestart() {
	a.requestRestart()
}

func (a *App) onUpdate() {
	go func() {
		nodePath, npmCli, prefix := a.updateTargets()
		if nodePath == "" {
			a.setStatus(Status{Kind: StatusError, Text: "未找到可用的 Node.js 环境，无法更新。"})
			return
		}
		a.setStatus(Status{Kind: StatusInstalling, Text: "正在更新 @deepseek-ai/dsh 到最新版…"})
		err := a.runNpm(nodePath, npmCli, "install", "--prefix", prefix, "@deepseek-ai/dsh@latest")
		if err != nil {
			a.setStatus(Status{Kind: StatusError, Text: "更新失败：" + err.Error()})
			return
		}
		a.setStatus(Status{Kind: StatusStarting, Text: "更新完成，正在重启服务…"})
		a.requestRestart()
	}()
}

func (a *App) onToggleAutoStart(item *systray.MenuItem) {
	on := !isAutoStart()
	if err := setAutoStart(on); err != nil {
		msgBox("设置开机自启失败：" + err.Error())
		return
	}
	if on {
		item.Check()
	} else {
		item.Uncheck()
	}
}

func (a *App) onQuit() {
	a.onQuitFinal()
	systray.Quit()
}

// ---------------------------------------------------------------------------
// Single instance
// ---------------------------------------------------------------------------

var (
	instanceMutex windows.Handle
	instanceOnce  sync.Once
)

func acquireSingleInstance() bool {
	name, _ := windows.UTF16PtrFromString(mutexName)
	h, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		return false
	}
	if err != nil {
		return true // fail-open
	}
	instanceOnce.Do(func() { instanceMutex = h })
	return true
}

func focusExistingWindow() {
	user32 := syscall.NewLazyDLL("user32.dll")
	title, _ := windows.UTF16PtrFromString(windowTitle)
	hwnd, _, _ := user32.NewProc("FindWindowW").Call(0, uintptr(unsafe.Pointer(title)))
	if hwnd == 0 {
		return
	}
	bringToFront(hwnd)
}

func bringToFront(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	user32 := syscall.NewLazyDLL("user32.dll")
	user32.NewProc("ShowWindow").Call(hwnd, 9) // SW_RESTORE
	user32.NewProc("SetForegroundWindow").Call(hwnd)
}

// ---------------------------------------------------------------------------
// Autostart (HKCU Run key)
// ---------------------------------------------------------------------------

func isAutoStart() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, regRunPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(regRunValue)
	return err == nil
}

func setAutoStart(on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, regRunPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if on {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return k.SetStringValue(regRunValue, `"`+exe+`"`)
	}
	return k.DeleteValue(regRunValue)
}

// ---------------------------------------------------------------------------
// Misc Windows helpers
// ---------------------------------------------------------------------------

func msgBox(text string) {
	user32 := syscall.NewLazyDLL("user32.dll")
	p := user32.NewProc("MessageBoxW")
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString(appName)
	p.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), 0x40 /*MB_ICONINFORMATION*/)
}

func openBrowser(url string) {
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

func enablePerMonitorDPI() {
	user32 := syscall.NewLazyDLL("user32.dll")
	proc := user32.NewProc("SetProcessDpiAwarenessContext")
	if proc.Find() != nil {
		return
	}
	const dpiPerMonitorV2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
	_, _, _ = proc.Call(dpiPerMonitorV2)
}

// ---------------------------------------------------------------------------
// Logging
// ---------------------------------------------------------------------------

var logFileOnce sync.Once

func ensureDirs() {
	_ = os.MkdirAll(logsDir(), 0o755)
	_ = os.MkdirAll(dshInstallDir(), 0o755)
}

func setupLogging() {
	logFileOnce.Do(func() {
		p := filepath.Join(logsDir(), "app.log")
		f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		log.SetOutput(f)
	})
}

func (a *App) logWriter(prefix string) func(string) {
	day := time.Now().Format("20060102")
	p := filepath.Join(logsDir(), prefix+"-"+day+".log")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return func(string) {}
	}
	return func(line string) {
		_, _ = f.WriteString(time.Now().Format("15:04:05") + " " + line + "\n")
	}
}

// ---------------------------------------------------------------------------
// Icon handling
// ---------------------------------------------------------------------------

// iconHandle creates an HICON from the embedded ICO (kept alive for the
// process lifetime).
var (
	iconOnce  sync.Once
	iconHIcon windows.Handle
)

func iconHandle() windows.Handle {
	iconOnce.Do(func() {
		h, err := iconFromICO(iconICO)
		if err != nil {
			log.Printf("iconFromICO: %v", err)
			return
		}
		iconHIcon = h
	})
	return iconHIcon
}

// iconFromICO parses a single (or multi-image) ICO container and creates an
// HICON from its largest PNG image. Works on Vista+.
func iconFromICO(data []byte) (windows.Handle, error) {
	if len(data) < 6 {
		return 0, errors.New("ico too short")
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if len(data) < 6+16*count {
		return 0, errors.New("ico entries truncated")
	}
	best, bestW := -1, 0
	for i := 0; i < count; i++ {
		off := 6 + 16*i
		w := int(data[off])
		if w == 0 {
			w = 256
		}
		if w >= bestW {
			bestW = w
			best = i
		}
	}
	if best < 0 {
		return 0, errors.New("no icon entries")
	}
	off := 6 + 16*best
	size := int(binary.LittleEndian.Uint32(data[off+8 : off+12]))
	imgOff := int(binary.LittleEndian.Uint32(data[off+12 : off+16]))
	if imgOff < 0 || size < 0 || imgOff+size > len(data) {
		return 0, errors.New("bad icon image offset")
	}
	img := data[imgOff : imgOff+size]
	user32 := syscall.NewLazyDLL("user32.dll")
	h, _, _ := user32.NewProc("CreateIconFromResourceEx").Call(
		uintptr(unsafe.Pointer(&img[0])),
		uintptr(uint32(size)),
		1, // fIcon
		0x00030000,
		0, 0,
		0, // LR_DEFAULTCOLOR
	)
	if h == 0 {
		return 0, errors.New("CreateIconFromResourceEx failed")
	}
	return windows.Handle(h), nil
}

const (
	wmSetIcon = 0x0080
	iconBig   = 1
	iconSmall = 0
)

func setWindowIcon(hwnd uintptr, hicon windows.Handle) {
	if hwnd == 0 {
		return
	}
	user32 := syscall.NewLazyDLL("user32.dll")
	send := user32.NewProc("SendMessageW")
	send.Call(hwnd, wmSetIcon, iconBig, uintptr(hicon))
	send.Call(hwnd, wmSetIcon, iconSmall, uintptr(hicon))
}
