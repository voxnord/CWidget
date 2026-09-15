// Package tray wraps the system tray icon and its context menu.
package tray

import (
	"runtime"
	"sync"
	"time"

	"github.com/energye/systray"
	"github.com/voxnord/cwidget/i18n"
)

// Hooks is everything the tray asks the outside world to do
// (the app injects implementations).
type Hooks struct {
    ShowWidget      func()
    OpenSettings    func()
    Refresh         func()
    ToggleAutoStart func(enable bool)
    SetLanguage     func(code string)
    Quit            func()
}

type Tray struct {
    iconICO []byte // Windows (.ico)
    iconPNG []byte // Linux/macOS
    hooks   Hooks
    autoOn  bool // local checkbox state (for click toggling)

    mOpen     *systray.MenuItem
    mRefresh  *systray.MenuItem
    mAuto     *systray.MenuItem
    mSettings *systray.MenuItem
    mQuit     *systray.MenuItem
    mLang     *systray.MenuItem
    langItems map[string]*systray.MenuItem

    mu            sync.RWMutex // RWMutex: applyTooltip reads under RLock, writers use Lock
    lastUpdate    time.Time    // moment of the last successful refresh
    cooldownUntil time.Time    // until when the refresh item is blocked
    cooldownStop  chan struct{} // cancels the current countdown

    degPref, degAct string // degraded: keys of the selected and the actual provider ("" = none)
    stale           bool   // "offline": nobody answered on the last attempt
}

func New(iconICO, iconPNG []byte, hooks Hooks) *Tray {
    return &Tray{iconICO: iconICO, iconPNG: iconPNG, hooks: hooks}
}

// Run blocks the calling goroutine until Stop().
func (t *Tray) Run() { systray.Run(t.onReady, nil) }

func (t *Tray) Stop() { systray.Quit() }

func (t *Tray) onReady() {
    if runtime.GOOS == "windows" {
        systray.SetIcon(t.iconICO)
    } else {
        systray.SetIcon(t.iconPNG)
    }
    systray.SetTooltip(i18n.T("tray.tooltip"))

    // Left click — open the converter window, do NOT show the menu
    // (ShowMenu is not called). Works on Windows/macOS. On Linux the
    // AppIndicator protocol does not report the click type in most DEs —
    // the menu will open there regardless; the "Open" menu item keeps the
    // functionality.
    systray.SetOnClick(func(_ systray.IMenu) {
        if t.hooks.ShowWidget != nil {
            t.hooks.ShowWidget()
        }
    })
    // Right click — context menu.
    systray.SetOnRClick(func(menu systray.IMenu) {
        menu.ShowMenu()
    })
    // Double click — the window as well.
    systray.SetOnDClick(func(_ systray.IMenu) {
        if t.hooks.ShowWidget != nil {
            t.hooks.ShowWidget()
        }
    })

    t.mOpen = systray.AddMenuItem(i18n.T("tray.open"), i18n.T("tray.open.tip"))
    t.mRefresh = systray.AddMenuItem(t.refreshTitle(), i18n.T("tray.refresh.tip"))
    systray.AddSeparator()
    t.mAuto = systray.AddMenuItemCheckbox(i18n.T("tray.autostart"), i18n.T("tray.autostart.tip"), false)
    t.mSettings = systray.AddMenuItem(i18n.T("tray.settings"), i18n.T("tray.settings.tip"))

    // Language submenu: the parent item is a regular menu item, language
    // entries are added via the AddSubMenuItem method of that item. A new
    // language (a new JSON in locales/) appears here automatically.
    t.mLang = systray.AddMenuItem(i18n.T("tray.language"), i18n.T("tray.language"))
    t.langItems = make(map[string]*systray.MenuItem)
    for _, l := range i18n.Languages() {
        code := l.Code
        item := t.mLang.AddSubMenuItem(t.langTitle(code), "")
        item.Click(func() {
            go func() {
                if t.hooks.SetLanguage != nil {
                    t.hooks.SetLanguage(code)
                }
            }()
        })
        t.langItems[code] = item
    }

    systray.AddSeparator()
    t.mQuit = systray.AddMenuItem(i18n.T("tray.quit"), i18n.T("tray.quit.tip"))

    onClick(t.mOpen, t.hooks.ShowWidget)
    onClick(t.mRefresh, t.hooks.Refresh)
    onClick(t.mSettings, t.hooks.OpenSettings)
    onClick(t.mQuit, t.hooks.Quit)

    t.mAuto.Click(func() {
        go func() {
            t.autoOn = !t.autoOn
            if t.hooks.ToggleAutoStart != nil {
                t.hooks.ToggleAutoStart(t.autoOn)
            }
        }()
    })
}

func onClick(m *systray.MenuItem, fn func()) {
    if m == nil || fn == nil {
        return
    }
    m.Click(func() {
        go fn()
    })
}

/* ---------- bridge from the app (implements app.TrayBridge) ---------- */

func (t *Tray) langTitle(code string) string {
    name := code
    for _, l := range i18n.Languages() {
        if l.Code == code {
            name = l.Name
            break
        }
    }
    if i18n.Current() == code {
        return "✓ " + name
    }
    return name
}

func (t *Tray) refreshTitle() string {
    t.mu.Lock()
    last := t.lastUpdate
    t.mu.Unlock()
    if last.IsZero() {
        return i18n.T("tray.refresh")
    }
    return i18n.T("tray.refresh.time", last.Local().Format("15:04"))
}

func (t *Tray) inCooldown() bool {
    t.mu.Lock()
    defer t.mu.Unlock()
    return time.Now().Before(t.cooldownUntil)
}

