package app

import (
	"errors"
	"strings"
	"time"

	"github.com/voxnord/cwidget/config"
	"github.com/voxnord/cwidget/i18n"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

/* --------------------------- frontend-facing API ------------------------- */

func (a *App) GetInitState() InitPayload {
    a.cfgMu.Lock()
    cfgCopy := *a.cfg
    a.cfgMu.Unlock()
    tbl, _ := a.ratesM.Current()
    pref := a.ratesM.Preferred()

    cooldownSec := 0
    if rem := time.Until(time.Unix(a.cooldownUntil.Load(), 0)); rem > 0 {
        cooldownSec = int(rem.Seconds()) + 1
    }

    return InitPayload{
        Version:          Version,
        Config:           cfgCopy,
        AutoEnabled:      autostartEnabled(),
        CooldownSec:      cooldownSec,
        Provider:         pref,
        Providers:        a.ratesM.Providers(),
        ProviderStatuses: a.ratesM.Statuses(),
        Stale:            a.stale.Load(),
        Lang:             i18n.Current(),
        Languages:        i18n.Languages(),
        I18n:             i18n.Dict(),
        Update: ratesUpdate{
            Table:    tbl,
            Names:    a.ratesM.Names(),
            Provider: pref,
            Fallback: isFallback(tbl, pref),
        },
    }
}

// SetProvider switches the rates source. It is generic: it works with any key
// present in the manager registry and requires zero per-provider code.
// Errors are returned as localization KEYS ("err.xxx[|arg]"), the frontend
// translates them.
func (a *App) SetProvider(key string) error {
    key = strings.ToLower(strings.TrimSpace(key))
    if !a.ratesM.HasKey(key) {
        return errors.New("err.provider_unknown|" + key)
    }
    a.cfgMu.Lock()
    a.cfg.Provider = key
    saveErr := config.Save(a.cfg)
    a.cfgMu.Unlock()
    if saveErr != nil {
        return saveErr
    }
    a.ratesM.SetPreferred(key)
    // Fetch the new source immediately (with a cooldown — protection against
    // "scroll the list and storm the APIs").
    go a.BackgroundRefresh(true)
    return nil
}

// SetTheme switches the theme ("dark" | "light"). Applied immediately: the
// frontend receives theme:changed and toggles the class on <body>.
func (a *App) SetTheme(theme string) error {
    theme = strings.ToLower(strings.TrimSpace(theme))
    if theme != "dark" && theme != "light" {
        return errors.New("err.theme")
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

// SetLanguage switches the UI language. One call does everything: persists
// the choice, swaps the dictionary, retranlsates the tray menu (Retranslate)
// and broadcasts the dictionary to the frontend. Works from both the settings
// and the tray language submenu.
func (a *App) SetLanguage(lang string) error {
    lang = strings.ToLower(strings.TrimSpace(lang))
    if !i18n.Has(lang) {
        return errors.New("err.language|" + lang)
    }
    a.cfgMu.Lock()
    a.cfg.Language = lang
    saveErr := config.Save(a.cfg)
    a.cfgMu.Unlock()
    if saveErr != nil {
        return saveErr
    }
    if err := i18n.SetLanguage(lang); err != nil {
        return err
    }
    if a.trayB != nil {
        a.trayB.Retranslate()
    }
    if a.ctx != nil {
        runtime.EventsEmit(a.ctx, "i18n:changed", map[string]any{
            "lang": lang,
            "dict": i18n.Dict(),
        })
    }
    return nil
}

func (a *App) SetDefaults(base, target string, minutes int) error {
    base, target = strings.ToUpper(strings.TrimSpace(base)), strings.ToUpper(strings.TrimSpace(target))
    if len(base) != 3 || len(target) != 3 ||
        strings.ContainsFunc(base, func(r rune) bool { return r < 'A' || r > 'Z' }) ||
        strings.ContainsFunc(target, func(r rune) bool { return r < 'A' || r > 'Z' }) {
        return errors.New("err.iso")
    }
    if base == target {
        return errors.New("err.same_pair")
    }
    if minutes < 5 || minutes > 1440 {
        return errors.New("err.interval")
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
        a.startTicker() // first tick — a full new interval from now
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
        a.trayB.SetAutoState(enable) // pin the actual state
    }
    if a.ctx != nil {
        runtime.EventsEmit(a.ctx, "autostart:changed", enable)
    }
    return nil
}