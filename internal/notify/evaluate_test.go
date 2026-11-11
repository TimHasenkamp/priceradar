package notify

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"priceradar/internal/store"
)

const day = 24 * time.Hour

func ptr[T any](v T) *T { return &v }

// now is the time of the check under test.
var now = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

type hp struct {
	ago   time.Duration
	price int64
	avail string // default in_stock
}

func okAt(id int64, at time.Time, price int64, avail string) store.PriceCheck {
	if avail == "" {
		avail = "in_stock"
	}
	return store.PriceCheck{ID: id, ProductID: 42, CheckedAt: at, Success: true, PriceCents: ptr(price), Availability: ptr(avail)}
}

// buildInput derives the evaluator input from a check history the way
// Service does from the store.
func buildInput(p store.Product, history []hp, c store.PriceCheck, last map[string]store.Notification, r Rules) Input {
	var checks []store.PriceCheck
	for i, h := range history {
		checks = append(checks, okAt(int64(i+1), now.Add(-h.ago), h.price, h.avail))
	}
	in := Input{Product: p, Check: c, Last: last}
	for i := range checks {
		ch := checks[i]
		in.Before.Count++
		if in.Before.FirstAt == nil || ch.CheckedAt.Before(*in.Before.FirstAt) {
			in.Before.FirstAt = &ch.CheckedAt
		}
		if in.Before.MinCents == nil || *ch.PriceCents < *in.Before.MinCents {
			in.Before.MinCents = ch.PriceCents
		}
		in.Before.Last = &checks[i]
	}
	all := append(append([]store.PriceCheck{}, checks...), c)
	for _, ch := range all {
		if !ch.Success {
			continue
		}
		if !ch.CheckedAt.Before(c.CheckedAt.Add(-r.TrendWindow)) {
			in.Window = append(in.Window, ch)
		}
		if n, ok := last[KindTargetReached]; ok && !ch.CheckedAt.Before(n.CreatedAt) {
			in.SinceTarget = append(in.SinceTarget, ch)
		}
	}
	return in
}

func product(mod ...func(*store.Product)) store.Product {
	p := store.Product{ID: 42, Shop: "otto", URL: "https://www.otto.de/p/x-C1/", Name: "Drucker *Pro*", Currency: "EUR",
		Notify: true, ConsecutiveFailures: 0, LastSuccessAt: ptr(now)}
	for _, m := range mod {
		m(&p)
	}
	return p
}

func withTarget(c int64) func(*store.Product) {
	return func(p *store.Product) { p.TargetPriceCents = &c }
}

func notif(kind string, ago time.Duration, target *int64) store.Notification {
	return store.Notification{ID: 1, ProductID: 42, Kind: kind, CreatedAt: now.Add(-ago), TargetPriceCents: target}
}

func newCheck(price int64) store.PriceCheck { return okAt(1000, now, price, "") }

func failed(kind string) store.PriceCheck {
	return store.PriceCheck{ID: 1000, ProductID: 42, CheckedAt: now, ErrorKind: ptr(kind), ErrorMessage: ptr("HTTP 403 `challenge`")}
}

func kinds(ms []Message) []string {
	out := []string{}
	for _, m := range ms {
		out = append(out, m.Kind)
	}
	return out
}

