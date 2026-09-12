package app

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/voxnord/cwidget/config"
	"github.com/voxnord/cwidget/i18n"
	"github.com/voxnord/cwidget/rates"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Version — версия приложения ("1.2.3").
// Прописывается скриптом сборки через ldflags:
//   -ldflags "-X <module>/app.Version=1.2.3"
// Значение "dev" означает запуск из исходников (wails dev / go run) без сборки.
var Version = "dev"

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
    SetDegraded(degraded bool, preferredKey, actualKey string) // резерв вместо выбранного
    SetStale(stale bool)                                       // «нет связи»: не ответил никто
    Retranslate()                                              // перевод меню и тултипа на текущий язык
}

// ratesUpdate — payload события rates:update.
// Provider — ключ ВЫБРАННОГО провайдера; Table.SourceKey — кто РЕАЛЬНО ответил.
// Fallback=true означает: выбранный недоступен, показаны данные резерва.
type ratesUpdate struct {
    Table    *rates.Table      `json:"table"`
    Names    map[string]string `json:"names"`
    Provider string            `json:"provider"`
    Fallback bool              `json:"fallback"`
}

type InitPayload struct {
    Version          string               `json:"version"`   // версия сборки (для «О программе»)
    Config           config.Config        `json:"config"`
    AutoEnabled      bool                 `json:"autostart_enabled"`
    CooldownSec      int                  `json:"cooldown_sec"` // остаток кулдауна обновления
    Provider         string               `json:"provider"`     // выбранный провайдер
    Providers        []rates.ProviderInfo `json:"providers"`    // зарегистрированные провайдеры
    ProviderStatuses []rates.ProviderStatus `json:"provider_statuses"` // диагностика провайдеров
    Stale            bool                 `json:"stale"`        // «нет связи» (не ответил никто)
    Lang             string               `json:"lang"`         // текущий язык
    Languages        []i18n.Language      `json:"languages"`    // доступные языки
    I18n             map[string]string    `json:"i18n"`         // словарь текущего языка для UI
    Update           ratesUpdate          `json:"update"`
}

// App — центральное состояние приложения. Файлы пакета делят методы по
// обязанностям: window.go — показ окна, refresh.go — обновление курсов,
// settings.go — API для фронтенда, ticker.go — фоновый таймер.
type App struct {
    ctx    context.Context
    cfg    *config.Config
    cfgMu  sync.Mutex
    ratesM *rates.Manager
    trayB  TrayBridge

    exiting       atomic.Bool
    busy          atomic.Bool
    cooldownUntil atomic.Int64 // unix-время окончания кулдауна
    stale         atomic.Bool  // «нет связи»: последняя попытка обновления не ответила
    failures      atomic.Int64 // подряд идущих полных отказов (для backoff)

    retryMu    sync.Mutex
    retryTimer *time.Timer // запланированный backoff-ретрай (максимум один)

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
    // Закрытие окна = скрытие (виджет живёт в трее). Выход — только через трей.
    if a.exiting.Load() {
        return false
    }
    runtime.WindowHide(ctx)
    return true
}