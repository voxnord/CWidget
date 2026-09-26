package app

import "time"

/* -------------------------------- ticker ---------------------------------
 * Scheduling is delegated to the manager: NextTick returns the sooner of
 * the regular interval and a publication-chase moment (see
 * rates.PublicationAware). The schedule is recomputed after every tick, so
 * a landed publication naturally reverts the cadence to the interval. */

func (a *App) startTicker() {
    a.stopMu.Lock()
    defer a.stopMu.Unlock()
    if a.stopCh != nil {
        return
    }
    a.stopCh = make(chan struct{})
    ch := a.stopCh
    go Safe("ticker", func() {
        for {
            interval := time.Duration(a.intervalMin()) * time.Minute
            next := a.ratesM.NextTick(time.Now(), interval)
            wait := time.Until(next)
            if wait < time.Minute {
                wait = time.Minute // safety: never spin
            }
            t := time.NewTimer(wait)
            select {
            case <-ch:
                t.Stop()
                return
            case <-t.C:
                a.BackgroundRefresh(false) // automatic refresh, no cooldown
            }
        }
    })
}

func (a *App) stopTicker() {
    a.stopMu.Lock()
    defer a.stopMu.Unlock()
    if a.stopCh != nil {
        close(a.stopCh)
        a.stopCh = nil
    }
    a.cancelRetry() // interval change/quit: a scheduled retry is no longer valid
}

func (a *App) intervalMin() int {
    a.cfgMu.Lock()
    defer a.cfgMu.Unlock()
    return a.cfg.RefreshMin
}