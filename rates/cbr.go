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
        // FetchedAt = момент успешного запроса (для метки «обн. 14:30»),
        // QuoteDate = дата официальных котировок ЦБ (меняется раз в день).
        FetchedAt: time.Now().UTC(),
        QuoteDate: quoteDate,
        Source:    c.Name(),
        Anchor:    "RUB",
        Points:    points,
        Names:     names,
    }, nil
}