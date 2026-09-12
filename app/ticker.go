package app

import "time"

/* ------------------------------ таймер ---------------------------------- */

func (a *App) startTicker() {
    a.stopMu.Lock()
    defer a.stopMu.Unlock()
    if a.stopCh != nil {
        return
    }
    a.stopCh = make(chan struct{})
    ch := a.stopCh
    go func() {
        for {
            d := time.Duration(a.intervalMin()) * time.Minute
            t := time.NewTimer(d)
            select {
            case <-ch:
                t.Stop()
                return
            case <-t.C:
                a.BackgroundRefresh(false) // автообновление без кулдауна
            }
        }
    }()
}

func (a *App) stopTicker() {
    a.stopMu.Lock()
    defer a.stopMu.Unlock()
    if a.stopCh != nil {
        close(a.stopCh)
        a.stopCh = nil
    }
    a.cancelRetry() // смена интервала/выход: запланированный ретрай неактуален
}

func (a *App) intervalMin() int {
    a.cfgMu.Lock()
    defer a.cfgMu.Unlock()
    return a.cfg.RefreshMin
}