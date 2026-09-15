package app

import (
	"context"
	"time"

	"github.com/voxnord/cwidget/rates"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

/* ------------------------------ rates updates ---------------------------- */

const (
    retryBaseDelay = 30 * time.Second // first retry after a complete failure
    retryMaxDelay  = 10 * time.Minute // backoff cap
)

// ManualRefresh is the user-triggered refresh (tray menu or the window button).
func (a *App) ManualRefresh() {
    go a.BackgroundRefresh(true)
}

// BackgroundRefresh performs one refresh. manual=true -> after completion a
// one-minute cooldown is armed (tray + window button). Automatic updates
// (ticker, retry) never arm the cooldown. After a complete failure (no
// provider answered) a backoff retry is scheduled; after success the failure
// counter and any pending retry are reset.
func (a *App) BackgroundRefresh(manual bool) {
    if !a.busy.CompareAndSwap(false, true) {
        return // debounce: another attempt is already in flight
    }
    defer a.busy.Store(false)

    a.emitBusy(true)
    if a.trayB != nil {
        a.trayB.SetBusy(true)
    }
    tbl, err := a.ratesM.TryRefresh(context.Background())
    a.publish(tbl, err)
    if a.trayB != nil {
        a.trayB.SetBusy(false)
    }
    a.emitBusy(false)

    if err != nil {
        a.scheduleRetry() // network/API is down — come back sooner than the ticker
    } else {
        a.failures.Store(0)
        a.cancelRetry()
    }

    if manual {
        a.startCooldown(time.Minute) // cooldown for manual clicks only
    }
}

// scheduleRetry implements exponential backoff: 30s, 1m, 2m, 4m, 8m, capped
// at 10m. At most one retry is scheduled; a repeated call restarts the timer.
func (a *App) scheduleRetry() {
    n := a.failures.Add(1)
    d := retryBaseDelay
    for i := int64(1); i < n && d < retryMaxDelay; i++ {
        d *= 2
    }
    if d > retryMaxDelay {
        d = retryMaxDelay
    }
    a.retryMu.Lock()
    if a.retryTimer != nil {
        a.retryTimer.Stop()
    }
    a.retryTimer = time.AfterFunc(d, a.BackgroundRefreshFalse)
    a.retryMu.Unlock()
}

// cancelRetry drops a scheduled retry (success, interval change, quit).
func (a *App) cancelRetry() {
    a.retryMu.Lock()
    if a.retryTimer != nil {
        a.retryTimer.Stop()
        a.retryTimer = nil
    }
    a.retryMu.Unlock()
}

// BackgroundRefreshFalse is an adapter for time.AfterFunc (no cooldown).
func (a *App) BackgroundRefreshFalse() { a.BackgroundRefresh(false) }

func (a *App) emitBusy(b bool) {
    if a.ctx != nil {
        runtime.EventsEmit(a.ctx, "refresh:busy", b)
    }
}

func (a *App) startCooldown(d time.Duration) {
    until := time.Now().Add(d)
    a.cooldownUntil.Store(until.Unix())
    if a.trayB != nil {
        a.trayB.Cooldown(d)
    }
    if a.ctx != nil {
        runtime.EventsEmit(a.ctx, "refresh:cooldown", int(time.Until(until).Seconds())+1)
    }
}

// isFallback reports that data came from the fallback instead of the selected
// provider. An empty SourceKey (a disk cache written by an older build) does
// NOT count as degraded: the manager stamps the key on the first successful
// refresh.
func isFallback(tbl *rates.Table, preferred string) bool {
    return tbl != nil && tbl.SourceKey != "" && preferred != "" && tbl.SourceKey != preferred
}

// publish distributes a refresh result: an event to the frontend plus tray
// synchronization (updated-at label, degraded and offline states).
func (a *App) publish(tbl *rates.Table, err error) {
    if a.ctx == nil {
        return
    }
    if err != nil {
        a.stale.Store(true)
        runtime.EventsEmit(a.ctx, "rates:error", err.Error())
        if a.trayB != nil {
            a.trayB.NotifyFailure(err.Error())
            a.trayB.SetStale(true) // tray icon tooltip: "offline"
        }
        return
    }
    a.stale.Store(false)
    preferred := a.ratesM.Preferred()
    payload := ratesUpdate{
        Table:    tbl,
        Names:    a.ratesM.Names(),
        Provider: preferred,
        Fallback: isFallback(tbl, preferred),
    }
    runtime.EventsEmit(a.ctx, "rates:update", payload)
    if a.trayB != nil {
        a.trayB.SetUpdatedLabel(tbl.FetchedAt)
        a.trayB.SetStale(false)
        if payload.Fallback {
            a.trayB.SetDegraded(true, preferred, tbl.SourceKey)
        } else {
            a.trayB.SetDegraded(false, "", "")
        }
    }
}