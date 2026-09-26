package rates

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultCBRURL = "https://www.cbr-xml-daily.ru/daily_json.js"

// CBR is the adapter for the Central Bank of Russia daily rates feed
// (cbr-xml-daily.ru). Anchor currency: RUB. Provides Previous for every
// currency out of the box, so trend arrows work without extra logic.
type CBR struct {
    client *http.Client
    url    string
}

func NewCBR(client *http.Client) *CBR { return &CBR{client: client, url: defaultCBRURL} }

func (c *CBR) Name() string { return "ЦБ РФ" }

type cbrDoc struct {
    Date   time.Time `json:"Date"`
    Valute map[string]struct {
        CharCode string  `json:"CharCode"`
        Nominal  float64 `json:"Nominal"`
        Value    float64 `json:"Value"`
        Previous float64 `json:"Previous"`
        Name     string  `json:"Name"`
    } `json:"Valute"`
}

func (c *CBR) Fetch(ctx context.Context) (*Table, error) {
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
    if err != nil {
        return nil, err
    }
    resp, err := c.client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
        return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
    }
    var doc cbrDoc
    if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
        return nil, err
    }

    points := map[string]RatePoint{
        "RUB": {Price: 1, HasPrev: true, Prev: 1},
    }
    names := map[string]string{"RUB": "Российский рубль"}

    for _, v := range doc.Valute {
        if v.CharCode == "" || v.Nominal <= 0 || v.Value <= 0 {
            continue
        }
        p := RatePoint{Price: v.Value / v.Nominal}
        if v.Previous > 0 {
            p.HasPrev, p.Prev = true, v.Previous/v.Nominal
        }
        points[v.CharCode] = p
        names[v.CharCode] = v.Name
    }

    quoteDate := ""
    if !doc.Date.IsZero() {
        quoteDate = doc.Date.Format("2006-01-02")
    }
    return &Table{
        // IMPORTANT: the time of OUR successful request (for the "upd. 14:30"
        // label), not the CBR quote Date (quotes change once a day — the label
        // would stay the same all day otherwise).
        FetchedAt: time.Now().UTC(),
        QuoteDate: quoteDate,
        Source:    c.Name(),
        Anchor:    "RUB",
        Points:    points,
        Names:     names,
    }, nil
}

// msk is Moscow time as a fixed zone: MSK is UTC+3 year-round (no DST since
// 2014), so no tzdata database is required.
var msk = time.FixedZone("MSK", 3*3600)

// NextPublication implements rates.PublicationAware. The CBR sets official
// rates once per WORKING day at ~15:30 MSK (session results); the mirror
// lags slightly, hence 15:45 MSK. The rate set on day D is valid from D+1 —
// exactly the QuoteDate flip the manager chases. Weekends are skipped
// (Friday's publication covers them).
func (c *CBR) NextPublication(after time.Time) time.Time {
    t := after.In(msk)
    for i := 0; i < 10; i++ {
        cand := time.Date(t.Year(), t.Month(), t.Day(), 15, 45, 0, 0, msk)
        if !cand.After(t) {
            cand = cand.AddDate(0, 0, 1)
        }
        if wd := cand.Weekday(); wd == time.Saturday || wd == time.Sunday {
            t = cand // candidate is a weekend: advance and recompute
            continue
        }
        return cand
    }
    return after.Add(24 * time.Hour)
}