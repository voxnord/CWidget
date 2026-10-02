// Package tray wraps the system tray icon and its context menu.
//
// Layering: this file is the LOGIC layer (see backend.go for the contract
// and the implementation files for the library wrappers). It knows nothing
// about the concrete tray library — swapping the backend is a single-file
// change on the implementation side.
package tray

import (
	"log"
	"runtime/debug"
	"sync"
	"time"

	"github.com/voxnord/cwidget/i18n"
)

// Hooks is everything the tray asks the outside world to do
// (the app injects implementations).
type Hooks struct {
    ShowWidget        func()
    ToggleWidget      func() // native tray toggle: hidden -> show, visible -> hide
    OpenSettings      func()
    Refresh           func()
    ToggleAutoStart   func(enable bool)
    GetAutoStart      func() bool // the actual OS state, polled when the menu is built
    ToggleStayOpen    func(enable bool)
    GetStayOpen       func() bool // the actual config state, polled when the menu is built
    ToggleRememberPos func(enable bool)
    GetRememberPos    func() bool // the actual config state, polled when the menu is built
    SetLanguage       func(code string)
    Quit              func()
}

type Tray struct {
    iconICO []byte // Windows (.ico) — consumed by the backend
    iconPNG []byte // Linux/macOS — consumed by the backend
    hooks   Hooks
    autoOn  bool // local checkbox states (seeded from the sources of truth)
    stayOn  bool
    rposOn  bool

    be        backend    // the implementation layer (created in Run)
    mOpen     menuHandle
    mRefresh  menuHandle
    mOnTop    menuHandle
    mRPos     menuHandle
    mAuto     menuHandle
    mSettings menuHandle
    mQuit     menuHandle
    mLang     menuHandle
    langItems map[string]menuHandle

    mu            sync.RWMutex // guards lastUpdate/cooldown/degraded state
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
func (t *Tray) Run() {
    t.be = newBackend(t.iconICO, t.iconPNG)
    t.be.run(t.onReady)
}

func (t *Tray) Stop() {
    if t.be != nil {
        t.be.quit()
    }
}

// safeGo runs fn on its own goroutine with a panic shield. Two rules make
// the tray survive long idle periods:
//  1. never run application logic ON the tray message-loop thread —
//     a blocking call there wedges the loop and the icon stops responding
//     to ANY click, including Quit;
//  2. never let a panic escape — an uncaught panic in any goroutine kills
//     the whole process and leaves a zombie icon behind.
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
    t.be.setTooltip(i18n.T("tray.tooltip"))

    // Left click — a native TOGGLE (hidden -> show, visible -> hide). See
    // app.ToggleWidget for why this is not an unconditional show.
    t.be.setOnClick(func() {
        log.Printf("[tray] left click -> toggle request")
        if t.hooks.ToggleWidget != nil {
            safeGo("toggle", t.hooks.ToggleWidget)
            return
        }
        if t.hooks.ShowWidget != nil {
            safeGo("show", t.hooks.ShowWidget)
        }
    })
    t.be.setOnRClick(func() {
        log.Printf("[tray] right click -> menu")
    })
    t.be.setOnDClick(func() {
        log.Printf("[tray] double click -> show request")
        if t.hooks.ShowWidget != nil {
            safeGo("show", t.hooks.ShowWidget)
        }
    })

    // Seed all checkboxes from the sources of truth (the OS and the config)
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

    t.mOpen = t.be.addItem(i18n.T("tray.open"), i18n.T("tray.open.tip"))
    t.mRefresh = t.be.addItem(t.refreshTitle(), i18n.T("tray.refresh.tip"))
    t.be.addSeparator()
    t.mOnTop = t.be.addCheckboxItem(i18n.T("tray.ontop"), i18n.T("tray.ontop.tip"), t.stayOn)
    t.mRPos = t.be.addCheckboxItem(i18n.T("tray.rpos"), i18n.T("tray.rpos.tip"), t.rposOn)
    t.mAuto = t.be.addCheckboxItem(i18n.T("tray.autostart"), i18n.T("tray.autostart.tip"), t.autoOn)
    t.be.addSeparator() // separates the toggles group from settings/language
    t.mSettings = t.be.addItem(i18n.T("tray.settings"), i18n.T("tray.settings.tip"))

    // Language submenu: a new language (a new JSON in locales/) appears here
    // automatically.
    t.mLang = t.be.addSubmenuItem(i18n.T("tray.language"), i18n.T("tray.language"))
    t.langItems = make(map[string]menuHandle)
    for _, l := range i18n.Languages() {
        code := l.Code
        item := t.mLang.addSubItem(t.langTitle(code), "")
        item.onClick(func() {
            safeGo("set-language-"+code, func() {
                if t.hooks.SetLanguage != nil {
                    t.hooks.SetLanguage(code)
                }
            })
        })
        t.langItems[code] = item
    }

    t.be.addSeparator()
    t.mQuit = t.be.addItem(i18n.T("tray.quit"), i18n.T("tray.quit.tip"))

    onClick("open", t.mOpen, t.hooks.ShowWidget)
    onClick("refresh", t.mRefresh, t.hooks.Refresh)
    onClick("settings", t.mSettings, t.hooks.OpenSettings)
    onClick("quit", t.mQuit, t.hooks.Quit)

    t.mAuto.onClick(func() {
        safeGo("autostart-toggle", func() {
            t.autoOn = !t.autoOn
            if t.hooks.ToggleAutoStart != nil {
                t.hooks.ToggleAutoStart(t.autoOn)
            }
        })
    })
    t.mOnTop.onClick(func() {
        safeGo("stayopen-toggle", func() {
            t.stayOn = !t.stayOn
            if t.hooks.ToggleStayOpen != nil {
                t.hooks.ToggleStayOpen(t.stayOn)
            }
        })
    })
    t.mRPos.onClick(func() {
        safeGo("rememberpos-toggle", func() {
            t.rposOn = !t.rposOn
            if t.hooks.ToggleRememberPos != nil {
                t.hooks.ToggleRememberPos(t.rposOn)
            }
        })
    })
}

