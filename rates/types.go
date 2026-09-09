package rates

import "time"

// RatePoint — стоимость 1 единицы валюты, выраженная в якорной валюте.
type RatePoint struct {
    Price   float64 `json:"price"`
    HasPrev bool    `json:"has_prev"`       // вчераший курс доступен?
    Prev    float64 `json:"prev,omitempty"` // курс вчера (в тех же якорях)
}

// Table — снимок курсов одного провайдера на момент Fetch().
// После публикации таблица НЕ мутируется — это позволяет безопасно
// отдавать её в UI и в дисковый кэш.
type Table struct {
    FetchedAt time.Time `json:"fetched_at"`
    // QuoteDate — дата котировок источника ("2006-01-02"), если API её сообщает.
    QuoteDate string `json:"quote_date,omitempty"`
    // Source — каноническое имя провайдера (из Name(), отображается,
    // если в словарях локализации нет перевода для этого провайдера).
    // SourceKey — стабильный ключ из реестра (Registration.Key):
    // фронтенд ищет перевод по ключу "provider.<source_key>".
    Source    string               `json:"source"`
    SourceKey string               `json:"source_key,omitempty"`
    Anchor    string               `json:"anchor"` // например "RUB" или "EUR"
    Points    map[string]RatePoint `json:"points"`
    Names     map[string]string    `json:"names,omitempty"` // код → человекочитаемое имя
}

// Has — все перечисленные коды есть в таблице?
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

// Pair — курс «сколько единиц to за 1 from» сегодня и вчера.
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