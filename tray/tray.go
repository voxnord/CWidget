// Package tray wraps the system tray icon and its context menu.
package tray

import (
	"log"
	"runtime"
	"runtime/debug"
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
    GetAutoStart    func() bool // the actual OS state, polled when the menu is built
    ToggleStayOpen  func(enable bool)
    GetStayOpen     func() bool // the actual config state, polled when the menu is built
    ToggleRememberPos func(enable bool)
    GetRememberPos    func() bool // the actual config state, polled when the menu is built    
    SetLanguage     func(code string)
    Quit            func()
}

type Tray struct {
    iconICO []byte // Windows (.ico)
    iconPNG []byte // Linux/macOS
    hooks   Hooks
    autoOn  bool // local checkbox states (seeded from the sources of truth)
    stayOn  bool
    rposOn  bool

    mOpen     *systray.MenuItem
    mRefresh  *systray.MenuItem
    mAuto     *systray.MenuItem
    mOnTop    *systray.MenuItem
    mRPos     *systray.MenuItem
    mSettings *systray.MenuItem
    mQuit     *systray.MenuItem
    mLang     *systray.MenuItem
    langItems map[string]*systray.MenuItem

    
    mu            sync.RWMutex  // RWMutex: applyTooltip reads under RLock, writers use Lock
    lastUpdate    time.Time     // moment of the last successful refresh
    cooldownUntil time.Time     // until when the refresh item is blocked
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

// safeGo runs fn on its own goroutine with a panic shield. Two rules make
// the tray survive long idle periods:
//   1. never run application logic ON the tray message-loop thread —
//      a blocking call there wedges the loop and the icon stops responding
//      to ANY click, including Quit;
//   2. never let a panic escape — an uncaught panic in any goroutine kills
//      the whole process and leaves a zombie icon behind.
func safeGo(what string, fn func()) {
    go func() {
        defer func() {
            if r := recover(); r != nil {
                log.Printf("[panic] tray/%s: %v\n%s", what, r, debug.Stack())
            }
        }()
        fn()
    }()
}

func (t *Tray) onReady() {
    if runtime.GOOS == "windows" {
        systray.SetIcon(t.iconICO)
    } else {
        systray.SetIcon(t.iconPNG)
    }
    systray.SetTooltip(i18n.T("tray.tooltip"))

    // Left click — open the converter window, do NOT show the menu.
    // Right click — context menu. Double click — the window as well.
    // Click lines are logged: the first checkpoint when diagnosing a "dead" tray.
    systray.SetOnClick(func(_ systray.IMenu) {
        log.Printf("[tray] left click -> show request")
        if t.hooks.ShowWidget != nil {
            safeGo("show", t.hooks.ShowWidget)
        }
    })
    systray.SetOnRClick(func(menu systray.IMenu) {
        log.Printf("[tray] right click -> menu")
        menu.ShowMenu() // MUST stay on the tray thread (modal TrackPopupMenu)
    })
    systray.SetOnDClick(func(_ systray.IMenu) {
        log.Printf("[tray] double click -> show request")
        if t.hooks.ShowWidget != nil {
            safeGo("show", t.hooks.ShowWidget)
        }
    })

    // Seed both checkboxes from the sources of truth (the OS and the config)
    // so the menu is in sync from the very first launch.
    t.autoOn = false
    if t.hooks.GetAutoStart != nil {
        t.autoOn = t.hooks.GetAutoStart()
    }
    t.stayOn = false
    if t.hooks.GetStayOpen != nil {
        t.stayOn = t.hooks.GetStayOpen()
    }
    t.rposOn = false
    if t.hooks.GetRememberPos != nil {
        t.rposOn = t.hooks.GetRememberPos()
    }

    t.mOpen = systray.AddMenuItem(i18n.T("tray.open"), i18n.T("tray.open.tip"))
    t.mRefresh = systray.AddMenuItem(t.refreshTitle(), i18n.T("tray.refresh.tip"))
    systray.AddSeparator()
    t.mOnTop = systray.AddMenuItemCheckbox(i18n.T("tray.ontop"), i18n.T("tray.ontop.tip"), t.stayOn)
    t.mRPos = systray.AddMenuItemCheckbox(i18n.T("tray.rpos"), i18n.T("tray.rpos.tip"), t.rposOn)
    t.mAuto = systray.AddMenuItemCheckbox(i18n.T("tray.autostart"), i18n.T("tray.autostart.tip"), t.autoOn)
    t.mSettings = systray.AddMenuItem(i18n.T("tray.settings"), i18n.T("tray.settings.tip"))

    // Language submenu: a new language (a new JSON in locales/) appears here
    // automatically.
    t.mLang = systray.AddMenuItem(i18n.T("tray.language"), i18n.T("tray.language"))
    t.langItems = make(map[string]*systray.MenuItem)
    for _, l := range i18n.Languages() {
        code := l.Code
        item := t.mLang.AddSubMenuItem(t.langTitle(code), "")
        item.Click(func() {
            safeGo("set-language-"+code, func() {
                if t.hooks.SetLanguage != nil {
                    t.hooks.SetLanguage(code)
                }
            })
        })
        t.langItems[code] = item
    }

    systray.AddSeparator()
    t.mQuit = systray.AddMenuItem(i18n.T("tray.quit"), i18n.T("tray.quit.tip"))

    onClick("open", t.mOpen, t.hooks.ShowWidget)
    onClick("refresh", t.mRefresh, t.hooks.Refresh)
    onClick("settings", t.mSettings, t.hooks.OpenSettings)
    onClick("quit", t.mQuit, t.hooks.Quit)

    t.mAuto.Click(func() {
        safeGo("autostart-toggle", func() {
            t.autoOn = !t.autoOn
            if t.hooks.ToggleAutoStart != nil {
                t.hooks.ToggleAutoStart(t.autoOn)
            }
        })
    })
    t.mOnTop.Click(func() {
        safeGo("stayopen-toggle", func() {
            t.stayOn = !t.stayOn
            if t.hooks.ToggleStayOpen != nil {
                t.hooks.ToggleStayOpen(t.stayOn)
            }
        })
    })
    t.mRPos.Click(func() {
        safeGo("rememberpos-toggle", func() {
            t.rposOn = !t.rposOn
            if t.hooks.ToggleRememberPos != nil {
                t.hooks.ToggleRememberPos(t.rposOn)
            }
        })
    })

}

func onClick(what string, m *systray.MenuItem, fn func()) {
    if m == nil || fn == nil {
        return
    }
    m.Click(func() {
        safeGo(what, fn)
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
// "offline" > degraded (the fallback answered) > normal.
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

// SetStayOpenState pins the on-top checkbox to the fact (called by the app
// after the setting is saved — from the settings toggle as well).
func (t *Tray) SetStayOpenState(on bool) {
    t.stayOn = on
    if t.mOnTop != nil {
        if on {
            t.mOnTop.Check()
        } else {
            t.mOnTop.Uncheck()
        }
    }
}

// SetRememberPosState pins the remember-position checkbox to the fact.
func (t *Tray) SetRememberPosState(on bool) {
    t.rposOn = on
    if t.mRPos != nil {
        if on {
            t.mRPos.Check()
        } else {
            t.mRPos.Uncheck()
        }
    }
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
        // the countdown title is redrawn by the cooldown ticker
    case t.mRefresh.Disabled():
        t.mRefresh.SetTitle(i18n.T("tray.refresh.busy"))
    default:
        t.mRefresh.SetTitle(t.refreshTitle())
    }
    t.mRefresh.SetTooltip(i18n.T("tray.refresh.tip"))

    t.mOnTop.SetTitle(i18n.T("tray.ontop"))
    t.mOnTop.SetTooltip(i18n.T("tray.ontop.tip"))
    t.mRPos.SetTitle(i18n.T("tray.rpos"))
    t.mRPos.SetTooltip(i18n.T("tray.rpos.tip"))
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