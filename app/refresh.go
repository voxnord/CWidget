package app

import (
	"context"
	"time"

	"github.com/voxnord/cwidget/rates"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

/* -------------------------- обновление курсов --------------------------- */

// ManualRefresh — принудительное обновление по кнопке (трей или окно).
func (a *App) ManualRefresh() {
    go a.BackgroundRefresh(true)
}

// BackgroundRefresh: manual=true → после завершения включается минутный кулдаун
// (трей + кнопка в окне). Автообновление по таймеру кулдаун не включает.
func (a *App) BackgroundRefresh(manual bool) {
    if !a.busy.CompareAndSwap(false, true) {
        return // защита от спама кнопкой
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

    if manual {
        a.startCooldown(time.Minute)
    }
}

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

// publish рассылает результат обновления: событие фронтенду + метка времени в трее.
func (a *App) publish(tbl *rates.Table, err error) {
    if a.ctx == nil {
        return
    }
    if err != nil {
        runtime.EventsEmit(a.ctx, "rates:error", err.Error())
        if a.trayB != nil {
            a.trayB.NotifyFailure(err.Error())
        }
        return
    }
    payload := ratesUpdate{Table: tbl, Names: a.ratesM.Names()}
    runtime.EventsEmit(a.ctx, "rates:update", payload)
    if a.trayB != nil {
        a.trayB.SetUpdatedLabel(tbl.FetchedAt)
    }
}