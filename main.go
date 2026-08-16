package main

import (
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/getlantern/systray"
)

const (
	appName     = "DSH"
	windowTitle = "DeepSeek Harness"
	mutexName   = `Local\DSHDesktopApp`
	regRunPath  = `Software\Microsoft\Windows\CurrentVersion\Run`
	regRunValue = "DSH"
	windowW     = 1280
	windowH     = 800
)

func main() {
	// Per-monitor DPI awareness must be set before any window is created.
	enablePerMonitorDPI()

	// Main goroutine hosts the systray message pump (systray locks its OS
	// thread at package init). The webview lives on its own locked thread.
	runtime.LockOSThread()

	if !acquireSingleInstance() {
		focusExistingWindow()
		return
	}
	ensureDirs()
	setupLogging()

	app := newApp()
	initUpdater()
	go updaterStartupCheck(app)

	if ms := os.Getenv("DSH_AUTO_QUIT_MS"); ms != "" {
		// Testing aid: quit automatically after N milliseconds.
		if n, err := strconv.Atoi(ms); err == nil && n > 0 {
			go func() {
				time.Sleep(time.Duration(n) * time.Millisecond)
				app.onQuitFinal()
				systray.Quit()
			}()
		}
	}

	go app.serveLoop()
	go app.webviewLoop()

	app.showCh <- struct{}{} // open the first window

	systray.Run(app.trayReady, app.trayExit)

	// systray.Run returned => user chose 退出. Final cleanup.
	app.onQuitFinal()
	os.Exit(0)
}
