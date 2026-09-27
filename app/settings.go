package app

import (
	"errors"
	"log"
	"os"
	"strings"
	"time"

	"github.com/voxnord/cwidget/config"
	"github.com/voxnord/cwidget/i18n"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

/* --------------------------- frontend-facing API -------------------------
 * Every setter follows the same shape: mutate via saveConfig (one place
 * owns the lock and the persist), then emit side effects (frontend event,
 * tray sync, ticker restart). Side effects NEVER happen when the persist
 * failed — a saved-to-disk lie is worse than a stale UI. */

func (a *App) GetInitState() InitPayload {
    var cfgCopy config.Config
    a.readConfig(func(c *config.Config) { cfgCopy = *c })
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
            Fallback: tbl.IsFallback(pref),
            Snapshots: a.ratesM.Snapshots(),
        },
    }
}

// SetProvider switches the rates source. Generic: works with any key present
// in the manager registry; errors are localization KEYS ("err.xxx[|arg]").
func (a *App) SetProvider(key string) error {
    key = strings.ToLower(strings.TrimSpace(key))
    if !a.ratesM.HasKey(key) {
        return errors.New("err.provider_unknown|" + key)
    }
    if err := a.saveConfig(func(c *config.Config) { c.Provider = key }); err != nil {
        return err
    }
    a.ratesM.SetPreferred(key)
    a.resetServedView()
    // Fetch the new source immediately (with a cooldown — protection
    // against "scroll the list and storm the APIs").
    go a.BackgroundRefresh(true)
    return nil
}

// resetServedView republishes the current state right after a provider
// switch: the frontend gets a rates:update carrying either the NEW source's
// cached snapshot (marked) or nothing — never the previous source's data.
// Without it the UI would keep showing the old source until the new one
// answers (publish() only fires on fetch completion).
func (a *App) resetServedView() {
    if a.ctx == nil {
        return
    }
    tbl, _ := a.ratesM.Current()
    pref := a.ratesM.Preferred()
    runtime.EventsEmit(a.ctx, "rates:update", ratesUpdate{
        Table:     tbl,
        Names:     a.ratesM.Names(),
        Provider:  pref,
        Fallback:  tbl.IsFallback(pref),
        Snapshots: a.ratesM.Snapshots(),
    })
}

func (a *App) StayOpenState() bool { return a.stayOpen() }

// SetStayOpen toggles the keep-window-open (topmost while visible) mode.
func (a *App) SetStayOpen(enable bool) error {
    if err := a.saveConfig(func(c *config.Config) { c.StayOpen = enable }); err != nil {
        return err
    }
    if a.ctx != nil {
        runtime.WindowSetAlwaysOnTop(a.ctx, enable)
        runtime.EventsEmit(a.ctx, "stayopen:changed", enable)
    }
    if a.trayB != nil {
        a.trayB.SetStayOpenState(enable)
    }
    return nil
}

func (a *App) RememberPosState() bool { return a.rememberPos() }

// SetRememberPos toggles the position persistence. Enabling starts the
// watcher lazily; disabling stops it.
func (a *App) SetRememberPos(enable bool) error {
    if err := a.saveConfig(func(c *config.Config) { c.RememberPos = enable }); err != nil {
        return err
    }
    a.setWatcherRunning(enable)
    if a.trayB != nil {
        a.trayB.SetRememberPosState(enable)
    }
    if a.ctx != nil {
        runtime.EventsEmit(a.ctx, "rememberpos:changed", enable)
    }
    return nil
}

// SetFavorites replaces the global favorites list (provider-agnostic ISO
// codes; availability filtering happens at render time).
func (a *App) SetFavorites(codes []string) error {
    favs := config.NormalizeFavorites(codes)
    if err := a.saveConfig(func(c *config.Config) { c.Favorites = favs }); err != nil {
        return err
    }
    if a.ctx != nil {
        runtime.EventsEmit(a.ctx, "favorites:changed", favs)
    }
    return nil
}

func (a *App) SetTheme(theme string) error {
    theme = strings.ToLower(strings.TrimSpace(theme))
    if theme != "dark" && theme != "light" {
        return errors.New("err.theme")
    }
    if err := a.saveConfig(func(c *config.Config) { c.Theme = theme }); err != nil {
        return err
    }
    if a.ctx != nil {
        runtime.EventsEmit(a.ctx, "theme:changed", theme)
    }
    return nil
}

// SetLanguage switches the UI language: persists, swaps the dictionary,
// retranlsates the tray, broadcasts to the frontend.
func (a *App) SetLanguage(lang string) error {
    lang = strings.ToLower(strings.TrimSpace(lang))
    if !i18n.Has(lang) {
        return errors.New("err.language|" + lang)
    }
    if err := a.saveConfig(func(c *config.Config) { c.Language = lang }); err != nil {
        return err
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

// SetDefaults persists the default pair and the refresh interval. The
// interval change restarts the ticker; the pair change is broadcast so the
// main window re-seeds its selects.
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

    var intervalChanged bool
    if err := a.saveConfig(func(c *config.Config) {
        intervalChanged = c.RefreshMin != minutes
        c.Base, c.Target, c.RefreshMin = base, target, minutes
    }); err != nil {
        return err
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
    // The OS is the source of truth: mirror the fact into the config
    // (AutoStart is informational) and pin both UI surfaces.
    if err := a.saveConfig(func(c *config.Config) { c.AutoStart = enable }); err != nil {
        log.Printf("[settings] autostart mirror persist failed: %v", err)
    }
    if a.trayB != nil {
        a.trayB.SetAutoState(enable)
    }
    if a.ctx != nil {
        runtime.EventsEmit(a.ctx, "autostart:changed", enable)
    }
    return nil
}

// Uninstall removes the autostart entry, schedules the deletion of the app
// data folder and the executable, and quits. The frontend confirms first —
// this is irreversible. Quitting happens ONLY after the cleanup has been
// scheduled successfully: on a scheduling failure the app stays alive so
// the user can retry.
func (a *App) Uninstall() error {
    log.Printf("[app] uninstall requested")
    _ = autostartDisable()
    exe, err := os.Executable()
    if err != nil {
        return err
    }
    dataDir, err := config.Dir()
    if err != nil {
        return err
    }
    if err := runSelfUninstall(dataDir, exe); err != nil {
        log.Printf("[app] self-uninstall scheduling failed: %v", err)
        return err // no quit — the user retries or removes manually
    }
    a.Quit() // scheduled — now it is safe to exit
    return nil
}