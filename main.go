package main

import (
	"context"
	"embed"
	"log"
	"net/http"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/you/currency-widget/app"
	"github.com/you/currency-widget/config"
	"github.com/you/currency-widget/popup"
	"github.com/you/currency-widget/rates"
	"github.com/you/currency-widget/tray"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed assets/icon.ico
var iconICO []byte

//go:embed assets/icon.png
var iconPNG []byte

const winTitle = "cw-floating-converter" // уникально — popup ищет окно по нему

func main() {
    cfg, err := config.Load()
    if err != nil {
        log.Fatalf("config: %v", err)
    }

    httpc := &http.Client{Timeout: 15 * time.Second}

    // ---- Реестр провайдеров: ЕДИНСТВЕННОЕ место для подключения нового API.
    // Добавьте сюда одну запись — провайдер сам появится в настройках
    // («Источник курсов») и в каскаде обхода. Больше нигде кода не нужно. ----
    factory := map[string]func(*http.Client) rates.Provider{
        "cbr":         func(c *http.Client) rates.Provider { return rates.NewCBR(c) },
        "frankfurter": func(c *http.Client) rates.Provider { return rates.NewFrankfurter(c) },
    }
    order := cfg.Priority
    if len(order) == 0 {
        order = []string{"cbr", "frankfurter"} // порядок по умолчанию (каскад)
    }
    var regs []rates.Registration
    for _, key := range order {
        if mk, ok := factory[key]; ok {
            regs = append(regs, rates.Registration{Key: key, Provider: mk(httpc)})
        } else {
            log.Printf("config: неизвестный провайдер %q — пропущен", key)
        }
    }
    if len(regs) == 0 { // страховка от битого providers_priority
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

    // Нормализация выбранного источника: пустой/невалидный → первый
    // зарегистрированный (ЦБ РФ). Авто-режима нет: провайдер всегда конкретный.
    if !manager.HasKey(cfg.Provider) {
        cfg.Provider = regs[0].Key
        if err := config.Save(cfg); err != nil {
            log.Printf("config: не удалось сохранить провайдера: %v", err)
        }
    }
    manager.SetPreferred(cfg.Provider)

    app.WindowTitle = winTitle // popup-модуль позиционирует окно по этому заголовку

    appCore := app.New(cfg, manager, nil) // трей внедрим после его создания
    hooks := tray.Hooks{
        ShowWidget:      appCore.ShowWidget,
        OpenSettings:    appCore.OpenSettingsView,
        Refresh:         appCore.ManualRefresh,
        ToggleAutoStart: func(enable bool) { _ = appCore.SetAutoStart(enable) },
        Quit:            appCore.Quit,
    }
    trayCtl := tray.New(iconICO, iconPNG, hooks)
    appCore.SetTray(trayCtl)
    go trayCtl.Run()

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
            popup.MoveNearTray(winTitle) // первичная позиция до первого показа
        },
        Bind: []interface{}{appCore},
    })
    if err != nil {
        log.Fatal(err)
    }
    trayCtl.Stop()
}