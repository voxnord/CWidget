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

// StaleCacheAge defines how old a cached snapshot may be (by QuoteDate)
// while still being considered fresh enough to serve. One day is correct
// for daily-published official rates: "today" and "yesterday" are both fine,
// anything older means the source is truly unavailable.
const StaleCacheAge = 24 * time.Hour

// Publication-chase knobs: a tick is placed pubGrace after the expected
// publication (the mirror lag), re-checked every pubRetry while the expected
// QuoteDate flip has not landed, and abandoned after pubWindow — reverting
// to the regular interval.
const (
    pubGrace  = 15 * time.Minute
    pubRetry  = 15 * time.Minute
    pubWindow = 2 * time.Hour
)

// Registration — связка стабильного ключа провайдера и его реализации.
type Registration struct {
    Key      string
    Provider Provider
}

// ProviderInfo — сведения о провайдере для UI.
type ProviderInfo struct {
    Key  string `json:"key"`
    Name string `json:"name"`
}

// ProviderStatus — исход последней попытки конкретного провайдера.
type ProviderStatus struct {
    Key       string    `json:"key"`
    Name      string    `json:"name"`
    Tried     bool      `json:"tried"`
    OK        bool      `json:"ok"`
    LastErr   string    `json:"last_err,omitempty"`
    LastErrAt time.Time `json:"last_err_at,omitempty"`
    LastOKAt  time.Time `json:"last_ok_at,omitempty"`
}

// Manager keeps the last successful snapshot, distributes requests across
// providers and survives restarts thanks to the disk cache.
//
// Fetch order: selected provider → fallback providers (registration order)
// → the disk cache as a last-resort provider, VALIDATED by QuoteDate
// freshness (StaleCacheAge). The cache beats a stale fallback: a two-day-old
// snapshot from the cache is worse than a one-day-old live answer, but a
// one-day-old cache is better than nothing at all.
type Manager struct {
    mu        sync.RWMutex
    regs      []Registration
    preferred string // key of the selected provider (always non-empty, see main.go)
    current   *Table
    statuses  map[string]*ProviderStatus
    pendingPub time.Time // publication being chased; guarded by mu
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

func (m *Manager) SetPreferred(key string) {
    m.mu.Lock()
    m.preferred = key
    m.pendingPub = time.Time{} // another source — another publication schedule
    m.mu.Unlock()
}

func (m *Manager) Preferred() string {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return m.preferred
}

// NextTick returns when the next automatic refresh should run: the sooner
// of (a) the regular interval from now and (b) a publication-chase moment —
// right after the selected source is expected to publish a new QuoteDate,
// or a re-check while a chased publication is overdue within the window.
func (m *Manager) NextTick(now time.Time, interval time.Duration) time.Time {
    base := now.Add(interval)

    m.mu.RLock()
    pending := m.pendingPub
    m.mu.RUnlock()
    if !pending.IsZero() {
        if now.Before(pending.Add(pubWindow)) {
            if cand := now.Add(pubRetry); cand.Before(base) {
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
    cur := m.current
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
    next := pub.NextPublication(now).Add(pubGrace)
    if next.Before(base) {
        m.mu.Lock()
        m.pendingPub = next
        m.mu.Unlock()
        return next
    }
    return base
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

// ordered — порядок обхода: выбранный первым, остальные резервом.
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

// TryRefresh walks providers in order; the first successful snapshot wins.
// When every live provider fails, the disk cache serves as the last resort
// — but only while its QuoteDate is fresh enough (StaleCacheAge). A stale
// cache is NOT served: better an honest "offline" state than silently old
// data.
//
// Deduplication: a snapshot whose QuoteDate equals the currently stored one
// does not replace the cache (the data is identical — only FetchedAt is
// bumped). This keeps the "refresh every N minutes" semantic while reducing
// real network pressure: with daily-published official rates most ticks of
// the day return the same data.
func (m *Manager) TryRefresh(ctx context.Context) (*Table, error) {
    if !m.busy.CompareAndSwap(false, true) {
        if t, ok := m.Current(); ok {
            return t, nil
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
        tbl.SourceKey = reg.Key

        m.mu.RLock()
        prev := m.current
        m.mu.RUnlock()
        if prev != nil && tbl.QuoteDate != "" && tbl.QuoteDate == prev.QuoteDate &&
            prev.SourceKey == reg.Key {
            // Same provider, same quote date: nothing new. Refresh the fetch
            // timestamp (it reflects "when we last checked"), keep the rest.
            prev.FetchedAt = tbl.FetchedAt
            m.persist(prev)
            log.Printf("[rates] %s: quote date %s unchanged — cache kept, no update needed",
                reg.Provider.Name(), tbl.QuoteDate)
            return prev, nil
        }

        m.mu.Lock()
        m.current = tbl
        m.pendingPub = time.Time{} // a chased publication (if any) has landed
        m.mu.Unlock()
        m.persist(tbl)
        return tbl, nil
    }

    // Every live provider failed. Last resort: the disk cache — but only
    // while its quote date is fresh enough. A stale cache is worse than an
    // honest "offline": the user sees the ⚠/offline indicator and knows the
    // data may be old.
    if cur, ok := m.Current(); ok && cacheIsFresh(cur) {
        log.Printf("[rates] all providers failed — serving the fresh-enough disk cache (quotes=%s, fetched at=%s)",
            cur.QuoteDate, cur.FetchedAt.Local().Format("15:04"))
        return cur, nil
    }

    if lastErr == nil {
        lastErr = errors.New("rates: provider list is empty")
    }
    return nil, lastErr
}

// cacheIsFresh reports whether the snapshot's quote date is within the
// StaleCacheAge window. Snapshots without a QuoteDate (very old cache files
// from before this field existed) are treated as stale — honesty first.
func cacheIsFresh(t *Table) bool {
    if t.QuoteDate == "" {
        return false
    }
    qd, err := time.Parse("2006-01-02", t.QuoteDate)
    if err != nil {
        return false
    }
    // End of the quote day + one full day of grace: a snapshot quoted today
    // or yesterday is fine; "two days ago and older" is stale.
    return time.Since(qd.AddDate(0, 0, 1)) < StaleCacheAge
}

func (m *Manager) Current() (*Table, bool) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return m.current, m.current != nil
}

// Names returns the "code -> name" dictionary of STRICTLY the current
// provider.
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