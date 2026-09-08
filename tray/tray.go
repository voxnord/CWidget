package tray

import (
	"fmt"
	"runtime"
	"sync"
	"time"

	"fyne.io/systray"
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
    go func() {
        for range t.mAuto.ClickedCh {
            t.autoOn = !t.autoOn
            if t.hooks.ToggleAutoStart != nil {
                t.hooks.ToggleAutoStart(t.autoOn) // приложение подтвердит/исправит фактом
            }
        }
    }()
}

func onClick(m *systray.MenuItem, fn func()) {
    if fn == nil || m == nil {
        return
    }
    go func() {
        for range m.ClickedCh {
            fn()
        }
    }()
}

/* ---------- мост из приложения (реализация app.TrayBridge) ---------- */

// refreshTitle — заголовок кнопки обновления с временем последнего успеха.
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
        return // кулдаун сам включит кнопку по истечении
    }
    t.mRefresh.SetTitle(t.refreshTitle())
    t.mRefresh.Enable()
}

// Cooldown блокирует пункт обновления на время d (серый, некликабельный),
// показывая в заголовке обратный отсчёт. Повторный вызов перезапускает отсчёт.
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