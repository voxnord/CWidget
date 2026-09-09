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
    Config      config.Config        `json:"config"`
    AutoEnabled bool                 `json:"autostart_enabled"`
    CooldownSec int                  `json:"cooldown_sec"` // остаток кулдауна обновления
    Provider    string               `json:"provider"`     // выбранный провайдер
    Providers   []rates.ProviderInfo `json:"providers"`    // зарегистрированные провайдеры
    Update      ratesUpdate          `json:"update"`
}

type App struct {
    ctx    context.Context
    cfg    *config.Config
    cfgMu  sync.Mutex
    ratesM *rates.Manager
    trayB  TrayBridge

    exiting       atomic.Bool
    busy          atomic.Bool
    cooldownUntil atomic.Int64 // unix-время окончания кулдауна

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
    go a.BackgroundRefresh(false) // прогрев без кулдауна
}

func (a *App) BeforeClose(ctx context.Context) bool {
    if a.exiting.Load() {
        return false
    }
    runtime.WindowHide(ctx)
    return true
}

/* ----------------------------- показ окна ------------------------------- */

// ShowMode — единая точка показа: размер под режим, позиция у трея,
// скругление под фактический размер, фокус.
func (a *App) ShowMode(mode string) {
    if a.ctx == nil {
        return
    }
    w, h := 380, 260
    if mode == "settings" {
        w, h = 380, 500
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

/* -------------------------- обновление курсов --------------------------- */

func (a *App) ManualRefresh() {
    go a.BackgroundRefresh(true)
}

func (a *App) BackgroundRefresh(manual bool) {
    if !a.busy.CompareAndSwap(false, true) {
        return
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
                a.BackgroundRefresh(false)
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

    cooldownSec := 0
    if rem := time.Until(time.Unix(a.cooldownUntil.Load(), 0)); rem > 0 {
        cooldownSec = int(rem.Seconds()) + 1
    }

    return InitPayload{
        Config:      cfgCopy,
        AutoEnabled: autostartEnabled(),
        CooldownSec: cooldownSec,
        Provider:    a.ratesM.Preferred(),
        Providers:   a.ratesM.Providers(),
        Update:      ratesUpdate{Table: tbl, Names: a.ratesM.Names()},
    }
}

func (a *App) SetProvider(key string) error {
    key = strings.ToLower(strings.TrimSpace(key))
    if !a.ratesM.HasKey(key) {
        return errors.New("неизвестный провайдер: " + key)
    }
    a.cfgMu.Lock()
    a.cfg.Provider = key
    saveErr := config.Save(a.cfg)
    a.cfgMu.Unlock()
    if saveErr != nil {
        return saveErr
    }
    a.ratesM.SetPreferred(key)
    go a.BackgroundRefresh(true)
    return nil
}

// SetTheme — смена темы ("dark" | "light"). Применяется немедленно.
func (a *App) SetTheme(theme string) error {
    theme = strings.ToLower(strings.TrimSpace(theme))
    if theme != "dark" && theme != "light" {
        return errors.New("тема: dark или light")
    }
    a.cfgMu.Lock()
    a.cfg.Theme = theme
    saveErr := config.Save(a.cfg)
    a.cfgMu.Unlock()
    if saveErr != nil {
        return saveErr
    }
    if a.ctx != nil {
        runtime.EventsEmit(a.ctx, "theme:changed", theme)
    }
    return nil
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
        a.trayB.SetAutoState(enable)
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