func onClick(what string, m menuHandle, fn func()) {
    if m == nil || fn == nil {
        return
    }
    m.onClick(func() {
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
            t.be.setTooltip(i18n.T("warn.offline.tip"))
        } else {
            t.be.setTooltip(i18n.T("warn.offline.tip.at", last.Local().Format("15:04")))
        }
    case pref != "" && act != "" && pref != act:
        t.be.setTooltip(i18n.T("warn.tray.tip", i18n.T("provider."+pref), i18n.T("provider."+act)))
    default:
        t.be.setTooltip(i18n.T("tray.tooltip"))
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
    t.mOnTop.setChecked(on)
}

// SetRememberPosState pins the remember-position checkbox to the fact.
func (t *Tray) SetRememberPosState(on bool) {
    t.rposOn = on
    t.mRPos.setChecked(on)
}

// Retranslate translates all menu items and the tooltip to the current
// i18n language. Idempotent.
func (t *Tray) Retranslate() {
    if t.mOpen == nil {
        return // the menu is not built yet
    }
    t.applyTooltip()
    t.mOpen.setTitle(i18n.T("tray.open"))
    t.mOpen.setTooltip(i18n.T("tray.open.tip"))

    switch {
    case t.inCooldown():
        // the countdown title is redrawn by the cooldown ticker
    case t.mRefresh.disabled():
        t.mRefresh.setTitle(i18n.T("tray.refresh.busy"))
    default:
        t.mRefresh.setTitle(t.refreshTitle())
    }
    t.mRefresh.setTooltip(i18n.T("tray.refresh.tip"))

    t.mOnTop.setTitle(i18n.T("tray.ontop"))
    t.mOnTop.setTooltip(i18n.T("tray.ontop.tip"))
    t.mRPos.setTitle(i18n.T("tray.rpos"))
    t.mRPos.setTooltip(i18n.T("tray.rpos.tip"))
    t.mAuto.setTitle(i18n.T("tray.autostart"))
    t.mAuto.setTooltip(i18n.T("tray.autostart.tip"))
    t.mSettings.setTitle(i18n.T("tray.settings"))
    t.mSettings.setTooltip(i18n.T("tray.settings.tip"))
    t.mQuit.setTitle(i18n.T("tray.quit"))
    t.mQuit.setTooltip(i18n.T("tray.quit.tip"))

    if t.mLang != nil {
        t.mLang.setTitle(i18n.T("tray.language"))
        for code, item := range t.langItems {
            item.setTitle(t.langTitle(code))
        }
    }
}

// SetUpdatedLabel pins the time of the last successful refresh.
func (t *Tray) SetUpdatedLabel(at time.Time) {
    t.mu.Lock()
    t.lastUpdate = at
    t.mu.Unlock()
    if t.mRefresh != nil && !t.inCooldown() && !t.mRefresh.disabled() {
        t.mRefresh.setTitle(t.refreshTitle())
    }
}

func (t *Tray) SetBusy(busy bool) {
    if t.mRefresh == nil {
        return
    }
    if busy {
        t.mRefresh.setTitle(i18n.T("tray.refresh.busy"))
        t.mRefresh.disable()
        return
    }
    if t.inCooldown() {
        return // the cooldown will re-enable the item on its own
    }
    t.mRefresh.setTitle(t.refreshTitle())
    t.mRefresh.enable()
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
    t.mRefresh.disable()
    go func() {
        ticker := time.NewTicker(500 * time.Millisecond)
        defer ticker.Stop()
        for {
            remain := time.Until(deadline)
            if remain <= 0 {
                break
            }
            t.mRefresh.setTitle(i18n.T("tray.refresh.cooldown", int(remain.Seconds())+1))
            select {
            case <-stop:
                return
            case <-ticker.C:
            }
        }
        t.mu.Lock()
        t.cooldownStop = nil
        t.mu.Unlock()
        t.mRefresh.setTitle(t.refreshTitle())
        t.mRefresh.enable()
    }()
}

func (t *Tray) NotifyFailure(reason string) {
    if t.mRefresh != nil {
        t.mRefresh.setTooltip(i18n.T("tray.fail.tip", reason))
    }
}

func (t *Tray) SetAutoState(on bool) {
    t.autoOn = on
    t.mAuto.setChecked(on)
}