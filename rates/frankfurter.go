package rates

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultFFBase = "https://api.frankfurter.app"

// Frankfurter is the adapter for the Frankfurter API (ECB reference rates).
// Anchor currency: EUR. Note: RUB is not published since March 2022 and KZT
// has never been in the ECB set, so for those codes the manager silently
// falls through to the next provider. No "yesterday" in the response — it is
// fetched with a separate request (non-working days are auto-rolled back by
// the API; if it matches the current date, the history is discarded).
type Frankfurter struct {
    client *http.Client
    base   string
}

func NewFrankfurter(client *http.Client) *Frankfurter {
    return &Frankfurter{client: client, base: defaultFFBase}
}

func (f *Frankfurter) Name() string { return "Frankfurter (ЕЦБ)" }

type ffDoc struct {
    Date  string             `json:"date"`
    Rates map[string]float64 `json:"rates"`
}

func (f *Frankfurter) get(ctx context.Context, url string, dst any) error {
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
    if err != nil {
        return err
    }
    resp, err := f.client.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
        return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
    }
    return json.NewDecoder(resp.Body).Decode(dst)
}

func (f *Frankfurter) Fetch(ctx context.Context) (*Table, error) {
    names := map[string]string{"EUR": "Euro"}
    // /currencies is a non-critical name dictionary (the fallback fills the
    // rest from other adapters)
    var cur map[string]string
    if err := f.get(ctx, f.base+"/currencies", &cur); err == nil {
        for k, v := range cur {
            names[k] = v
        }
    }

    anchor := "EUR"
    var latest ffDoc
    if err := f.get(ctx, f.base+"/latest", &latest); err != nil {
        return nil, err
    }

    points := map[string]RatePoint{anchor: {Price: 1}}
    for code, v := range latest.Rates {
        if v > 0 {
            points[code] = RatePoint{Price: v}
        }
    }

    // "Yesterday" snapshot for the trend arrows.
    yday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
    var hist ffDoc
    if err := f.get(ctx, f.base+"/"+yday, &hist); err == nil &&
        hist.Date != latest.Date && hist.Date != "" {
        for code, v := range hist.Rates {
            p, ok := points[code]
            if !ok || v <= 0 {
                continue
            }
            p.HasPrev, p.Prev = true, v
            points[code] = p
        }
        eur := points[anchor]
        eur.HasPrev, eur.Prev = true, 1
        points[anchor] = eur
    }

    return &Table{
        FetchedAt: time.Now().UTC(), // the moment of the successful request
        QuoteDate: latest.Date,
        Source:    f.Name(),
        Anchor:    anchor,
        Points:    points,
        Names:     names,
    }, nil
}