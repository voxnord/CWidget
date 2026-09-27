package rates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// FetchTimeoutPerProvider is a hard limit for ONE attempt of ONE provider.
const FetchTimeoutPerProvider = 8 * time.Second

// StaleCacheAge defines how old a snapshot may be (by QuoteDate) while
// still being servable as a cached fallback. One day fits daily-published
// official rates: "today" and "yesterday" are both fine, anything older is
// honestly stale.
const StaleCacheAge = 24 * time.Hour

// Registration binds a stable provider key to its implementation.
type Registration struct {
    Key      string
    Provider Provider
}

// ProviderInfo describes a provider for the UI.
type ProviderInfo struct {
    Key  string `json:"key"`
    Name string `json:"name"`
}

// ProviderStatus is the outcome of the last attempt of a specific provider.
type ProviderStatus struct {
    Key       string    `json:"key"`
    Name      string    `json:"name"`
    Tried     bool      `json:"tried"`
    OK        bool      `json:"ok"`
    LastErr   string    `json:"last_err,omitempty"`
    LastErrAt time.Time `json:"last_err_at,omitempty"`
    LastOKAt  time.Time `json:"last_ok_at,omitempty"`
}

// cacheFile is the on-disk format: one snapshot PER provider.
type cacheFile struct {
    Snapshots map[string]*Table `json:"snapshots"`
}

// Manager keeps one snapshot per provider.
//
// SERVING MODEL (provider-agnostic — nothing here knows about specific
// currencies or sources):
//
//   - LIVE WINS. Any successful fetch of the selected provider (or a
//     fallback covering the pair) is served immediately, replacing
//     whatever was displayed — a live answer is never worse than cached
//     data. A provider switch (SetPreferred) therefore always ends up on
//     the newly selected source's fresh data.
//   - CACHE is the fallback FOR THE SELECTED SOURCE: while the selected
//     provider is down, ITS OWN last snapshot serves the pairs it covers
//     (marked FromCache), until it goes stale (StaleCacheAge). A cache is
//     never shown for a provider that is alive, and never for a pair the
//     selected source does not cover.
//   - FALLBACK providers serve the pairs they cover (marked Fallback) —
//     live answers, so they beat any cache.
//
// The frontend refines this per pair: when the served snapshot does not
// cover the displayed pair, it looks for a cached snapshot of another
// source that does (pickCachedTable) — still marked, still fresh-gated.
type Manager struct {
    mu        sync.RWMutex
    regs      []Registration
    preferred string            // key of the selected provider
    served    *Table            // last table handed out (live or cached copy)
    snapshots map[string]*Table // per-provider snapshots (disk cache mirror)
    statuses  map[string]*ProviderStatus
    cachePath string
    busy      atomic.Bool

    pendingPub time.Time // publication being chased; guarded by mu
}

func NewManager(regs []Registration, cachePath string) (*Manager, error) {
    m := &Manager{
        regs:      regs,
        cachePath: cachePath,
        snapshots: map[string]*Table{},
        statuses:  map[string]*ProviderStatus{},
    }
    m.loadCache()
    return m, nil
}

// loadCache reads the per-provider cache. The previous single-snapshot
// format is imported under its own source_key — no migration needed.
func (m *Manager) loadCache() {
    raw, err := os.ReadFile(m.cachePath)
    if err != nil {
        return
    }
    var cf cacheFile
    if json.Unmarshal(raw, &cf) == nil && len(cf.Snapshots) > 0 {
        m.snapshots = cf.Snapshots
        return
    }
    var t Table
    if json.Unmarshal(raw, &t) == nil && t.Anchor != "" && len(t.Points) > 0 {
        key := t.SourceKey
        if key == "" {
            key = "unknown"
        }
        m.snapshots[key] = &t
        log.Printf("[rates] legacy single-snapshot cache imported under key %q", key)
    }
}

/* --------------------------- provider registry --------------------------- */

func (m *Manager) SetPreferred(key string) {
    m.mu.Lock()
    m.preferred = key
    m.pendingPub = time.Time{} // another source — another publication schedule
    // Reset the served view ONLY if it belongs to another provider: the UI
    // must not keep showing the previous source's data while the newly
    // selected one is being fetched. The frontend shows its "unavailable"
    // state until the first live answer (or the new source's cached
    // snapshot, seeded by Current() below).
    if m.served != nil && m.served.SourceKey != key {
        if snap := m.snapshots[key]; snap != nil && cacheIsFresh(snap) {
            cp := *snap
            cp.FromCache = true
            m.served = &cp // seed from the NEW source's own cache — never from another source
        } else {
            m.served = nil // honest "fetching" state until a live answer
        }
    }
    m.mu.Unlock()
}

func (m *Manager) Preferred() string {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return m.preferred
}

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

func (m *Manager) Providers() []ProviderInfo {
    m.mu.RLock()
    defer m.mu.RUnlock()
    out := make([]ProviderInfo, 0, len(m.regs))
    for _, r := range m.regs {
        out = append(out, ProviderInfo{Key: r.Key, Name: r.Provider.Name()})
    }
    return out
}

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

// ordered — the selected provider first, the rest as fallbacks.
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