func TestEvaluate(t *testing.T) {
	old := []hp{{30 * day, 1000, ""}, {20 * day, 1000, ""}, {10 * day, 1000, ""}, {1 * day, 1000, ""}}
	young := []hp{{3 * day, 1000, ""}, {1 * day, 1000, ""}}
	tests := []struct {
		name    string
		p       store.Product
		history []hp
		check   store.PriceCheck
		last    map[string]store.Notification
		rules   func(*Rules)
		want    []string
	}{
		// --- first check ---
		{name: "first check: nothing", p: product(), check: newCheck(500), want: []string{}},
		{name: "first check below target: target only", p: product(withTarget(600)), check: newCheck(500), want: []string{KindTargetReached}},

		// --- price change ---
		{name: "unchanged price", p: product(), history: old, check: newCheck(1000), want: []string{}},
		{name: "drop 10%", p: product(), history: young, check: newCheck(900), want: []string{KindPriceDrop}},
		{name: "drop exactly 5% and 1 €", p: product(), history: []hp{{day, 2000, ""}}, check: newCheck(1900), want: []string{KindPriceDrop}},
		{name: "drop below pct", p: product(), history: young, check: newCheck(960), want: []string{}},
		{name: "drop below min cents", p: product(), history: []hp{{day, 1000, ""}}, check: newCheck(940), rules: func(r *Rules) { r.ChangeMinCents = 100 }, want: []string{}},
		{name: "rise 10%", p: product(), history: young, check: newCheck(1100), want: []string{KindPriceRise}},
		{name: "rise disabled", p: product(), history: young, check: newCheck(1100), rules: func(r *Rules) { r.Rises = false }, want: []string{}},
		{name: "compares with previous successful price only", p: product(), history: []hp{{3 * day, 2000, ""}, {day, 1000, ""}}, check: newCheck(1020), want: []string{}},

		// --- new low ---
		{name: "new low + drop are merged", p: product(), history: old, check: newCheck(900), want: []string{KindNewLow}},
		{name: "new low with small change alone", p: product(), history: old, check: newCheck(990), want: []string{KindNewLow}},
		{name: "new low needs history", p: product(), history: young, check: newCheck(900), want: []string{KindPriceDrop}},
		{name: "equal to min is no new low", p: product(), history: []hp{{30 * day, 900, ""}, {day, 1000, ""}}, check: newCheck(900), want: []string{KindPriceDrop}},

		// --- target ---
		{name: "target reached by drop", p: product(withTarget(950)), history: young, check: newCheck(900), want: []string{KindTargetReached, KindPriceDrop}},
		{name: "target equal counts", p: product(withTarget(900)), history: young, check: newCheck(900), want: []string{KindTargetReached, KindPriceDrop}},
		{name: "above target", p: product(withTarget(800)), history: young, check: newCheck(900), want: []string{KindPriceDrop}},
		{name: "target set while already below", p: product(withTarget(950)), history: []hp{{day, 900, ""}}, check: newCheck(900), want: []string{KindTargetReached}},
		{name: "target already notified", p: product(withTarget(950)), history: []hp{{2 * day, 900, ""}, {day, 900, ""}}, check: newCheck(900),
			last: map[string]store.Notification{KindTargetReached: notif(KindTargetReached, 2*day, ptr(int64(950)))}, want: []string{}},
		{name: "target re-armed after price above", p: product(withTarget(950)), history: []hp{{2 * day, 900, ""}, {day, 960, ""}}, check: newCheck(940),
			last: map[string]store.Notification{KindTargetReached: notif(KindTargetReached, 2*day, ptr(int64(950)))}, want: []string{KindTargetReached}},
		{name: "target changed while below", p: product(withTarget(920)), history: []hp{{2 * day, 900, ""}, {day, 900, ""}}, check: newCheck(900),
			last: map[string]store.Notification{KindTargetReached: notif(KindTargetReached, 2*day, ptr(int64(950)))}, want: []string{KindTargetReached}},
		{name: "old notification without target re-arms", p: product(withTarget(950)), history: []hp{{day, 900, ""}}, check: newCheck(900),
			last: map[string]store.Notification{KindTargetReached: notif(KindTargetReached, 2*day, nil)}, want: []string{KindTargetReached}},

		// --- trend ---
		{name: "trend down", p: product(), history: []hp{{20 * day, 800, ""}, {6 * day, 1000, ""}, {4 * day, 980, ""}, {3 * day, 960, ""}, {2 * day, 940, ""}}, check: newCheck(920),
			want: []string{KindTrendDown}},
		{name: "trend down in cooldown", p: product(), history: []hp{{20 * day, 800, ""}, {6 * day, 1000, ""}, {4 * day, 980, ""}, {3 * day, 960, ""}, {2 * day, 940, ""}}, check: newCheck(920),
			last: map[string]store.Notification{KindTrendDown: notif(KindTrendDown, 24*time.Hour, nil)}, want: []string{}},
		{name: "trend down after cooldown", p: product(), history: []hp{{20 * day, 800, ""}, {6 * day, 1000, ""}, {4 * day, 980, ""}, {3 * day, 960, ""}, {2 * day, 940, ""}}, check: newCheck(920),
			last: map[string]store.Notification{KindTrendDown: notif(KindTrendDown, 73*time.Hour, nil)}, want: []string{KindTrendDown}},
		{name: "trend up ignores trend_down cooldown", p: product(), history: []hp{{6 * day, 1000, ""}, {4 * day, 1020, ""}, {3 * day, 1040, ""}, {2 * day, 1060, ""}}, check: newCheck(1080),
			last: map[string]store.Notification{KindTrendDown: notif(KindTrendDown, time.Hour, nil)}, want: []string{KindTrendUp}},
		{name: "old points outside window are ignored", p: product(), history: []hp{{20 * day, 2000, ""}, {15 * day, 1500, ""}, {10 * day, 1000, ""}, {2 * day, 1000, ""}}, check: newCheck(1000),
			want: []string{}},
		{name: "trend needs 4 points", p: product(), history: []hp{{20 * day, 800, ""}, {4 * day, 1000, ""}, {2 * day, 960, ""}}, check: newCheck(920),
			want: []string{}},

		// --- availability ---
		{name: "back in stock", p: product(), history: []hp{{day, 1000, "out_of_stock"}}, check: okAt(1000, now, 1000, "limited"), want: []string{KindBackInStock}},
		{name: "out of stock", p: product(), history: []hp{{day, 1000, "preorder"}}, check: okAt(1000, now, 1000, "out_of_stock"), want: []string{KindOutOfStock}},
		{name: "available to available", p: product(), history: []hp{{day, 1000, "limited"}}, check: okAt(1000, now, 1000, "in_stock"), want: []string{}},
		{name: "unknown is no transition", p: product(), history: []hp{{day, 1000, "unknown"}}, check: okAt(1000, now, 1000, "out_of_stock"), want: []string{}},

		// --- failures ---
		{name: "failures below threshold", p: product(func(p *store.Product) { p.ConsecutiveFailures = 2 }), check: failed("blocked"), want: []string{}},
		{name: "failures reach threshold", p: product(func(p *store.Product) { p.ConsecutiveFailures = 3; p.LastSuccessAt = ptr(now.Add(-day)) }), history: old, check: failed("blocked"),
			want: []string{KindScrapeFailing}},
		{name: "failures already notified", p: product(func(p *store.Product) { p.ConsecutiveFailures = 4; p.LastSuccessAt = ptr(now.Add(-day)) }), check: failed("blocked"),
			last: map[string]store.Notification{KindScrapeFailing: notif(KindScrapeFailing, time.Hour, nil)}, want: []string{}},
		{name: "failures re-armed after success", p: product(func(p *store.Product) { p.ConsecutiveFailures = 3; p.LastSuccessAt = ptr(now.Add(-2 * time.Hour)) }), check: failed("blocked"),
			last: map[string]store.Notification{KindScrapeFailing: notif(KindScrapeFailing, 5*day, nil)}, want: []string{KindScrapeFailing}},
		{name: "never succeeded and already notified", p: product(func(p *store.Product) { p.ConsecutiveFailures = 9; p.LastSuccessAt = nil }), check: failed("blocked"),
			last: map[string]store.Notification{KindScrapeFailing: notif(KindScrapeFailing, 5*day, nil)}, want: []string{}},
		{name: "failed check ignores target", p: product(withTarget(5000), func(p *store.Product) { p.ConsecutiveFailures = 1 }), history: young, check: failed("transient"), want: []string{}},

		// --- product switches ---
		{name: "paused", p: product(withTarget(950), func(p *store.Product) { p.Paused = true }), history: old, check: newCheck(500), want: []string{}},
		{name: "notify disabled", p: product(withTarget(950), func(p *store.Product) { p.Notify = false }), history: old, check: newCheck(500), want: []string{}},
		{name: "notify disabled failing", p: product(func(p *store.Product) { p.Notify = false; p.ConsecutiveFailures = 3 }), check: failed("blocked"), want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := DefaultRules()
			if tt.rules != nil {
				tt.rules(&r)
			}
			got := Evaluate(buildInput(tt.p, tt.history, tt.check, tt.last, r), r)
			if !reflect.DeepEqual(kinds(got), tt.want) {
				t.Fatalf("kinds = %v, want %v", kinds(got), tt.want)
			}
			for _, m := range got {
				if m.Priority != priorities[m.Kind] || m.Title == "" || m.Body == "" {
					t.Errorf("bad message %+v", m)
				}
			}
		})
	}
}

