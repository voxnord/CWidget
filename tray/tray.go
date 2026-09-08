package tray

import (
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/energye/systray"
)

// Hooks — всё, что трей просит сделать у внешнего мира (приложение внедряет реализации).
type Hooks struct {
    ShowWidget      func()
    OpenSettings    func()
    Refresh         func()
    ToggleAutoStart func(enable bool)
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
    systray.SetTooltip("Конвертер валют")

    // ЛКМ — открыть окно конвертера, меню НЕ показывать (не вызываем ShowMenu).
    // Работает на Windows/macOS. На Linux AppIndicator-протокол в большинстве DE
    // не передаёт приложению тип клика — там меню откроется в любом случае,
    // функциональность сохраняет первый пункт меню «Открыть».
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

    t.mOpen = systray.AddMenuItem("Открыть", "Показать окно конвертера")
    t.mRefresh = systray.AddMenuItem(t.refreshTitle(), "Запросить свежие курсы из API")
    systray.AddSeparator()
    t.mAuto = systray.AddMenuItemCheckbox("Запускать при старте системы", "Автозапуск вместе с операционной системой", false)
    t.mSettings = systray.AddMenuItem("Настройки", "Валюты по умолчанию и параметры")
    systray.AddSeparator()
    t.mQuit = systray.AddMenuItem("Выход", "Завершить приложение")

    onClick(t.mOpen, t.hooks.ShowWidget)
    onClick(t.mRefresh, t.hooks.Refresh)
    onClick(t.mSettings, t.hooks.OpenSettings)
    onClick(t.mQuit, t.hooks.Quit)

    // Галочка автозапуска: клик переключает локальное состояние,
    // фактическое состояние подтверждает app (SetAutoState).
    // В energye-форке подписка называется Click(fn func()) — колбэк без аргументов.
    t.mAuto.Click(func() {
        go func() {
            t.autoOn = !t.autoOn
            if t.hooks.ToggleAutoStart != nil {
                t.hooks.ToggleAutoStart(t.autoOn)
            }
        }()
    })
}

// onClick подписывает пункт меню на клик. Оборачиваем в goroutine, чтобы
// не блокировать цикл сообщений трея (сеть/реестр внутри колбэка недопустимы).
func onClick(m *systray.MenuItem, fn func()) {
    if m == nil || fn == nil {
        return
    }
    m.Click(func() {
        go fn()
    })
}

/* ---------- мост из приложения (реализация app.TrayBridge) ---------- */

func (t *Tray) refreshTitle() string {
    t.mu.Lock()
    defer t.mu.Unlock()
    if t.lastUpdate.IsZero() {
        return "Обновить курс"
    }
    return fmt.Sprintf("[%s] Обновить курс", t.lastUpdate.Local().Format("15:04"))
}

func (t *Tray) inCooldown() bool {
    t.mu.Lock()
    defer t.mu.Unlock()
    return time.Now().Before(t.cooldownUntil)
}

// SetUpdatedLabel фиксирует время последнего успешного обновления.
func (t *Tray) SetUpdatedLabel(at time.Time) {
    t.mu.Lock()
    t.lastUpdate = at
    t.mu.Unlock()
    if t.mRefresh != nil && !t.inCooldown() {
        t.mRefresh.SetTitle(t.refreshTitle())
    }
}

func (t *Tray) SetBusy(busy bool) {
    if t.mRefresh == nil {
        return
    }
    if busy {
        t.mRefresh.SetTitle("Обновление…")
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
            t.mRefresh.SetTitle(fmt.Sprintf("Обновить курс (через %d c)", int(remain.Seconds())+1))
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
        t.mRefresh.SetTooltip("Последняя попытка не удалась: " + reason)
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