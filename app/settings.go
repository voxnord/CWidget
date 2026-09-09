package app

import (
	"errors"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/you/currency-widget/config"
)

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

// SetProvider — смена источника курсов. Универсальна: работает с любым ключом,
// присутствующим в реестре менеджера; на стороне провайдеров не нужно ни строчки.
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
    // Немедленно подтянуть данные нового источника (с кулдауном — защита
    // от «полистать список и устроить шторм запросов»).
    go a.BackgroundRefresh(true)
    return nil
}

// SetTheme — смена темы ("dark" | "light"). Применяется немедленно:
// фронтенд получает событие theme:changed и переключает класс на body.
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
        a.startTicker() // первый тик — через новый интервал от текущего момента
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