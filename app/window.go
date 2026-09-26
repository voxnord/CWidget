package app

import (
	"log"
	"sync"

	"github.com/voxnord/cwidget/popup"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

/* ----------------------------- window presentation ----------------------- */

var showMu sync.Mutex

// ShowMode is the single presentation entry point.
func (a *App) ShowMode(mode string) {
    Safe("window.ShowMode("+mode+")", func() { a.showMode(mode) })
}

// modeWindowSize returns the window size for a presentation mode. The
// backend is the single source of truth for sizes: the frontend layout is
// built to fit these. Tune here — one place per mode.
func modeWindowSize(mode string) (int, int) {
    switch mode {
    case "settings":
        return 380, 590
    default: // "main" and any future mode
        return 380, 300
    }
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
    w, h := modeWindowSize(mode)
    runtime.WindowSetSize(a.ctx, w, h)
    log.Printf("[window] ShowMode(%s): size set", mode)

    // Positioning model: the SAVED position belongs to the widget (main
    // window) and is applied EXACTLY as saved — no clamping. That is
    // deliberate user placement; the known trade-off is that if the
    // taskbar edge changes after the save, the widget may end up partially
    // under the panel. The settings view is a taller, transient view of
    // the same anchor — clamped into the work area so it never dives under
    // the taskbar. Clamped settings coordinates are never persisted.
    hasPos, px, py := a.rememberedPos()
    placed := false
    if a.rememberPos() && hasPos {
        switch mode {
        case "main":
            placed = popup.SetWindowPosAbs(WindowTitle, px, py)
        case "settings":
            if fx, fy, ok := popup.FitToWorkArea(px, py, w, h); ok {
                placed = popup.SetWindowPosAbs(WindowTitle, fx, fy)
                if placed && (fx != px || fy != py) {
                    log.Printf("[window] settings anchored at (%d,%d) clamped to (%d,%d)", px, py, fx, fy)
                }
            }
        default:
            // unknown mode — never guess the positioning branch
        }
        if !placed {
            log.Printf("[window] saved position (%d,%d) unavailable — falling back to tray", px, py)
        }
    }
    if !placed {
        popup.MoveNearTray(WindowTitle) // taskbar-edge aware
    }
    popup.RoundCorners(WindowTitle)
    log.Printf("[window] ShowMode(%s): positioned", mode)

    stayOpen := a.stayOpen()

    runtime.WindowUnminimise(a.ctx)
    runtime.WindowShow(a.ctx)
    runtime.WindowSetAlwaysOnTop(a.ctx, stayOpen)
    popup.FocusWindow(WindowTitle)
    a.shownOnce.Store(true)
    a.curMode.Store(mode)
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