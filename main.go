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

// winTitle — внутренний уникальный заголовок окна: popup-модуль ищет окно
// по нему (таскбар/позиция/фокус). Пользователю не виден, менять не обязательно.
const winTitle = "cw-floating-converter"

func main() {
    cfg, err := config.Load()
    if err != nil {
        log.Fatalf("config: %v", err)
    }

    // Локализация: словари ДО создания трея; нормализация языка из конфига.
    if err := i18n.Load(); err != nil {
        log.Fatalf("i18n: %v", err)
    }
    if !i18n.Has(cfg.Language) {
        cfg.Language = i18n.DefaultLanguage()
        if err := config.Save(cfg); err != nil {
            log.Printf("config: не удалось сохранить язык: %v", err)
        }
    }
    if err := i18n.SetLanguage(cfg.Language); err != nil {
        log.Fatalf("i18n: %v", err)
    }

    httpc := &http.Client{Timeout: 15 * time.Second}

    // ---- Реестр провайдеров: ЕДИНСТВЕННОЕ место для подключения нового API. ----
    factory := map[string]func(*http.Client) rates.Provider{
        "cbr":         func(c *http.Client) rates.Provider { return rates.NewCBR(c) },
        "frankfurter": func(c *http.Client) rates.Provider { return rates.NewFrankfurter(c) },
    }
    order := cfg.Priority
    if len(order) == 0 {
        order = []string{"cbr", "frankfurter"}
    }
    var regs []rates.Registration
    for _, key := range order {
        if mk, ok := factory[key]; ok {
            regs = append(regs, rates.Registration{Key: key, Provider: mk(httpc)})
        } else {
            log.Printf("config: неизвестный провайдер %q — пропущен", key)
        }
    }
    if len(regs) == 0 {
        regs = append(regs,
            rates.Registration{Key: "cbr", Provider: rates.NewCBR(httpc)},
            rates.Registration{Key: "frankfurter", Provider: rates.NewFrankfurter(httpc)},
        )
    }

    cachePath, _ := config.DataFile("rates-cache.json")
    manager, err := rates.NewManager(regs, cachePath)
    if err != nil {
        log.Printf("rates: кэш не загружен (%v)", err)
    }

    if !manager.HasKey(cfg.Provider) {
        cfg.Provider = regs[0].Key
        if err := config.Save(cfg); err != nil {
            log.Printf("config: не удалось сохранить провайдера: %v", err)
        }
    }
    manager.SetPreferred(cfg.Provider)

    app.WindowTitle = winTitle

    appCore := app.New(cfg, manager, nil)
    hooks := tray.Hooks{
        ShowWidget:      appCore.ShowWidget,
        OpenSettings:    appCore.OpenSettingsView,
        Refresh:         appCore.ManualRefresh,
        ToggleAutoStart: func(enable bool) { _ = appCore.SetAutoStart(enable) },
        SetLanguage:     func(code string) { _ = appCore.SetLanguage(code) },
        Quit:            appCore.Quit,
    }
    trayCtl := tray.New(iconICO, iconPNG, hooks)
    appCore.SetTray(trayCtl)
    go trayCtl.Run()

    // Профиль WebView2 — внутрь папки данных приложения,
    // чтобы в %APPDATA% не заводилась отдельная папка "<имя_exe>.exe".
    webviewData := ""
    if d, err := config.Dir(); err == nil {
        webviewData = filepath.Join(d, "EBWebView")
        _ = os.MkdirAll(webviewData, 0o755)
    }

    err = wails.Run(&options.App{
        Title:       winTitle,
        Width:       380,
        Height:      260,
        Frameless:   true, // рамки нет — наше оформление
        StartHidden: true,  // стартует свёрнутым в трей
        AssetServer: &assetserver.Options{Assets: assets},
        OnStartup:     appCore.Startup,
        OnBeforeClose: appCore.BeforeClose, // закрытие = Hide
        OnDomReady: func(_ context.Context) {
            popup.RemoveFromTaskbar(winTitle)
            popup.LockResize(winTitle)
            popup.MoveNearTray(winTitle)
            popup.RoundCorners(winTitle)
        },
        // «Стекло» + прозрачность под скруглённые углы; фиксированный профиль.
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