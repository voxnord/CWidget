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

// ShowMode is the single presentation entry point: size per mode, position
// near the tray, corner rounding for the actual size, focus. Every step is
// logged — if the window ever fails to appear, the log shows exactly which
// step hung.
func (a *App) ShowMode(mode string) {
    if a.ctx == nil {
        return
    }
    if !showMu.TryLock() {
        log.Printf("[window] ShowMode(%s): another show is in flight — skipped", mode)
        return
    }
    defer showMu.Unlock()

    log.Printf("[window] ShowMode(%s): enter", mode)
    w, h := 380, 260
    if mode == "settings" {
        w, h = 380, 540
    }
    runtime.WindowSetSize(a.ctx, w, h)
    log.Printf("[window] ShowMode(%s): size set", mode)

    popup.MoveNearTray(WindowTitle)
    popup.RoundCorners(WindowTitle) // the region depends on the size — after the resize
    log.Printf("[window] ShowMode(%s): positioned", mode)

    runtime.WindowUnminimise(a.ctx)
    runtime.WindowShow(a.ctx)
    popup.FocusWindow(WindowTitle)
    log.Printf("[window] ShowMode(%s): shown + focused", mode)

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