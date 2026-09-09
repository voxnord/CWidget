package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const (
    appFolder  = "Voxs_CurrencyWidget"
    configName = "config.json"
)

type Config struct {
    Base       string   `json:"base_currency"`                // валюта слева по умолчанию
    Target     string   `json:"target_currency"`              // валюта справа по умолчанию
    AutoStart  bool     `json:"autostart"`                    // зеркало состояния (источник истины — ОС)
    RefreshMin int      `json:"refresh_interval_minutes"`     // период фонового опроса
    Provider   string   `json:"provider,omitempty"`           // выбранный источник курсов (ключ)
    Theme      string   `json:"theme,omitempty"`              // "dark" | "light"
    Language   string   `json:"language,omitempty"`           // код языка ("ru", "en", ...)
    Priority   []string `json:"providers_priority,omitempty"` // порядок регистрации провайдеров
}

// Dir — каталог данных приложения (%AppData%/Voxs_CurrencyWidget).
// Используется и самим конфигом, и main.go (папка профиля WebView2),
// чтобы всё данные приложения жили в одном месте.
func Dir() (string, error) {
    root, err := os.UserConfigDir() // %AppData% / ~/.config
    if err != nil {
        return "", err
    }
    return filepath.Join(root, appFolder), nil
}

// Path — путь к config.json.
func Path() (string, error) {
    d, err := Dir()
    return filepath.Join(d, configName), err
}

// DataFile — сторонний файл в той же папке (например, кэш курсов).
func DataFile(name string) (string, error) {
    d, err := Dir()
    return filepath.Join(d, name), err
}

// Load читает конфиг; при первом запуске создаёт дефолтный.
func Load() (*Config, error) {
    p, err := Path()
    if err != nil {
        return nil, err
    }
    raw, err := os.ReadFile(p)
    if os.IsNotExist(err) {
        c := Default()
        return c, Save(c)
    }
    if err != nil {
        return nil, err
    }
    c := Default()
    if err := json.Unmarshal(raw, c); err != nil {
        return nil, err
    }
    sanitize(c)
    return c, nil
}

func Default() *Config {
    return &Config{Base: "RUB", Target: "KZT", RefreshMin: 60}
}

func sanitize(c *Config) {
    c.Base = strings.ToUpper(strings.TrimSpace(c.Base))
    c.Target = strings.ToUpper(strings.TrimSpace(c.Target))
    c.Provider = strings.ToLower(strings.TrimSpace(c.Provider))
    c.Theme = strings.ToLower(strings.TrimSpace(c.Theme))
    c.Language = strings.ToLower(strings.TrimSpace(c.Language))
    if c.Theme != "light" {
        c.Theme = "dark"
    }
    // Валидность языка проверит i18n в main.go (конфиг не должен о нём знать).
    if c.RefreshMin < 5 {
        c.RefreshMin = 5
    }
    if c.Base == "" || c.Target == "" || c.Base == c.Target {
        c.Base, c.Target = "RUB", "KZT"
    }
}

// Save пишет конфиг атомарно (tmp + rename).
func Save(c *Config) error {
    d, err := Dir()
    if err != nil {
        return err
    }
    if err := os.MkdirAll(d, 0o755); err != nil {
        return err
    }
    p, _ := Path()
    b, err := json.MarshalIndent(c, "", "  ")
    if err != nil {
        return err
    }
    tmp := p + ".tmp"
    if err := os.WriteFile(tmp, b, 0o600); err != nil {
        return err
    }
    return os.Rename(tmp, p)
}