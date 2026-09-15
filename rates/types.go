// Package rates abstracts currency rate sources behind the Provider
// interface (the Adapter pattern) and manages fetching, failover and caching.
package rates

import "time"

// RatePoint is the cost of 1 unit of a currency expressed in the anchor currency.
type RatePoint struct {
    Price   float64 `json:"price"`
    HasPrev bool    `json:"has_prev"`       // is yesterday's rate available?
    Prev    float64 `json:"prev,omitempty"` // yesterday's rate (in the same anchor)
}

// Table is a snapshot of rates from one provider at the time of Fetch().
// After publication the table is NOT mutated — this makes it safe to hand it
// to the UI and to the disk cache.
type Table struct {
    FetchedAt time.Time `json:"fetched_at"`
    // QuoteDate is the source's quote date ("2006-01-02") when the API
    // reports it. For the CBR it is the date of the official rate (it changes
    // once a day). FetchedAt is the moment of OUR successful request.
    QuoteDate string `json:"quote_date,omitempty"`
    // Source is the canonical provider name (from Name(); shown when the
    // localization dictionaries have no translation for this provider).
    // SourceKey is the stable key from the registry (Registration.Key): the
    // frontend looks up a translation by the "provider.<source_key>" key.
    Source    string               `json:"source"`
    SourceKey string               `json:"source_key,omitempty"`
    Anchor    string               `json:"anchor"` // e.g. "RUB" or "EUR"
    Points    map[string]RatePoint `json:"points"`
    Names     map[string]string    `json:"names,omitempty"` // code -> human-readable name
}

// Has reports whether all listed codes are present in the table.
func (t *Table) Has(codes ...string) bool {
    if t == nil {
        return false
    }
    for _, c := range codes {
        if _, ok := t.Points[c]; !ok {
            return false
        }
    }
    return true
}

// Pair returns the "how many units of to per 1 from" rate, today and yesterday.
func (t *Table) Pair(from, to string) (cur, prev float64, hasPrev, ok bool) {
    if t == nil {
        return 0, 0, false, false
    }
    f, okF := t.Points[from]
    q, okQ := t.Points[to]
    if !okF || !okQ || q.Price == 0 {
        return 0, 0, false, false
    }
    cur = f.Price / q.Price
    if f.HasPrev && q.HasPrev && q.Prev != 0 {
        prev, hasPrev = f.Prev/q.Prev, true
    }
    return cur, prev, hasPrev, true
}