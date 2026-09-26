package app

import (
	"log"
	"time"

	"github.com/voxnord/cwidget/config"
	"github.com/voxnord/cwidget/popup"
)

/* Window position persistence. The watcher polls the window position while
 * it is visible AND in MAIN mode, saving it (debounced) into the config. On
 * show the saved coordinates are restored, validated against the visible
 * monitors first. Guards keep the saved position honest: hidden windows and
 * the clamped settings view never trigger a save. The watcher is started/
 * stopped with the RememberPos toggle (setWatcherRunning). */

const (
    posPollInterval = 800 * time.Millisecond
    posSaveDebounce = 500 * time.Millisecond
)

func (a *App) startPositionWatcher() {
    a.watcherMu.Lock()
    defer a.watcherMu.Unlock()
    if a.watcherStop != nil {
        return
    }
    stop := make(chan struct{})
    a.watcherStop = stop
    go Safe("position-watcher", func() {
        t := time.NewTicker(posPollInterval)
        defer t.Stop()
        for {
            select {
            case <-stop:
                return
            case <-t.C:
                a.pollPosition()
            }
        }
    })
}

func (a *App) stopPositionWatcher() {
    a.watcherMu.Lock()
    defer a.watcherMu.Unlock()
    if a.watcherStop != nil {
        close(a.watcherStop)
        a.watcherStop = nil
    }
}

// setWatcherRunning starts/stops the watcher according to the toggle.
func (a *App) setWatcherRunning(run bool) {
    if run {
        a.startPositionWatcher()
    } else {
        a.stopPositionWatcher()
    }
}

func (a *App) pollPosition() {
    if !a.rememberPos() || !a.shownOnce.Load() {
        return
    }
    if m, _ := a.curMode.Load().(string); m != "main" {
        return
    }
    if !popup.IsWindowVisible(WindowTitle) {
        return
    }
    x, y, ok := popup.GetWindowPos(WindowTitle)
    if !ok {
        return
    }

    a.posMu.Lock()
    defer a.posMu.Unlock()
    if !a.posInit {
        a.posInit = true // anchor the baseline; the default placement is not a user choice
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
    if err := a.saveConfig(func(c *config.Config) {
        if !c.RememberPos {
            return // the toggle was switched off mid-drag
        }
        c.HasPos = true
        c.PosX, c.PosY = x, y
    }); err != nil {
        log.Printf("[window] position save failed: %v", err)
    }
}