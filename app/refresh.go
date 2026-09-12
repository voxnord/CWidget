package app

import (
	"context"
	"time"

	"github.com/voxnord/cwidget/rates"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

/* -------------------------- обновление курсов --------------------------- */

const (
    retryBaseDelay = 30 * time.Second  // первый ретрай после полного отказа
    retryMaxDelay  = 10 * time.Minute  // потолок backoff
)

// ManualRefresh — принудительное обновление по кнопке (трей или окно).
func (a *App) ManualRefresh() {
    go a.BackgroundRefresh(true)
}

// BackgroundRefresh: manual=true → после завершения включается минутный кулдаун
// (трей + кнопка в окне). Автообновление (таймер, ретрай) кулдаун не включает.
// После полного отказа (не ответил никто) планируется backoff-ретрай,
// после успеха — счётчик отказов и запланированный ретрай сбрасываются.
func (a *App) BackgroundRefresh(manual bool) {
    if !a.busy.CompareAndSwap(false, true) {
        return // защита от спама кнопкой; параллельная попытка и так идёт
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
        a.scheduleRetry() // сеть/API легли — вернёмся раньше планового тика
    } else {
        a.failures.Store(0)
        a.cancelRetry()
    }

    if manual {
        a.startCooldown(time.Minute) // кулдаун только для ручных нажатий
    }
}

// scheduleRetry — backoff: 30 c, 1 мин, 2 мин, 4 мин, 8 мин, далее кап 10 мин.
// Активен максимум один ретрай; повторный вызов перезапускает отсчёт.
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

// cancelRetry отменяет запланированный ретрай (успех, смена интервала, выход).
func (a *App) cancelRetry() {
    a.retryMu.Lock()
    if a.retryTimer != nil {
        a.retryTimer.Stop()
        a.retryTimer = nil
    }
    a.retryMu.Unlock()
}

// BackgroundRefreshFalse — адаптер для time.AfterFunc (без кулдауна).
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

// isFallback — данные пришли от резерва, а не от выбранного провайдера.
// Пустой SourceKey (дисковый кэш от версии без ключей) деградацией НЕ считается:
// после первого успешного обновления ключ проставится менеджером.
func isFallback(tbl *rates.Table, preferred string) bool {
    return tbl != nil && tbl.SourceKey != "" && preferred != "" && tbl.SourceKey != preferred
}

// publish рассылает результат обновления: событие фронтенду + синхронизация трея
// (метка времени, деградация, «нет связи»).
func (a *App) publish(tbl *rates.Table, err error) {
    if a.ctx == nil {
        return
    }
    if err != nil {
        a.stale.Store(true)
        runtime.EventsEmit(a.ctx, "rates:error", err.Error())
        if a.trayB != nil {
            a.trayB.NotifyFailure(err.Error())
            a.trayB.SetStale(true) // тултип иконки: «нет связи»
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