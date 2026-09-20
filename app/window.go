package app

import (
	"log"
	"sync"

	"github.com/voxnord/cwidget/popup"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

/* ----------------------------- window presentation ----------------------- */

// showMu serializes ShowMode: a second show request while one is still in
// flight is dropped instead of stacking up on the UI threads. TryLock keeps
// the tray responsive even if a show call hangs below.
var showMu sync.Mutex

// Window sizes per mode. The main window is a bit taller than the grid
// strictly requires: it leaves breathing room for the currency dropdowns,
// which can open either way inside the clipped rounded window.
const (
    mainW = 380
    mainH = 300
    setW  = 380
    setH  = 560
)

// ShowMode is the single presentation entry point: size per mode, position
// near the tray, corner rounding for the actual size, focus, and the
// always-on-top state per the stay-open mode. Every step is logged — if the
// window ever fails to appear, the log shows exactly which step hung.
// Panics are shielded: the call can come from a tray goroutine, where an
// uncaught panic would kill the whole process.
func (a *App) ShowMode(mode string) {
    Safe("window.ShowMode("+mode+")", func() { a.showMode(mode) })
}

func (a *App) showMode(mode string) {
    if a.ctx == nil {
        return
    }
    if !showMu.TryLock() {
        log.Printf("[window] ShowMode(%s): another show is in flight — skipped", mode)
        return
    }
    defer showMu.Unlock()

    log.Printf("[window] ShowMode(%s): enter", mode)
    w, h := mainW, mainH
    if mode == "settings" {
        w, h = setW, setH
    }
    runtime.WindowSetSize(a.ctx, w, h)
    log.Printf("[window] ShowMode(%s): size set", mode)

    popup.MoveNearTray(WindowTitle)
    popup.RoundCorners(WindowTitle) // the region depends on the size — after the resize
    log.Printf("[window] ShowMode(%s): positioned", mode)

    // Stay-open mode: keep the window on top of others while it is visible.
    // In the regular popup mode the window behaves like a normal one.
    a.cfgMu.Lock()
    stayOpen := a.cfg.StayOpen
    a.cfgMu.Unlock()

    runtime.WindowUnminimise(a.ctx)
    runtime.WindowShow(a.ctx)
    runtime.WindowSetAlwaysOnTop(a.ctx, stayOpen)
    popup.FocusWindow(WindowTitle)
    log.Printf("[window] ShowMode(%s): shown (topmost=%v)", mode, stayOpen)

    runtime.EventsEmit(a.ctx, "ui:mode", mode)
    log.Printf("[window] ShowMode(%s): exit", mode)
}

func (a *App) ShowWidget()       { a.ShowMode("main") }
func (a *App) OpenSettingsView() { a.ShowMode("settings") }

func (a *App) Quit() {
    log.Printf("[app] quit requested")
    a.exiting.Store(true)
    a.stopTicker()
    if a.ctx != nil {
        runtime.Quit(a.ctx)
    }
}

func (a *App) HideWindow() {
    if a.ctx != nil {
        runtime.WindowHide(a.ctx)
    }
}