func TestEvaluate_MessageContent(t *testing.T) {
	r := DefaultRules()
	old := []hp{{30 * day, 1000, ""}, {day, 129900, ""}}

	// target + plain drop (the old 10,00 € keeps this from being a new low): escaped name, shop link
	got := Evaluate(buildInput(product(withTarget(100000)), old, newCheck(99900), nil, r), r)
	if len(got) != 2 {
		t.Fatalf("want target + drop/low, got %v", kinds(got))
	}
	m := got[1]
	if m.Kind != KindPriceDrop || m.Priority != 6 {
		t.Fatalf("message: %+v", m)
	}
	for _, want := range []string{"**Drucker \\*Pro\\***", "999,00 €", "1.299,00 €", "−300,00 €", "−23,1 %", "Zielpreis", "1.000,00 €", "[Im Shop ansehen](https://www.otto.de/p/x-C1/)"} {
		if !strings.Contains(m.Body, want) {
			t.Errorf("body misses %q:\n%s", want, m.Body)
		}
	}
	if m.Title != "Preis gesunken: Drucker *Pro* (−23,1 %)" || m.ClickURL != "https://www.otto.de/p/x-C1/" {
		t.Errorf("title/click: %q %q", m.Title, m.ClickURL)
	}

	merged := Evaluate(buildInput(product(), []hp{{30 * day, 1000, ""}, {day, 1000, ""}}, newCheck(800), nil, r), r)
	if len(merged) != 1 || merged[0].Kind != KindNewLow || !strings.Contains(merged[0].Title, "Neuer Tiefstpreis") ||
		!strings.Contains(merged[0].Body, "Bisheriger Tiefstpreis") || !strings.Contains(merged[0].Body, "−20,0 %") {
		t.Fatalf("merged: %+v", merged)
	}

	// public URL: click goes to PriceRadar, both links in body
	r.PublicURL = "https://priceradar.example"
	got = Evaluate(buildInput(product(withTarget(950)), nil, newCheck(900), nil, r), r)
	if len(got) != 1 || got[0].ClickURL != "https://priceradar.example/products/42" ||
		!strings.Contains(got[0].Body, "[In PriceRadar öffnen](https://priceradar.example/products/42)") ||
		!strings.Contains(got[0].Body, "0,50 € unter dem Zielpreis") {
		t.Fatalf("target with public url: %+v", got)
	}

	// failure message: label + escaped error message
	p := product(func(p *store.Product) { p.ConsecutiveFailures = 3 })
	got = Evaluate(buildInput(p, nil, failed("layout_changed"), nil, r), r)
	if len(got) != 1 || !strings.Contains(got[0].Body, "Seitenlayout geändert?") || !strings.Contains(got[0].Body, "HTTP 403 \\`challenge\\`") ||
		!strings.Contains(got[0].Body, "3 Abrufe") {
		t.Fatalf("failure: %+v", got)
	}

	// trend message
	hist := []hp{{20 * day, 800, ""}, {6 * day, 1000, ""}, {4 * day, 980, ""}, {3 * day, 960, ""}, {2 * day, 940, ""}}
	got = Evaluate(buildInput(product(), hist, newCheck(920), nil, r), r)
	if len(got) != 1 || !strings.Contains(got[0].Body, "7 Tage") || !strings.Contains(got[0].Title, "fallend") {
		t.Fatalf("trend: %+v", got)
	}

	// stock messages
	got = Evaluate(buildInput(product(), []hp{{day, 1000, "out_of_stock"}}, okAt(1000, now, 1000, "in_stock"), nil, r), r)
	if len(got) != 1 || got[0].Title != "Wieder lieferbar: Drucker *Pro*" || !strings.Contains(got[0].Body, "Lieferbar") {
		t.Fatalf("stock: %+v", got)
	}
}

func TestDefaultRules(t *testing.T) {
	r := DefaultRules()
	if r.ChangePct != 5 || r.ChangeMinCents != 100 || !r.Rises || r.LowMinHistory != 7*day || r.TrendWindow != 7*day ||
		r.TrendPct != 8 || r.TrendCooldown != 72*time.Hour || r.Failures != 3 {
		t.Fatalf("%+v", r)
	}
}
