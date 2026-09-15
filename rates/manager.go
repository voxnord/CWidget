package rates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// FetchTimeoutPerProvider is a hard limit for ONE attempt of ONE provider.
// Without it a hanging source would consume the whole http-client timeout
// (15s) before the failover chain moves to the fallback, leaving the user
// staring at "Updating…". Frankfurter (3 requests per Fetch) fits into the
// limit with room to spare.
const FetchTimeoutPerProvider = 8 * time.Second

// Registration binds a stable provider key to its implementation.
// The key is stored in the config and is independent of the human-readable
// name (Name()).
type Registration struct {
    Key      string
    Provider Provider
}

// ProviderInfo describes a provider for the UI (the settings list is built
// from it).
type ProviderInfo struct {
    Key  string `json:"key"`
    Name string `json:"name"`
}

// ProviderStatus is the outcome of the last attempt of a specific provider.
// Filled by the manager on every Fetch; intended for UI diagnostics.
type ProviderStatus struct {
    Key       string    `json:"key"`
    Name      string    `json:"name"`
    Tried     bool      `json:"tried"`              // was there at least one attempt
    OK        bool      `json:"ok"`                 // outcome of the last attempt
    LastErr   string    `json:"last_err,omitempty"` // full text of the last error
    LastErrAt time.Time `json:"last_err_at,omitempty"`
    LastOKAt  time.Time `json:"last_ok_at,omitempty"`
}

// Manager keeps the last successful snapshot, distributes requests across
// providers and survives restarts thanks to the disk cache.
//
// The currency list is strictly bound to the current snapshot: switching
// providers fully replaces the set of codes and names — no "tails" from the
// previous source are possible. Traversal order on refresh: the selected
// provider first, the rest in registration order as fallbacks.
type Manager struct {
    mu        sync.RWMutex
    regs      []Registration
    preferred string // key of the selected provider (always non-empty, see main.go)
    current   *Table
    statuses  map[string]*ProviderStatus
    cachePath string
    busy      atomic.Bool
}

func NewManager(regs []Registration, cachePath string) (*Manager, error) {
    m := &Manager{regs: regs, cachePath: cachePath, statuses: map[string]*ProviderStatus{}}
    if b, err := os.ReadFile(cachePath); err == nil {
        var cached Table
        if json.Unmarshal(b, &cached) == nil && cached.Anchor != "" && len(cached.Points) > 0 {
            cached.FetchedAt = cached.FetchedAt.UTC()
            m.current = &cached
        }
    }
    return m, nil
}

/* --------------------------- provider registry --------------------------- */

// SetPreferred selects the active provider by key.
func (m *Manager) SetPreferred(key string) {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.preferred = key
}

// Preferred returns the key of the selected provider.
func (m *Manager) Preferred() string {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return m.preferred
}

// HasKey reports whether a provider with this key exists.
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

// Providers returns a snapshot of the registered providers for the UI.
func (m *Manager) Providers() []ProviderInfo {
    m.mu.RLock()
    defer m.mu.RUnlock()
    out := make([]ProviderInfo, 0, len(m.regs))
    for _, r := range m.regs {
        out = append(out, ProviderInfo{Key: r.Key, Name: r.Provider.Name()})
    }
    return out
}

// Statuses returns a snapshot of all provider states (for UI diagnostics).
func (m *Manager) Statuses() []ProviderStatus {
    m.mu.RLock()
    defer m.mu.RUnlock()
    out := make([]ProviderStatus, 0, len(m.regs))
    for _, r := range m.regs {
        if st, ok := m.statuses[r.Key]; ok && st != nil {
            cp := *st
            cp.Key, cp.Name = r.Key, r.Provider.Name()
            out = append(out, cp)
            continue
        }
        out = append(out, ProviderStatus{Key: r.Key, Name: r.Provider.Name()})
    }
    return out
}

// recordStatus stores the outcome of an attempt (takes its own Lock).
func (m *Manager) recordStatus(key string, err error) {
    m.mu.Lock()
    defer m.mu.Unlock()
    st, ok := m.statuses[key]
    if !ok {
        st = &ProviderStatus{}
        m.statuses[key] = st
    }
    st.Tried = true
    if err != nil {
        st.OK = false
        st.LastErr = err.Error()
        st.LastErrAt = time.Now()
        return
    }
    st.OK = true
    st.LastErr = ""
    st.LastOKAt = time.Now()
}

// ordered is the traversal order: the selected one first, the rest as backup.
func (m *Manager) ordered() []Registration {
    m.mu.RLock()
    defer m.mu.RUnlock()
    out := make([]Registration, 0, len(m.regs))
    if m.preferred != "" {
        for _, r := range m.regs {
            if r.Key == m.preferred {
                out = append(out, r)
                break
            }
        }
    }
    for _, r := range m.regs {
        if r.Key != m.preferred {
            out = append(out, r)
        }
    }
    return out
}

/* -------------------------------- refresh -------------------------------- */

// TryRefresh walks the providers in order; the first successful snapshot
// becomes current and fully replaces the previous one (together with the
// currency set and names). Each attempt is limited by FetchTimeoutPerProvider
// independently of the others.
func (m *Manager) TryRefresh(ctx context.Context) (*Table, error) {
    if !m.busy.CompareAndSwap(false, true) {
        if t, ok := m.Current(); ok {
            return t, nil // someone is already refreshing — return the current data
        }
    }
    defer m.busy.Store(false)

    var lastErr error
    for _, reg := range m.ordered() {
        if err := ctx.Err(); err != nil {
            return nil, err
        }
        fctx, cancel := context.WithTimeout(ctx, FetchTimeoutPerProvider)
        tbl, err := reg.Provider.Fetch(fctx)
        cancel()
        m.recordStatus(reg.Key, err)
        if err != nil {
            lastErr = fmt.Errorf("%s: %w", reg.Provider.Name(), err)
            continue
        }
        tbl.FetchedAt = tbl.FetchedAt.UTC()
        tbl.SourceKey = reg.Key // key for localizing the name on the frontend
        m.mu.Lock()
        m.current = tbl
        m.mu.Unlock()
        m.persist(tbl)
        return tbl, nil
    }
    if lastErr == nil {
        lastErr = errors.New("rates: provider list is empty")
    }
    return nil, lastErr
}

// Current returns the last snapshot (possibly from the previous run's cache).
func (m *Manager) Current() (*Table, bool) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return m.current, m.current != nil
}

// Names returns the "code -> name" dictionary of STRICTLY the current
// provider. On a source switch the set is replaced wholesale, without
// accumulation.
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