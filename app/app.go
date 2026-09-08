package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/you/currency-widget/config"
	"github.com/you/currency-widget/popup"
	"github.com/you/currency-widget/rates"
)

// WindowTitle — уникальный заголовок окна. Задаётся из main.go ДО wails.Run:
// popup-модуль ищет окно по заголовку (убрать из таскбара, поставить у трея).
var WindowTitle string

// TrayBridge — то, чего приложение требует от трея (реализовано tray.Tray).
type TrayBridge interface {
    SetUpdatedLabel(at time.Time)
    SetAutoState(on bool)
    SetBusy(busy bool)
    Cooldown(d time.Duration)
    NotifyFailure(reason string)
}

type ratesUpdate struct {
    Table *rates.Table      `json:"table"`
    Names map[string]string `json:"names"`
}

type InitPayload struct {
    Config      config.Config `json:"config"`
    AutoEnabled bool          `json:"autostart_enabled"`
    Update      ratesUpdate   `json:"update"`
}

type App struct {
    ctx    context.Context
    cfg    *config.Config
    cfgMu  sync.Mutex
    ratesM *rates.Manager
    trayB  TrayBridge

    exiting atomic.Bool
    busy    atomic.Bool

    stopMu sync.Mutex
    stopCh chan struct{}
}

func New(cfg *config.Config, rm *rates.Manager, tb TrayBridge) *App {
    return &App{cfg: cfg, ratesM: rm, trayB: tb}
}

func (a *App) SetTray(tb TrayBridge) { a.trayB = tb }

/* ------------------------- жизненный цикл Wails ------------------------- */

func (a *App) Startup(ctx context.Context) {
    a.ctx = ctx
    a.startTicker()
    // Первичный прогрев сети — не блокируем запуск UI. Кулдаун не включается:
    // это не ручное нажатие.
    go a.BackgroundRefresh(false)
}

func (a *App) BeforeClose(ctx context.Context) bool {
    // Закрытие окна = скрытие (виджет живёт в трее). Выход — только через трей.
    if a.exiting.Load() {
        return false
    }
    runtime.WindowHide(ctx)
    return true
}

/* ----------------------------- показ окна ------------------------------- */

// ShowMode — единая точка показа: размер под режим, позиция у трея, фокус.
func (a *App) ShowMode(mode string) {
    if a.ctx == nil {
        return
    }
    w, h := 380, 245
    if mode == "settings" {
        w, h = 380, 480
    }
    runtime.WindowSetSize(a.ctx, w, h)
    popup.MoveNearTray(WindowTitle) // правый нижний угол, над панелью задач
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

/* -------------------------- обновление курсов --------------------------- */

func (a *App) ManualRefresh() { // из трея и из UI
    go a.BackgroundRefresh(true)
}

// BackgroundRefresh идёт в сеть, публикует событие и синхронизирует трей.
// manual=true → после завершения включается минутный кулдаун кнопки.
func (a *App) BackgroundRefresh(manual bool) {
    if !a.busy.CompareAndSwap(false, true) {
        return // защита от спама кнопкой
    }
    defer a.busy.Store(false)
    if a.trayB != nil {
        a.trayB.SetBusy(true)
    }
    tbl, err := a.ratesM.TryRefresh(context.Background())
    a.publish(tbl, err) // сначала публикуем (обновит [HH:MM] на кнопке)
    if a.trayB != nil {
        a.trayB.SetBusy(false)
        if manual {
            a.trayB.Cooldown(time.Minute) // кулдаун только для ручных нажатий
        }
    }
}

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
}

func (a *App) intervalMin() int {
    a.cfgMu.Lock()
    defer a.cfgMu.Unlock()
    return a.cfg.RefreshMin
}

/* --------------------------- API для фронтенда --------------------------- */

func (a *App) GetInitState() InitPayload {
    a.cfgMu.Lock()
    cfgCopy := *a.cfg
    a.cfgMu.Unlock()
    tbl, _ := a.ratesM.Current()
    return InitPayload{
        Config:      cfgCopy,
        AutoEnabled: autostartEnabled(),
        Update:      ratesUpdate{Table: tbl, Names: a.ratesM.Names()},
    }
}

func (a *App) SetDefaults(base, target string, minutes int) error {
    base, target = strings.ToUpper(strings.TrimSpace(base)), strings.ToUpper(strings.TrimSpace(target))
    if len(base) != 3 || len(target) != 3 ||
        strings.ContainsFunc(base, func(r rune) bool { return r < 'A' || r > 'Z' }) ||
        strings.ContainsFunc(target, func(r rune) bool { return r < 'A' || r > 'Z' }) {
        return errors.New("ожидается ISO-код из трёх букв, например RUB")
    }
    if base == target {
        return errors.New("валюты по умолчанию не должны совпадать")
    }
    if minutes < 5 || minutes > 1440 {
        return errors.New("интервал обновления: от 5 до 1440 минут")
    }

    a.cfgMu.Lock()
    intervalChanged := a.cfg.RefreshMin != minutes
    a.cfg.Base, a.cfg.Target, a.cfg.RefreshMin = base, target, minutes
    saveErr := config.Save(a.cfg)
    a.cfgMu.Unlock()

    if saveErr != nil {
        return saveErr
    }
    if intervalChanged {
        a.stopTicker()
        a.startTicker()
    }
    if a.ctx != nil {
        a.cfgMu.Lock()
        runtime.EventsEmit(a.ctx, "defaults:changed", *a.cfg)
        a.cfgMu.Unlock()
    }
    return nil
}

func (a *App) SetAutoStart(enable bool) error {
    var err error
    if enable {
        err = autostartEnable()
    } else {
        err = autostartDisable()
    }
    if err != nil {
        return err
    }
    a.cfgMu.Lock()
    a.cfg.AutoStart = enable
    saveErr := config.Save(a.cfg)
    a.cfgMu.Unlock()
    if saveErr != nil {
        return saveErr
    }
    if a.trayB != nil {
        a.trayB.SetAutoState(enable) // зафиксировать факт
    }
    if a.ctx != nil {
        runtime.EventsEmit(a.ctx, "autostart:changed", enable)
    }
    return nil
}

func (a *App) HideWindow() {
    if a.ctx != nil {
        runtime.WindowHide(a.ctx)
    }
}