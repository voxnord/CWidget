// Package i18n provides minimalistic localization backed by JSON
// dictionaries in locales/*.json.
//
// How to add a language:
//  1. copy locales/en.json to locales/de.json (the file name = the language code);
//  2. translate the values; the "lang.name" key is the endonym ("Deutsch");
//  3. rebuild the app — the language appears in the settings and in the tray
//     submenu automatically.
//
// Missing keys fall back to the reference locales/en.json.
package i18n

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

//go:embed locales/*.json
var localeFS embed.FS

// fallbackLang is the reference dictionary; redefined in Load() when
// en.json is absent.
var fallbackLang = "en"

const defaultLang = "ru"

// Language describes a language for the UI (the settings list and the tray
// submenu are built from it).
type Language struct {
    Code string `json:"code"`
    Name string `json:"name"` // endonym: "Русский", "English", "Deutsch"
}

var (
    mu    sync.RWMutex
    dicts = map[string]map[string]string{}
    cur   string
)

// Load reads all locales/*.json. A broken file does not crash the app —
// it is skipped.
func Load() error {
    entries, err := localeFS.ReadDir("locales")
    if err != nil {
        return err
    }
    found := map[string]map[string]string{}
    for _, e := range entries {
        if e.IsDir() || strings.ToLower(filepath.Ext(e.Name())) != ".json" {
            continue
        }
        code := strings.TrimSuffix(e.Name(), ".json")
        b, err := localeFS.ReadFile("locales/" + e.Name())
        if err != nil {
            continue
        }
        d := map[string]string{}
        if err := json.Unmarshal(b, &d); err != nil {
            continue
        }
        found[code] = d
    }
    if len(found) == 0 {
        return errors.New("i18n: no valid JSON found in locales/")
    }
    if _, ok := found[fallbackLang]; !ok {
        fallbackLang = sortedCodes(found)[0]
    }
    mu.Lock()
    dicts = found
    mu.Unlock()
    return nil
}

func sortedCodes(m map[string]map[string]string) []string {
    codes := make([]string, 0, len(m))
    for c := range m {
        codes = append(codes, c)
    }
    sort.Strings(codes)
    return codes
}

// Has reports whether a language with this code exists.
func Has(code string) bool {
    mu.RLock()
    defer mu.RUnlock()
    _, ok := dicts[code]
    return ok
}

// DefaultLanguage returns the default language ("ru") or the first
// alphabetically available one.
func DefaultLanguage() string {
    mu.RLock()
    defer mu.RUnlock()
    if _, ok := dicts[defaultLang]; ok {
        return defaultLang
    }
    return sortedCodes(dicts)[0]
}

// SetLanguage switches the current language (the code must exist).
func SetLanguage(code string) error {
    mu.Lock()
    defer mu.Unlock()
    if _, ok := dicts[code]; !ok {
        return fmt.Errorf("i18n: unknown language %q", code)
    }
    cur = code
    return nil
}

// Current returns the current language code.
func Current() string {
    mu.RLock()
    defer mu.RUnlock()
    return cur
}

// Languages returns the available languages sorted by endonym.
func Languages() []Language {
    mu.RLock()
    defer mu.RUnlock()
    out := make([]Language, 0, len(dicts))
    for code, d := range dicts {
        name := d["lang.name"]
        if name == "" {
            name = code
        }
        out = append(out, Language{Code: code, Name: name})
    }
    sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
    return out
}

// T translates a key: current language -> reference -> the key itself
// (gaps are immediately visible). Placeholders {0}, {1}… are replaced with
// the corresponding args.
func T(key string, args ...any) string {
    mu.RLock()
    c, fb := dicts[cur], dicts[fallbackLang]
    mu.RUnlock()
    s, ok := c[key]
    if !ok {
        s, ok = fb[key]
        if !ok {
            return key
        }
    }
    for i, a := range args {
        s = strings.ReplaceAll(s, fmt.Sprintf("{%d}", i), fmt.Sprint(a))
    }
    return s
}

// Dict returns the full dictionary of the current language, merged with the
// reference (for the frontend).
func Dict() map[string]string {
    mu.RLock()
    defer mu.RUnlock()
    out := make(map[string]string, len(dicts[fallbackLang])+len(dicts[cur]))
    for k, v := range dicts[fallbackLang] {
        out[k] = v
    }
    for k, v := range dicts[cur] {
        out[k] = v
    }
    return out
}