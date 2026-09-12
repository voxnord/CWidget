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

// FetchTimeoutPerProvider — жёсткий лимит на ОДНУ попытку одного провайдера.
// Без него зависший/недоступный источник съедал бы весь таймаут http-клиента
// (15 с) до перехода каскада к резерву, и пользователь смотрел бы на
// «Обновление…» непозволительно долго. Frankfurter (3 запроса за Fetch)
// укладывается в лимит с запасом.
const FetchTimeoutPerProvider = 8 * time.Second

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

// ProviderStatus — исход последней попытки конкретного провайдера.
// Заполняется менеджером при каждом Fetch; предназначен для диагностики в UI.
type ProviderStatus struct {
    Key       string    `json:"key"`
    Name      string    `json:"name"`
    Tried     bool      `json:"tried"`                // была ли хоть одна попытка
    OK        bool      `json:"ok"`                   // исход последней попытки
    LastErr   string    `json:"last_err,omitempty"`   // полный текст последней ошибки
    LastErrAt time.Time `json:"last_err_at,omitempty"`
    LastOKAt  time.Time `json:"last_ok_at,omitempty"`
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

// Statuses — снапшот состояний всех провайдеров (для диагностики в настройках).
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

// recordStatus фиксирует исход попытки (вызывать под Lock не нужно — сам берёт Lock).
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

/* ----------------------------- обновление -------------------------------- */

// TryRefresh проходит провайдеров по порядку; первый удачный снимок становится текущим
// и ПОЛНОСТЬЮ заменяет прежний (вместе с набором валют и имён).
// Каждая попытка ограничена FetchTimeoutPerProvider независимо от остальных.
func (m *Manager) TryRefresh(ctx context.Context) (*Table, error) {
    if !m.busy.CompareAndSwap(false, true) {
        if t, ok := m.Current(); ok {
            return t, nil // уже кто-то обновляет — вернём текущее
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
        tbl.SourceKey = reg.Key // ключ для локализации имени на фронте
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