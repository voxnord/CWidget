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
// (remembered for the widget; clamped for the taller settings view),
// corner rounding for the actual size, focus, and the always-on-top state
// per the stay-open mode.
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
    w, h := 380, 300
    if mode == "settings" {
        w, h = 380, 600
    }
    runtime.WindowSetSize(a.ctx, w, h)
    log.Printf("[window] ShowMode(%s): size set", mode)

    a.cfgMu.Lock()
    remember := a.cfg.RememberPos
    hasPos, px, py := a.cfg.HasPos, a.cfg.PosX, a.cfg.PosY
    a.cfgMu.Unlock()

    // Positioning model: the SAVED position belongs to the widget (main
    // window). The settings view is a taller, transient view of the same
    // anchor — it is clamped into the work area so it never dives under the
    // taskbar (any taskbar edge) or off the screen. Clamped settings
    // coordinates are never persisted (the watcher saves only in main mode).
    placed := false
    if remember && hasPos {
        switch mode {
        case "main":
            placed = popup.SetWindowPosAbs(WindowTitle, px, py)
        default: // settings
            if fx, fy, ok := popup.FitToWorkArea(px, py, w, h); ok {
                placed = popup.SetWindowPosAbs(WindowTitle, fx, fy)
                if placed && (fx != px || fy != py) {
                    log.Printf("[window] settings anchored at (%d,%d) clamped to (%d,%d) to fit the work area", px, py, fx, fy)
                }
            }
        }
        if !placed {
            log.Printf("[window] saved position (%d,%d) unavailable — falling back to tray", px, py)
        }
    }
    if !placed {
        popup.MoveNearTray(WindowTitle) // taskbar-edge aware
    }
    popup.RoundCorners(WindowTitle) // the region depends on the size — after the resize
    log.Printf("[window] ShowMode(%s): positioned", mode)

    a.cfgMu.Lock()
    stayOpen := a.cfg.StayOpen
    a.cfgMu.Unlock()

    runtime.WindowUnminimise(a.ctx)
    runtime.WindowShow(a.ctx)
    runtime.WindowSetAlwaysOnTop(a.ctx, stayOpen)
    popup.FocusWindow(WindowTitle)
    a.shownOnce.Store(true) // the position watcher starts persisting from now on
    a.curMode.Store(mode)   // set AFTER the move: no transient positions are ever persisted
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