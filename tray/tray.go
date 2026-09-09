package tray

import (
	"runtime"
	"sync"
	"time"

	"github.com/energye/systray"
	"github.com/voxnord/cwidget/i18n"
)

// Hooks — всё, что трей просит сделать у внешнего мира (приложение внедряет реализации).
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
    autoOn  bool // локальное состояние галочки (для чередования кликов)

    mOpen     *systray.MenuItem
    mRefresh  *systray.MenuItem
    mAuto     *systray.MenuItem
    mSettings *systray.MenuItem
    mQuit     *systray.MenuItem
    mLang     *systray.MenuItem
    langItems map[string]*systray.MenuItem

    mu            sync.Mutex
    lastUpdate    time.Time     // момент последнего успешного обновления
    cooldownUntil time.Time     // до какого времени заблокирована кнопка
    cooldownStop  chan struct{} // отмена текущего отсчёта
}

func New(iconICO, iconPNG []byte, hooks Hooks) *Tray {
    return &Tray{iconICO: iconICO, iconPNG: iconPNG, hooks: hooks}
}

// Run блокирует вызывающую горутину до Stop().
func (t *Tray) Run() { systray.Run(t.onReady, nil) }

func (t *Tray) Stop() { systray.Quit() }

func (t *Tray) onReady() {
    if runtime.GOOS == "windows" {
        systray.SetIcon(t.iconICO)
    } else {
        systray.SetIcon(t.iconPNG)
    }
    systray.SetTooltip(i18n.T("tray.tooltip"))

    // ЛКМ — открыть окно конвертера, меню НЕ показывать (не вызываем ShowMenu).
    // Работает на Windows/macOS. На Linux AppIndicator-протокол в большинстве DE
    // не передаёт приложению тип клика — там меню откроется в любом случае.
    systray.SetOnClick(func(_ systray.IMenu) {
        if t.hooks.ShowWidget != nil {
            t.hooks.ShowWidget()
        }
    })
    // ПКМ — контекстное меню.
    systray.SetOnRClick(func(menu systray.IMenu) {
        menu.ShowMenu()
    })
    // Двойной клик — тоже окно.
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

    // Подменю языков: пункты строятся из реестра словарей — новый язык
    // (новый JSON в locales/) появляется здесь автоматически.
    t.mLang = systray.AddMenuItem(i18n.T("tray.language"), i18n.T("tray.language"))
    t.langItems = make(map[string]*systray.MenuItem)
    for _, l := range i18n.Languages() {
        code := l.Code
        item := t.mLang.AddSubMenuItem(t.langTitle(code), "")
        item.Click(func() {
            go func() {
                if t.hooks.SetLanguage != nil {
                    // app сохранит выбор, переключит словарь, переведёт трей
                    // (через Retranslate) и разошлёт событие фронтенду.
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

/* ---------- мост из приложения (реализация app.TrayBridge) ---------- */

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

// Retranslate — перевод всех пунктов меню на текущий язык i18n.
// Вызывается приложением после SetLanguage. Идемпотентен.
func (t *Tray) Retranslate() {
    if t.mOpen == nil {
        return // меню ещё не построено
    }
    systray.SetTooltip(i18n.T("tray.tooltip"))
    t.mOpen.SetTitle(i18n.T("tray.open"))
    t.mOpen.SetTooltip(i18n.T("tray.open.tip"))

    switch {
    case t.inCooldown():
        // заголовок с отсчётом перерисует тикер кулдауна — он берёт i18n.T в момент тика
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

// SetUpdatedLabel фиксирует время последнего успешного обновления.
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
        return // кулдаун сам вернёт кнопку по истечении
    }
    t.mRefresh.SetTitle(t.refreshTitle())
    t.mRefresh.Enable()
}

// Cooldown блокирует пункт обновления на время d (серый, некликабельный),
// показывая обратный отсчёт. Повторный вызов перезапускает отсчёт.
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