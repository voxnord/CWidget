package app

import (
	"log"
	"time"

	"github.com/voxnord/cwidget/config"
	"github.com/voxnord/cwidget/popup"
)

/* Window position persistence.
 *
 * A lightweight watcher polls the window position while it is visible AND
 * the window is in MAIN mode, saving it (debounced) into the config, so it
 * survives restarts and autostart. On show the saved coordinates are
 * restored — validated against the visible monitors first (multi-monitor
 * safe: if the monitor is gone, the default tray-corner placement is used).
 *
 * Two guards keep the saved position honest:
 *   - "only when visible": a hidden window never triggers a save, so the
 *     default (tray-corner) placement after autostart cannot overwrite the
 *     user's spot;
 *   - "only in main mode": the settings view is clamped to fit the work
 *     area, and those clamped coordinates must never be mistaken for the
 *     user's chosen widget position. */

const (
    posPollInterval = 800 * time.Millisecond
    posSaveDebounce = 500 * time.Millisecond
)

func (a *App) startPositionWatcher() {
    go Safe("position-watcher", func() {
        t := time.NewTicker(posPollInterval)
        defer t.Stop()
        for range t.C {
            a.pollPosition()
        }
    })
}

func (a *App) pollPosition() {
    a.cfgMu.Lock()
    enabled := a.cfg.RememberPos
    a.cfgMu.Unlock()
    if !enabled || !a.shownOnce.Load() {
        return
    }
    // Persist only the widget (main mode) position.
    if m, _ := a.curMode.Load().(string); m != "main" {
        return
    }
    if !popup.IsWindowVisible(WindowTitle) {
        return // hidden windows do not move; skip entirely
    }
    x, y, ok := popup.GetWindowPos(WindowTitle)
    if !ok {
        return
    }

    a.posMu.Lock()
    defer a.posMu.Unlock()
    if !a.posInit {
        // First observation of this session: anchor the baseline WITHOUT
        // writing the config — otherwise whatever default placement the
        // window got would overwrite the user's saved position.
        a.posInit = true
        a.posX, a.posY = x, y
        return
    }
    if x == a.posX && y == a.posY {
        return
    }
    a.posX, a.posY = x, y
    if a.posSaveTimer != nil {
        a.posSaveTimer.Stop()
    }
    a.posSaveTimer = time.AfterFunc(posSaveDebounce, func() {
        a.savePosition(a.posX, a.posY)
    })
}

func (a *App) savePosition(x, y int) {
    a.cfgMu.Lock()
    if !a.cfg.RememberPos {
        a.cfgMu.Unlock()
        return
    }
    a.cfg.HasPos = true
    a.cfg.PosX, a.cfg.PosY = x, y
    err := config.Save(a.cfg)
    a.cfgMu.Unlock()
    if err != nil {
        log.Printf("[window] position save failed: %v", err)
    }
}