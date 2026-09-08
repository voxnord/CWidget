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

// Manager хранит последний удачный снимок, распределяет запросы по провайдерам
// и переживает рестарты благодаря дисковому кэшу.
type Manager struct {
    mu        sync.RWMutex
    providers []Provider
    current   *Table
    names     map[string]string // накопленный словарь кодов/имён (union по всем историям)
    cachePath string
    busy      atomic.Bool
}

func NewManager(providers []Provider, cachePath string) (*Manager, error) {
    m := &Manager{providers: providers, cachePath: cachePath, names: map[string]string{}}
    if b, err := os.ReadFile(cachePath); err == nil {
        var cached Table
        if json.Unmarshal(b, &cached) == nil && cached.Anchor != "" && len(cached.Points) > 0 {
            cached.FetchedAt = cached.FetchedAt.UTC()
            m.current = &cached
            m.mergeNames(cached.Names)
        }
    }
    return m, nil
}

func (m *Manager) mergeNames(src map[string]string) {
    if src == nil {
        return
    }
    m.mu.Lock()
    defer m.mu.Unlock()
    for k, v := range src {
        if k != "" && v != "" {
            m.names[k] = v
        }
    }
}

// TryRefresh проходит провайдеров по порядку; первый удачный снимок становится текущим.
func (m *Manager) TryRefresh(ctx context.Context) (*Table, error) {
    if !m.busy.CompareAndSwap(false, true) {
        if t, ok := m.Current(); ok {
            return t, nil // уже кто-то обновляет — вернём текущее
        }
    }
    defer m.busy.Store(false)

    var lastErr error
    for _, p := range m.providers {
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
        m.mergeNames(tbl.Names)
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

// Names — накопленный справочник для выпадающих списков.
func (m *Manager) Names() map[string]string {
    m.mu.RLock()
    defer m.mu.RUnlock()
    out := make(map[string]string, len(m.names))
    for k, v := range m.names {
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