// applyTooltip builds the tray icon tooltip. Priority mirrors severity:
// "offline" (nobody answered) > degraded (the fallback answered) > normal.
// Provider names come from the dictionary by keys — Retranslate() rebuilds
// the tooltip on a language switch automatically.
func (t *Tray) applyTooltip() {
    t.mu.RLock()
    pref, act, stale, last := t.degPref, t.degAct, t.stale, t.lastUpdate
    t.mu.RUnlock()
    switch {
    case stale:
        if last.IsZero() {
            systray.SetTooltip(i18n.T("warn.offline.tip"))
        } else {
            systray.SetTooltip(i18n.T("warn.offline.tip.at", last.Local().Format("15:04")))
        }
    case pref != "" && act != "" && pref != act:
        systray.SetTooltip(i18n.T("warn.tray.tip", i18n.T("provider."+pref), i18n.T("provider."+act)))
    default:
        systray.SetTooltip(i18n.T("tray.tooltip"))
    }
}

// SetDegraded is told by the app: the selected provider is unavailable and
// data came from the fallback (keys are passed for localization).
func (t *Tray) SetDegraded(degraded bool, preferredKey, actualKey string) {
    t.mu.Lock()
    if degraded && preferredKey != "" && actualKey != "" && preferredKey != actualKey {
        t.degPref, t.degAct = preferredKey, actualKey
    } else {
        t.degPref, t.degAct = "", ""
    }
    t.mu.Unlock()
    t.applyTooltip()
}

// SetStale marks "offline": the last refresh attempt was answered by nobody.
func (t *Tray) SetStale(stale bool) {
    t.mu.Lock()
    t.stale = stale
    t.mu.Unlock()
    t.applyTooltip()
}

// Retranslate translates all menu items and the tooltip to the current
// i18n language. Idempotent.
func (t *Tray) Retranslate() {
    if t.mOpen == nil {
        return // the menu is not built yet
    }
    t.applyTooltip()
    t.mOpen.SetTitle(i18n.T("tray.open"))
    t.mOpen.SetTooltip(i18n.T("tray.open.tip"))

    switch {
    case t.inCooldown():
        // the countdown title is redrawn by the cooldown ticker — it calls
        // i18n.T at each tick
    case t.mRefresh.Disabled():
        t.mRefresh.SetTitle(i18n.T("tray.refresh.busy"))
    default:
        t.mRefresh.SetTitle(t.refreshTitle())
    }
    t.mRefresh.SetTooltip(i18n.T("tray.refresh.tip"))

    t.mAuto.SetTitle(i18n.T("tray.autostart"))
    t.mAuto.SetTooltip(i18n.T("tray.autostart.tip"))
    t.mSettings.SetTitle(i18n.T("tray.settings"))
    t.mSettings.SetTooltip(i18n.T("tray.settings.tip"))
    t.mQuit.SetTitle(i18n.T("tray.quit"))
    t.mQuit.SetTooltip(i18n.T("tray.quit.tip"))

    if t.mLang != nil {
        t.mLang.SetTitle(i18n.T("tray.language"))
        for code, item := range t.langItems {
            item.SetTitle(t.langTitle(code))
        }
    }
}

// SetUpdatedLabel pins the time of the last successful refresh.
func (t *Tray) SetUpdatedLabel(at time.Time) {
    t.mu.Lock()
    t.lastUpdate = at
    t.mu.Unlock()
    if t.mRefresh != nil && !t.inCooldown() && !t.mRefresh.Disabled() {
        t.mRefresh.SetTitle(t.refreshTitle())
    }
}

func (t *Tray) SetBusy(busy bool) {
    if t.mRefresh == nil {
        return
    }
    if busy {
        t.mRefresh.SetTitle(i18n.T("tray.refresh.busy"))
        t.mRefresh.Disable()
        return
    }
    if t.inCooldown() {
        return // the cooldown will re-enable the item on its own
    }
    t.mRefresh.SetTitle(t.refreshTitle())
    t.mRefresh.Enable()
}

// Cooldown blocks the refresh item for d (grey, unclickable) showing a live
// countdown. A repeated call restarts the countdown.
func (t *Tray) Cooldown(d time.Duration) {
    t.mu.Lock()
    if t.cooldownStop != nil {
        close(t.cooldownStop)
    }
    stop := make(chan struct{})
    t.cooldownStop = stop
    deadline := time.Now().Add(d)
    t.cooldownUntil = deadline
    t.mu.Unlock()

    if t.mRefresh == nil {
        return
    }
    t.mRefresh.Disable()
    go func() {
        ticker := time.NewTicker(500 * time.Millisecond)
        defer ticker.Stop()
        for {
            remain := time.Until(deadline)
            if remain <= 0 {
                break
            }
            t.mRefresh.SetTitle(i18n.T("tray.refresh.cooldown", int(remain.Seconds())+1))
            select {
            case <-stop:
                return
            case <-ticker.C:
            }
        }
        t.mu.Lock()
        t.cooldownStop = nil
        t.mu.Unlock()
        t.mRefresh.SetTitle(t.refreshTitle())
        t.mRefresh.Enable()
    }()
}

func (t *Tray) NotifyFailure(reason string) {
    if t.mRefresh != nil {
        t.mRefresh.SetTooltip(i18n.T("tray.fail.tip", reason))
    }
}

func (t *Tray) SetAutoState(on bool) {
    t.autoOn = on
    if t.mAuto != nil {
        if on {
            t.mAuto.Check()
        } else {
            t.mAuto.Uncheck()
        }
    }
}