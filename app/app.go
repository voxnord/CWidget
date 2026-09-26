// Package app wires the Wails lifecycle, the tray, the rates manager and the
// frontend bindings together. Files: app.go — state and lifecycle,
// window.go — presentation, refresh.go — rates updates, settings.go — the
// frontend API, ticker.go — scheduling, position.go — position persistence,
// config_helpers.go — private config access, autostart_bridge.go — the OS
// autostart wrappers, uninstall_*.go — self-removal, log.go — the panic
// shield and the log writer.
package app

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/voxnord/cwidget/config"
	"github.com/voxnord/cwidget/i18n"
	"github.com/voxnord/cwidget/rates"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Version is the application version ("1.2.3"), injected by the build
// script via ldflags. "dev" = started from sources (wails dev / go run).
var Version = "dev"

// WindowTitle is the unique window title, set from main.go BEFORE wails.Run:
// the popup package locates the window by title.
var WindowTitle string

// TrayBridge is what the app requires from the tray (implemented by tray.Tray).
type TrayBridge interface {
    SetUpdatedLabel(at time.Time)
    SetAutoState(on bool)
    SetStayOpenState(on bool)
    SetRememberPosState(on bool)
    SetBusy(busy bool)
    Cooldown(d time.Duration)
    NotifyFailure(reason string)
    SetDegraded(degraded bool, preferredKey, actualKey string)
    SetStale(stale bool)
    Retranslate()
}

type ratesUpdate struct {
    Table    *rates.Table      `json:"table"`
    Names    map[string]string `json:"names"`
    Provider string            `json:"provider"`
    Fallback bool              `json:"fallback"`
}

type InitPayload struct {
    Version          string                 `json:"version"`
    Config           config.Config          `json:"config"`
    AutoEnabled      bool                   `json:"autostart_enabled"`
    CooldownSec      int                    `json:"cooldown_sec"`
    Provider         string                 `json:"provider"`
    Providers        []rates.ProviderInfo   `json:"providers"`
    ProviderStatuses []rates.ProviderStatus `json:"provider_statuses"`
    Stale            bool                   `json:"stale"`
    Lang             string                 `json:"lang"`
    Languages        []i18n.Language        `json:"languages"`
    I18n             map[string]string      `json:"i18n"`
    Update           ratesUpdate            `json:"update"`
}

// App is the central application state.
type App struct {
    ctx    context.Context
    cfg    *config.Config
    cfgMu  sync.Mutex
    ratesM *rates.Manager
    trayB  TrayBridge

    exiting       atomic.Bool
    busy          atomic.Bool
    cooldownUntil atomic.Int64
    stale         atomic.Bool
    failures      atomic.Int64
    shownOnce     atomic.Bool
    curMode       atomic.Value // "main" | "settings"

    retryMu    sync.Mutex
    retryTimer *time.Timer

    watcherMu   sync.Mutex
    watcherStop chan struct{}

    posMu        sync.Mutex
    posInit      bool
    posX, posY   int
    posSaveTimer *time.Timer

    stopMu sync.Mutex
    stopCh chan struct{}
}

func New(cfg *config.Config, rm *rates.Manager, tb TrayBridge) *App {
    return &App{cfg: cfg, ratesM: rm, trayB: tb}
}

func (a *App) SetTray(tb TrayBridge) { a.trayB = tb }

/* ------------------------- Wails lifecycle hooks ------------------------- */

func (a *App) Startup(ctx context.Context) {
    a.ctx = ctx
    a.startTicker()
    if a.rememberPos() { // the watcher is only needed when the feature is on
        a.startPositionWatcher()
    }
    log.Printf("[app] startup complete (provider=%s, lang=%s)", a.ratesM.Preferred(), i18n.Current())
    go a.BackgroundRefresh(false) // warm-up, no cooldown
}

func (a *App) BeforeClose(ctx context.Context) bool {
    // Closing the window = hiding it (the widget lives in the tray).
    if a.exiting.Load() {
        return false
    }
    runtime.WindowHide(ctx)
    return true
}

/* ------------------------------ frontend log ----------------------------- */

// LogFrontend records a message from the JS side into the same log file.
func (a *App) LogFrontend(level, msg string) {
    log.Printf("[frontend:%s] %s", level, msg)
}