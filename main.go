// CWidget — a currency converter tray widget built with Go and Wails v2.
package main

import (
	"context"
	"embed"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"github.com/voxnord/cwidget/app"
	"github.com/voxnord/cwidget/config"
	"github.com/voxnord/cwidget/i18n"
	"github.com/voxnord/cwidget/popup"
	"github.com/voxnord/cwidget/rates"
	"github.com/voxnord/cwidget/tray"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed assets/icon.ico
var iconICO []byte

//go:embed assets/icon.png
var iconPNG []byte

// winTitle is the internal, unique window title. The popup package locates
// the window by it (taskbar hiding, tray-side positioning, focus). It is not
// visible to the user and does not need to be changed.
const winTitle = "cw-floating-converter"

func main() {
    // Flight recorder: set up BEFORE anything that can fail. Appends across
    // launches (a post-mortem of an idle-time failure needs the previous
    // session), capped in size (see app.NewCapWriter).
    var logCloser func()
    if d, err := config.Dir(); err == nil {
        if w, err := app.NewCapWriter(filepath.Join(d, "cwidget.log")); err == nil {
            log.SetOutput(w)
            logCloser = func() { w.Close() }
            defer logCloser()
        }
    }
    log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)

    // Single-instance guard: a second process exits here, before creating
    // any tray icon or window.
    if !acquireSingleInstance() {
        return
    }

    cfg, err := config.Load()
    if err != nil {
        log.Fatalf("config: %v", err)
    }

    // i18n: load dictionaries BEFORE creating the tray (it must speak the
    // configured language from the very start), then normalize the language
    // stored in the config.
    if err := i18n.Load(); err != nil {
        log.Fatalf("i18n: %v", err)
    }
    if !i18n.Has(cfg.Language) {
        cfg.Language = i18n.DefaultLanguage()
        if err := config.Save(cfg); err != nil {
            log.Printf("config: failed to persist language: %v", err)
        }
    }
    if err := i18n.SetLanguage(cfg.Language); err != nil {
        log.Fatalf("i18n: %v", err)
    }

    log.Printf("[start] ===== launch: CWidget v%s | provider=%s | lang=%s | theme=%s =====",
        app.Version, cfg.Provider, cfg.Language, cfg.Theme)

    httpc := &http.Client{Timeout: 15 * time.Second}

    // ---- Provider registry: the SINGLE place to plug in a new rates API. ----
    factory := map[string]func(*http.Client) rates.Provider{
        "cbr":         func(c *http.Client) rates.Provider { return rates.NewCBR(c) },
        "frankfurter": func(c *http.Client) rates.Provider { return rates.NewFrankfurter(c) },
    }
    order := cfg.Priority
    if len(order) == 0 {
        order = []string{"cbr", "frankfurter"} // default failover order
    }
    var regs []rates.Registration
    for _, key := range order {
        if mk, ok := factory[key]; ok {
            regs = append(regs, rates.Registration{Key: key, Provider: mk(httpc)})
        } else {
            log.Printf("config: unknown provider %q — skipped", key)
        }
    }
    if len(regs) == 0 { // guard against a broken providers_priority
        regs = append(regs,
            rates.Registration{Key: "cbr", Provider: rates.NewCBR(httpc)},
            rates.Registration{Key: "frankfurter", Provider: rates.NewFrankfurter(httpc)},
        )
    }

    cachePath, _ := config.DataFile("rates-cache.json")
    manager, err := rates.NewManager(regs, cachePath)
    if err != nil {
        log.Printf("rates: cache not loaded (%v)", err)
    }

    // Normalize the selected provider: empty/invalid -> the first registered one.
    if !manager.HasKey(cfg.Provider) {
        cfg.Provider = regs[0].Key
        if err := config.Save(cfg); err != nil {
            log.Printf("config: failed to save provider: %v", err)
        }
    }
    manager.SetPreferred(cfg.Provider)

    app.WindowTitle = winTitle

    appCore := app.New(cfg, manager, nil)
    hooks := tray.Hooks{
        ShowWidget:   appCore.ShowWidget,
        OpenSettings: appCore.OpenSettingsView,
        Refresh:      appCore.ManualRefresh,
        ToggleAutoStart: func(enable bool) {
            if err := appCore.SetAutoStart(enable); err != nil {
                log.Printf("[tray] autostart toggle failed (enable=%v): %v", enable, err)
            }
        },
        // The tray seeds its checkbox from the actual OS state at menu build
        // time — keeps the context menu in sync with the settings toggle from
        // the very first launch.
        GetAutoStart: appCore.AutoStartState,
        SetLanguage: func(code string) {
            if err := appCore.SetLanguage(code); err != nil {
                log.Printf("[tray] language switch to %q failed: %v", code, err)
            }
        },
        Quit: appCore.Quit,
    }
    trayCtl := tray.New(iconICO, iconPNG, hooks)
    appCore.SetTray(trayCtl)
    go trayCtl.Run()

    // Keep the WebView2 user-data profile inside the app data folder, so the
    // system AppData does not grow a separate "<exe name>.exe" directory.
    webviewData := ""
    if d, err := config.Dir(); err == nil {
        webviewData = filepath.Join(d, "EBWebView")
        _ = os.MkdirAll(webviewData, 0o755)
    }

    err = wails.Run(&options.App{
        Title:       winTitle,
        Width:       380,
        Height:      260,
        Frameless:   true, // no native frame — the UI provides its own chrome
        StartHidden: true,  // starts minimized to the tray
        AssetServer: &assetserver.Options{Assets: assets},
        OnStartup:     appCore.Startup,
        OnBeforeClose: appCore.BeforeClose, // closing the window = hide
        OnShutdown: func(_ context.Context) {
            log.Printf("[start] shutdown")
            if logCloser != nil {
                logCloser()
            }
        },
        OnDomReady: func(_ context.Context) {
            popup.RemoveFromTaskbar(winTitle)
            popup.LockResize(winTitle)
            popup.MoveNearTray(winTitle)
            popup.RoundCorners(winTitle)
            log.Printf("[start] window created and pre-positioned")
        },
        // Translucency + transparency for the rounded corners; fixed webview profile.
        Windows: &windows.Options{
            WebviewIsTransparent:              true,
            WindowIsTranslucent:               true,
            DisableFramelessWindowDecorations: true,
            WebviewUserDataPath:               webviewData,
        },
        Bind: []interface{}{appCore},
    })
    if err != nil {
        log.Fatal(err)
    }
    trayCtl.Stop()
}