// TryRefresh walks providers in order: the selected one first, then
// fallbacks. EVERY successful answer immediately becomes the served table
// (live wins — regardless of what was displayed before). A fallback's
// snapshot is stored under ITS OWN key and never clobbers other sources'.
// When every live provider failed, the SELECTED source's own fresh snapshot
// serves as the cached fallback (marked); other sources' snapshots stay in
// the store for the frontend's per-pair refinement, but the manager does
// not present foreign-source data when the selected source is merely down.
func (m *Manager) TryRefresh(ctx context.Context) (*Table, error) {
    if !m.busy.CompareAndSwap(false, true) {
        if t, ok := m.Current(); ok {
            return t, nil
        }
        return nil, errors.New("rates: refresh already in flight")
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
        tbl.SourceKey = reg.Key

        m.mu.Lock()
        if prev := m.snapshots[reg.Key]; prev != nil && tbl.QuoteDate != "" && tbl.QuoteDate == prev.QuoteDate {
            // Same source, same quote date: nothing new. Keep the
            // publication chase running — the new quote date is still
            // expected.
            prev.FetchedAt = tbl.FetchedAt
            m.served = prev
            m.persistLocked()
            m.mu.Unlock()
            log.Printf("[rates] %s: quote date %s unchanged — snapshot kept, no update needed",
                reg.Provider.Name(), tbl.QuoteDate)
            return prev, nil
        }
        m.snapshots[reg.Key] = tbl
        m.served = tbl
        m.pendingPub = time.Time{} // a chased publication (if any) has landed
        m.persistLocked()
        m.mu.Unlock()
        return tbl, nil
    }

    // Every live provider failed. Cached fallback: the SELECTED source's
    // OWN fresh snapshot (never another source's — a cache is bound to the
    // provider whose data it is). Other sources' snapshots remain in the
    // store for the frontend's per-pair refinement.
    m.mu.Lock()
    if snap := m.snapshots[m.preferred]; snap != nil && cacheIsFresh(snap) {
        cp := *snap
        cp.FromCache = true
        m.served = &cp
        m.mu.Unlock()
        log.Printf("[rates] all providers failed — serving the selected source's cached snapshot (quotes=%s)",
            snap.QuoteDate)
        return m.served, nil
    }
    m.mu.Unlock()

    if lastErr == nil {
        lastErr = errors.New("rates: provider list is empty")
    }
    return nil, lastErr
}

// cacheIsFresh: the snapshot's quote date is within StaleCacheAge.
func cacheIsFresh(t *Table) bool {
    if t.QuoteDate == "" {
        return false
    }
    qd, err := time.Parse("2006-01-02", t.QuoteDate)
    if err != nil {
        return false
    }
    return time.Since(qd.AddDate(0, 0, 1)) < StaleCacheAge
}

// Snapshots returns copies of all stored snapshots for the frontend's
// per-pair refinement.
func (m *Manager) Snapshots() []Table {
    m.mu.RLock()
    defer m.mu.RUnlock()
    out := make([]Table, 0, len(m.snapshots))
    for _, t := range m.snapshots {
        out = append(out, *t)
    }
    return out
}

// Current returns the last served table, boot-seeding from the selected
// source's own snapshot when nothing was served yet.
func (m *Manager) Current() (*Table, bool) {
    m.mu.Lock()
    defer m.mu.Unlock()
    if m.served == nil && m.preferred != "" {
        if snap := m.snapshots[m.preferred]; snap != nil && cacheIsFresh(snap) {
            cp := *snap
            cp.FromCache = true
            m.served = &cp
        }
    }
    return m.served, m.served != nil
}

// Names returns the "code -> name" dictionary of the served table.
func (m *Manager) Names() map[string]string {
    m.mu.RLock()
    defer m.mu.RUnlock()
    if m.served == nil {
        return nil
    }
    out := make(map[string]string, len(m.served.Names))
    for k, v := range m.served.Names {
        out[k] = v
    }
    return out
}

// NextTick — publication-chase scheduling (see PublicationAware).
func (m *Manager) NextTick(now time.Time, interval time.Duration) time.Time {
    base := now.Add(interval)

    m.mu.RLock()
    pending := m.pendingPub
    m.mu.RUnlock()
    if !pending.IsZero() {
        if now.Before(pending.Add(2 * time.Hour)) {
            if cand := now.Add(15 * time.Minute); cand.Before(base) {
                return cand
            }
            return base
        }
        m.mu.Lock()
        if m.pendingPub.Equal(pending) {
            m.pendingPub = time.Time{}
        }
        m.mu.Unlock()
    }

    m.mu.RLock()
    cur := m.served
    preferred := m.preferred
    m.mu.RUnlock()
    if cur == nil || preferred == "" {
        return base
    }
    var pub PublicationAware
    for _, r := range m.regs {
        if r.Key == preferred {
            if pa, ok := r.Provider.(PublicationAware); ok {
                pub = pa
            }
            break
        }
    }
    if pub == nil {
        return base
    }
    next := pub.NextPublication(now).Add(15 * time.Minute)
    if next.Before(base) {
        m.mu.Lock()
        m.pendingPub = next
        m.mu.Unlock()
        return next
    }
    return base
}

// Caller holds the lock.
func (m *Manager) persistLocked() {
    b, err := json.MarshalIndent(cacheFile{Snapshots: m.snapshots}, "", " ")
    if err != nil {
        return
    }
    tmp := m.cachePath + ".tmp"
    if os.WriteFile(tmp, b, 0o600) == nil {
        _ = os.Rename(tmp, m.cachePath)
    }
}