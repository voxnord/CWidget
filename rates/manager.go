package rates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

// Registration — связка стабильного ключа провайдера и его реализации.
// Ключ хранится в конфиге и не зависит от человекочитаемого имени (Name()).
type Registration struct {
    Key      string
    Provider Provider
}

// ProviderInfo — сведения о провайдере для UI (список в настройках строится из него).
type ProviderInfo struct {
    Key  string `json:"key"`
    Name string `json:"name"`
}

// Manager хранит последний удачный снимок, распределяет запросы по провайдерам
// и переживает рестарты благодаря дисковому кэшу.
//
// Список валют строго привязан к текущему снимку: смена провайдера полностью
// заменяет набор кодов и имён — «хвосты» от предыдущего источника невозможны.
// Порядок обхода при обновлении: выбранный провайдер первым, остальные —
// в порядке регистрации как резерв (отказоустойчивость).
type Manager struct {
    mu        sync.RWMutex
    regs      []Registration
    preferred string // ключ выбранного провайдера (всегда непустой, см. main.go)
    current   *Table
    cachePath string
    busy      atomic.Bool
}

func NewManager(regs []Registration, cachePath string) (*Manager, error) {
    m := &Manager{regs: regs, cachePath: cachePath}
    if b, err := os.ReadFile(cachePath); err == nil {
        var cached Table
        if json.Unmarshal(b, &cached) == nil && cached.Anchor != "" && len(cached.Points) > 0 {
            cached.FetchedAt = cached.FetchedAt.UTC()
            m.current = &cached
        }
    }
    return m, nil
}

/* ------------------------- реестр провайдеров ---------------------------- */

// SetPreferred выбирает активный провайдер по ключу.
func (m *Manager) SetPreferred(key string) {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.preferred = key
}

// Preferred — ключ выбранного провайдера.
func (m *Manager) Preferred() string {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return m.preferred
}

// HasKey — существует ли провайдер с таким ключом.
// Пустой ключ валиден только если в реестре есть явная регистрация.
func (m *Manager) HasKey(key string) bool {
    m.mu.RLock()
    defer m.mu.RUnlock()
    for _, r := range m.regs {
        if r.Key == key {
            return true
        }
    }
    return false
}

// Providers — снапшот списка зарегистрированных провайдеров для UI.
func (m *Manager) Providers() []ProviderInfo {
    m.mu.RLock()
    defer m.mu.RUnlock()
    out := make([]ProviderInfo, 0, len(m.regs))
    for _, r := range m.regs {
        out = append(out, ProviderInfo{Key: r.Key, Name: r.Provider.Name()})
    }
    return out
}

// ordered — порядок обхода: выбранный первым, остальные резервом.
func (m *Manager) ordered() []Provider {
    m.mu.RLock()
    defer m.mu.RUnlock()
    out := make([]Provider, 0, len(m.regs))
    if m.preferred != "" {
        for _, r := range m.regs {
            if r.Key == m.preferred {
                out = append(out, r.Provider)
                break
            }
        }
    }
    for _, r := range m.regs {
        if r.Key != m.preferred {
            out = append(out, r.Provider)
        }
    }
    return out
}

/* ----------------------------- обновление -------------------------------- */

// TryRefresh проходит провайдеров по порядку; первый удачный снимок становится текущим
// и ПОЛНОСТЬЮ заменяет прежний (вместе с набором валют и имён).
func (m *Manager) TryRefresh(ctx context.Context) (*Table, error) {
    if !m.busy.CompareAndSwap(false, true) {
        if t, ok := m.Current(); ok {
            return t, nil // уже кто-то обновляет — вернём текущее
        }
    }
    defer m.busy.Store(false)

    var lastErr error
    for _, p := range m.ordered() {
        if err := ctx.Err(); err != nil {
            return nil, err
        }
        tbl, err := p.Fetch(ctx)
        if err != nil {
            lastErr = fmt.Errorf("%s: %w", p.Name(), err)
            continue
        }
        tbl.FetchedAt = tbl.FetchedAt.UTC()
        m.mu.Lock()
        m.current = tbl
        m.mu.Unlock()
        m.persist(tbl)
        return tbl, nil
    }
    if lastErr == nil {
        lastErr = errors.New("rates: список провайдеров пуст")
    }
    return nil, lastErr
}

// Current — последний снимок (возможно из кэша прошлого запуска).
func (m *Manager) Current() (*Table, bool) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return m.current, m.current != nil
}

// Names — справочник «код → имя» СТРОГО текущего провайдера.
// При смене источника набор заменяется целиком, без накопления.
func (m *Manager) Names() map[string]string {
    m.mu.RLock()
    defer m.mu.RUnlock()
    if m.current == nil {
        return nil
    }
    out := make(map[string]string, len(m.current.Names))
    for k, v := range m.current.Names {
        out[k] = v
    }
    return out
}

func (m *Manager) persist(t *Table) {
    b, err := json.MarshalIndent(t, "", " ")
    if err != nil {
        return
    }
    tmp := m.cachePath + ".tmp"
    if os.WriteFile(tmp, b, 0o600) == nil {
        _ = os.Rename(tmp, m.cachePath)
    }
}