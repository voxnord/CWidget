// Package app wires the Wails lifecycle, the tray, the rates manager and the
// frontend bindings together. Methods are split across files by responsibility:
// window.go — window presentation, refresh.go — rates updates and cooldowns,
// settings.go — the frontend-facing API, ticker.go — the background timer,
// position.go — window position persistence, log.go — the panic shield and
// the log-file writer.
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

// Version is the application version ("1.2.3"). It is injected by the build
// script via ldflags:
//
//	-ldflags "-X <module>/app.Version=1.2.3"
//
// The value "dev" means the app was started from sources (wails dev / go run).
var Version = "dev"

// WindowTitle is the unique window title. It is set from main.go BEFORE
// wails.Run: the popup package locates the window by title (taskbar hiding,
// tray-side positioning, focus).
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
    SetDegraded(degraded bool, preferredKey, actualKey string) // fallback instead of the selected provider
    SetStale(stale bool)                                       // "offline": nobody answered
    Retranslate()                                              // retranslate the menu and tooltip to the current language
}

// ratesUpdate is the payload of the rates:update event.
// Provider is the key of the SELECTED provider; Table.SourceKey is the one
// that ACTUALLY answered. Fallback=true means: the selected provider is
// unavailable, data comes from the fallback.
type ratesUpdate struct {
    Table    *rates.Table      `json:"table"`
    Names    map[string]string `json:"names"`
    Provider string            `json:"provider"`
    Fallback bool              `json:"fallback"`
}

// InitPayload is the full bootstrap state for the frontend.
type InitPayload struct {
    Version          string                 `json:"version"`   // build version (for the About tab)
    Config           config.Config          `json:"config"`
    AutoEnabled      bool                   `json:"autostart_enabled"`
    CooldownSec      int                    `json:"cooldown_sec"` // seconds left in the refresh cooldown
    Provider         string                 `json:"provider"`     // selected provider
    Providers        []rates.ProviderInfo   `json:"providers"`    // registered providers
    ProviderStatuses []rates.ProviderStatus `json:"provider_statuses"`
    Stale            bool                   `json:"stale"`     // "offline" (nobody answered)
    Lang             string                 `json:"lang"`      // current language
    Languages        []i18n.Language        `json:"languages"` // available languages
    I18n             map[string]string      `json:"i18n"`      // dictionary of the current language for the UI
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
    cooldownUntil atomic.Int64 // unix time of the cooldown end
    stale         atomic.Bool  // "offline": the last refresh attempt failed completely
    failures      atomic.Int64 // consecutive full failures (drives the backoff)
    shownOnce     atomic.Bool  // the window has been shown at least once this session
    curMode       atomic.Value // current window mode ("main" | "settings"); the position watcher persists only in "main"

    retryMu    sync.Mutex
    retryTimer *time.Timer // the scheduled backoff retry (at most one)

    posMu        sync.Mutex
    posInit      bool        // first position observation already anchored
    posX, posY   int         // last seen position (the debounced save source)
    posSaveTimer *time.Timer // debounce for position writes

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
    a.startPositionWatcher()
    go a.BackgroundRefresh(false) // warm-up, no cooldown
}

func (a *App) BeforeClose(ctx context.Context) bool {
    // Closing the window = hiding it (the widget lives in the tray).
    // Quitting happens only via the tray menu or uninstall.
    if a.exiting.Load() {
        return false
    }
    runtime.WindowHide(ctx)
    return true
}

/* ------------------------------ frontend log ----------------------------- */

// LogFrontend records a message from the JS side into the same log file.
// A production build has no DevTools console, so without this bridge a
// frontend failure would leave no trace at all.
func (a *App) LogFrontend(level, msg string) {
    log.Printf("[frontend:%s] %s", level, msg)
}