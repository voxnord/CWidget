// Package i18n — минималистичная локализация: JSON-словари из locales/*.json.
//
// Как добавить язык (никакого Go-кода менять не нужно):
//  1. скопируйте locales/en.json в locales/de.json (имя файла = код языка);
//  2. переведите значения, ключ "lang.name" = самоназвание ("Deutsch");
//  3. пересоберите приложение — язык появится в настройках и в подменю трея.
//
// Недостающие ключи берутся из эталонного locales/en.json.
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

// fallbackLang — эталонный словарь; переопределяется в Load(), если en.json нет.
var fallbackLang = "en"

const defaultLang = "ru"

// Language — описание языка для UI (список в настройках и подменю трея).
type Language struct {
    Code string `json:"code"`
    Name string `json:"name"` // самоназвание: "Русский", "English", "Deutsch"
}

var (
    mu    sync.RWMutex
    dicts = map[string]map[string]string{}
    cur   string
)

// Load читает все locales/*.json. Битый файл не роняет приложение — пропускается.
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
        return errors.New("i18n: в locales/ не найдено ни одного валидного JSON")
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

// Has — существует ли язык с таким кодом.
func Has(code string) bool {
    mu.RLock()
    defer mu.RUnlock()
    _, ok := dicts[code]
    return ok
}

// DefaultLanguage — язык по умолчанию ("ru"), либо первый доступный по алфавиту.
func DefaultLanguage() string {
    mu.RLock()
    defer mu.RUnlock()
    if _, ok := dicts[defaultLang]; ok {
        return defaultLang
    }
    return sortedCodes(dicts)[0]
}

// SetLanguage переключает текущий язык (код должен существовать).
func SetLanguage(code string) error {
    mu.Lock()
    defer mu.Unlock()
    if _, ok := dicts[code]; !ok {
        return fmt.Errorf("i18n: неизвестный язык %q", code)
    }
    cur = code
    return nil
}

// Current — код текущего языка.
func Current() string {
    mu.RLock()
    defer mu.RUnlock()
    return cur
}

// Languages — список доступных языков, отсортированный по самоназванию.
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

// T — перевод ключа: текущий язык → эталонный → сам ключ (пропуски видны сразу).
// Плейсхолдеры {0}, {1}… замещаются соответствующими args.
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

// Dict — полный словарь текущего языка, дополненный эталонным (для фронтенда).
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