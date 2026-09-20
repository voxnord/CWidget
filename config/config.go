// Package config persists application settings as a JSON file in the
// user's OS configuration directory.
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

// Config holds all persisted settings.
type Config struct {
    Base       string   `json:"base_currency"`                // default left currency
    Target     string   `json:"target_currency"`              // default right currency
    AutoStart  bool     `json:"autostart"`                    // mirror only; the OS is the source of truth
    RefreshMin int      `json:"refresh_interval_minutes"`     // background polling period
    Provider   string   `json:"provider,omitempty"`           // selected rates source (key)
    Theme      string   `json:"theme,omitempty"`              // "dark" | "light"
    Language   string   `json:"language,omitempty"`           // language code ("ru", "en", ...)
    StayOpen   bool     `json:"stay_open"`                    // keep the window open on focus loss (and keep it on top while visible)
    Favorites  []string `json:"favorites,omitempty"`          // favorite currency codes (global, provider-agnostic)
    Priority   []string `json:"providers_priority,omitempty"` // provider registration order
}

// Dir returns the application data directory (%AppData%/Voxs_CurrencyWidget).
// It is used by the config itself and by main.go (the WebView2 profile
// folder), so all app data lives in one place.
func Dir() (string, error) {
    root, err := os.UserConfigDir() // %AppData% / ~/.config
    if err != nil {
        return "", err
    }
    return filepath.Join(root, appFolder), nil
}

// Path is the path to config.json.
func Path() (string, error) {
    d, err := Dir()
    return filepath.Join(d, configName), err
}

// DataFile returns a path for an auxiliary file in the same folder
// (e.g. the rates cache).
func DataFile(name string) (string, error) {
    d, err := Dir()
    return filepath.Join(d, name), err
}

// Load reads the config; on the first launch it creates a default one.
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

// NormalizeFavorites uppercases, validates (3 letters A-Z) and deduplicates
// the list, preserving the user's order. Exported: it is also used by the
// app package when the frontend replaces the favorites list.
func NormalizeFavorites(in []string) []string {
    seen := map[string]bool{}
    out := make([]string, 0, len(in))
    for _, c := range in {
        c = strings.ToUpper(strings.TrimSpace(c))
        if len(c) != 3 || seen[c] {
            continue
        }
        valid := true
        for _, r := range c {
            if r < 'A' || r > 'Z' {
                valid = false
                break
            }
        }
        if !valid {
            continue
        }
        seen[c] = true
        out = append(out, c)
    }
    return out
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
    // Language validity is checked by i18n in main.go (the config package
    // must not know about it).
    c.Favorites = NormalizeFavorites(c.Favorites)
    if c.RefreshMin < 5 {
        c.RefreshMin = 5
    }
    if c.Base == "" || c.Target == "" || c.Base == c.Target {
        c.Base, c.Target = "RUB", "KZT"
    }
}

// Save writes the config atomically (tmp + rename).
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