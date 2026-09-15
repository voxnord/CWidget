package app

import (
	"github.com/voxnord/cwidget/popup"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

/* ----------------------------- window presentation ----------------------- */

// ShowMode is the single presentation entry point: size per mode, position
// near the tray, corner rounding for the actual size, focus.
func (a *App) ShowMode(mode string) {
    if a.ctx == nil {
        return
    }
    w, h := 380, 260
    if mode == "settings" {
        w, h = 380, 540
    }
    runtime.WindowSetSize(a.ctx, w, h)
    popup.MoveNearTray(WindowTitle)
    popup.RoundCorners(WindowTitle) // the region depends on the size — after the resize
    runtime.WindowUnminimise(a.ctx)
    runtime.WindowShow(a.ctx)
    popup.FocusWindow(WindowTitle)
    runtime.EventsEmit(a.ctx, "ui:mode", mode)
}

func (a *App) ShowWidget()       { a.ShowMode("main") }
func (a *App) OpenSettingsView() { a.ShowMode("settings") }

func (a *App) Quit() {
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