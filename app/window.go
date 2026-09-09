package app

import (
	"github.com/voxnord/cwidget/popup"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

/* ----------------------------- показ окна ------------------------------- */

// ShowMode — единая точка показа: размер под режим, позиция у трея,
// скругление под фактический размер, фокус.
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
    popup.RoundCorners(WindowTitle) // регион зависит от размера — после ресайза
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