package notify

import (
	"fmt"
	"math"
	"strings"
	"time"

	"priceradar/internal/store"
)

// Rules are the notification thresholds (PR_NOTIFY_* env vars).
type Rules struct {
	ChangePct      float64       // min. relative change for price_drop/price_rise, in percent
	ChangeMinCents int64         // min. absolute change for price_drop/price_rise
	Rises          bool          // send price_rise at all
	LowMinHistory  time.Duration // new_low needs a first check at least this old
	TrendWindow    time.Duration
	TrendPct       float64 // min. change of the regression line, in percent
	TrendCooldown  time.Duration
	Failures       int    // consecutive failures for scrape_failing
	PublicURL      string // base URL of the web UI, without trailing slash (optional)
}

// DefaultRules returns the documented defaults.
func DefaultRules() Rules {
	return Rules{
		ChangePct: 5, ChangeMinCents: 100, Rises: true, LowMinHistory: 7 * 24 * time.Hour,
		TrendWindow: 7 * 24 * time.Hour, TrendPct: 8, TrendCooldown: 72 * time.Hour, Failures: 3,
	}
}

// Input is everything Evaluate needs about one recorded check.
type Input struct {
	Product store.Product    // state after the check was recorded
	Check   store.PriceCheck // the new check
	// Before aggregates the successful checks before Check.
	Before store.SuccessSummary
	// Window holds the successful checks within Rules.TrendWindow up to Check,
	// ascending. Check itself may or may not be included.
	Window []store.PriceCheck
	// SinceTarget holds the successful checks since the last target_reached
	// notification (only needed if there is one).
	SinceTarget []store.PriceCheck
	// Last is the latest notification per kind for this product.
	Last map[string]store.Notification
}

// Evaluate returns the messages that fire for in. It is pure: all state
// (history, previous notifications) comes from in.
func Evaluate(in Input, r Rules) []Message {
	p, c := in.Product, in.Check
	if p.Paused || !p.Notify {
		return nil
	}
	b := builder{p: p, r: r}
	if !c.Success {
		if m, ok := failing(in, r, b); ok {
			return []Message{m}
		}
		return nil
	}
	if c.PriceCents == nil {
		return nil
	}
	price := *c.PriceCents
	var out []Message
	if m, ok := targetReached(in, b, price); ok {
		out = append(out, m)
	}
	if in.Before.Count == 0 {
		return out // first check: no comparisons possible
	}
	out = append(out, priceChange(in, r, b, price)...)
	if m, ok := trend(in, r, b, price); ok {
		out = append(out, m)
	}
	if m, ok := stock(in, b, price); ok {
		out = append(out, m)
	}
	return out
}

func targetReached(in Input, b builder, price int64) (Message, bool) {
	t := in.Product.TargetPriceCents
	if t == nil || price > *t {
		return Message{}, false
	}
	if last, ok := in.Last[KindTargetReached]; ok && last.TargetPriceCents != nil && *last.TargetPriceCents == *t {
		// Already notified for this target: re-arm only if a later
		// successful check was above the target.
		armed := false
		for _, ch := range in.SinceTarget {
			if ch.ID != in.Check.ID && ch.Success && ch.PriceCents != nil && *ch.PriceCents > *t {
				armed = true
				break
			}
		}
		if !armed {
			return Message{}, false
		}
	}
	lines := []string{
		"Aktueller Preis: **" + b.money(price) + "**",
		fmt.Sprintf("Zielpreis: %s (%s unter dem Zielpreis)", b.money(*t), b.money(*t-price)),
	}
	if prev := lastPrice(in.Before); prev != nil {
		lines = append(lines, b.prevLine(*prev, price))
	}
	if a := in.Check.Availability; a != nil {
		lines = append(lines, "Verfügbarkeit: "+availabilityLabel(*a))
	}
	return b.msg(KindTargetReached, "Zielpreis erreicht: "+b.name(), "Dein Zielpreis ist erreicht.", lines), true
}

func priceChange(in Input, r Rules, b builder, price int64) []Message {
	var drop, rise bool
	var prev int64
	if pp := lastPrice(in.Before); pp != nil && *pp != price && *pp > 0 {
		prev = *pp
		diff := price - prev
		absDiff := diff
		if absDiff < 0 {
			absDiff = -absDiff
		}
		pct := float64(absDiff) / float64(prev) * 100
		if pct >= r.ChangePct-1e-9 && absDiff >= r.ChangeMinCents {
			drop, rise = diff < 0, diff > 0 && r.Rises
		}
	}
	low := in.Before.MinCents != nil && price < *in.Before.MinCents &&
		in.Before.FirstAt != nil && in.Check.CheckedAt.Sub(*in.Before.FirstAt) >= r.LowMinHistory

	var lines []string
	add := func(s string) { lines = append(lines, s) }
	add("Aktueller Preis: **" + b.money(price) + "**")
	if prev != 0 {
		add(b.prevLine(prev, price))
	}
	switch {
	case low:
		add("Bisheriger Tiefstpreis: " + b.money(*in.Before.MinCents))
		b.targetLine(add)
		title := "Neuer Tiefstpreis: " + b.name()
		lead := "So günstig war der Artikel noch nie, seit PriceRadar ihn beobachtet."
		if drop {
			title += " (" + formatPct(b.frac(prev, price)) + ")"
			lead = "Der Preis ist deutlich gesunken – so günstig war der Artikel noch nie, seit PriceRadar ihn beobachtet."
		}
		return []Message{b.msg(KindNewLow, title, lead, lines)}
	case drop:
		b.targetLine(add)
		return []Message{b.msg(KindPriceDrop, fmt.Sprintf("Preis gesunken: %s (%s)", b.name(), formatPct(b.frac(prev, price))), "Der Preis ist gesunken.", lines)}
	case rise:
		b.targetLine(add)
		return []Message{b.msg(KindPriceRise, fmt.Sprintf("Preis gestiegen: %s (%s)", b.name(), formatPct(b.frac(prev, price))), "Der Preis ist gestiegen.", lines)}
	}
	return nil
}

func trend(in Input, r Rules, b builder, price int64) (Message, bool) {
	var pts []Point
	for _, ch := range in.Window {
		if ch.ID == in.Check.ID || !ch.Success || ch.PriceCents == nil {
			continue
		}
		if ch.CheckedAt.Before(in.Check.CheckedAt.Add(-r.TrendWindow)) || ch.CheckedAt.After(in.Check.CheckedAt) {
			continue
		}
		pts = append(pts, Point{At: ch.CheckedAt, Cents: *ch.PriceCents})
	}
	pts = append(pts, Point{At: in.Check.CheckedAt, Cents: price})
	tr, ok := DetectTrend(pts, r.TrendPct)
	if !ok {
		return Message{}, false
	}
	kind, word, verb := KindTrendDown, "fallend", "gefallen"
	if tr.Direction > 0 {
		kind, word, verb = KindTrendUp, "steigend", "gestiegen"
	}
	if last, ok := in.Last[kind]; ok && in.Check.CheckedAt.Sub(last.CreatedAt) < r.TrendCooldown {
		return Message{}, false
	}
	start, end := int64(math.Round(tr.Start)), int64(math.Round(tr.End))
	lead := fmt.Sprintf("Über die letzten %s (%d Messpunkte) ist der Preis laut Trendlinie um %s %s.",
		formatSpan(r.TrendWindow), tr.Points, strings.TrimPrefix(strings.TrimPrefix(formatPct(tr.Change), "+"), "−"), verb)
	lines := []string{
		"Aktueller Preis: **" + b.money(price) + "**",
		fmt.Sprintf("Trendlinie: %s → %s (%s)", b.money(start), b.money(end), formatPct(tr.Change)),
	}
	if prev := lastPrice(in.Before); prev != nil && *prev != price {
		lines = append(lines, b.prevLine(*prev, price))
	}
	b.targetLine(func(s string) { lines = append(lines, s) })
	return b.msg(kind, "Preistrend "+word+": "+b.name(), lead, lines), true
}

func isAvailable(a string) bool {
	return a == "in_stock" || a == "limited" || a == "preorder"
}

func stock(in Input, b builder, price int64) (Message, bool) {
	last := in.Before.Last
	if last == nil || last.Availability == nil || in.Check.Availability == nil {
		return Message{}, false
	}
	was, now := *last.Availability, *in.Check.Availability
	lines := []string{
		"Verfügbarkeit: **" + availabilityLabel(now) + "** (vorher: " + availabilityLabel(was) + ")",
		"Aktueller Preis: " + b.money(price),
	}
	b.targetLine(func(s string) { lines = append(lines, s) })
	switch {
	case was == "out_of_stock" && isAvailable(now):
		return b.msg(KindBackInStock, "Wieder lieferbar: "+b.name(), "Der Artikel ist wieder verfügbar.", lines), true
	case isAvailable(was) && now == "out_of_stock":
		return b.msg(KindOutOfStock, "Ausverkauft: "+b.name(), "Der Artikel ist nicht mehr verfügbar.", lines), true
	}
	return Message{}, false
}

func failing(in Input, r Rules, b builder) (Message, bool) {
	p := in.Product
	if r.Failures <= 0 || p.ConsecutiveFailures < r.Failures {
		return Message{}, false
	}
	if last, ok := in.Last[KindScrapeFailing]; ok {
		// once per failure streak: re-armed by a success after the last alert
		if p.LastSuccessAt == nil || !p.LastSuccessAt.After(last.CreatedAt) {
			return Message{}, false
		}
	}
	kind, msg := "", ""
	if in.Check.ErrorKind != nil {
		kind = *in.Check.ErrorKind
	}
	if in.Check.ErrorMessage != nil {
		msg = *in.Check.ErrorMessage
	}
	lines := []string{"Fehlerart: **" + errorKindLabel(kind) + "**"}
	if msg != "" {
		lines = append(lines, "Meldung: "+escapeMD(msg))
	}
	if p.LastSuccessAt != nil {
		lines = append(lines, "Letzter erfolgreicher Abruf: "+p.LastSuccessAt.In(berlin).Format("02.01.2006 15:04"))
	}
	lead := fmt.Sprintf("Die letzten %d Abrufe sind fehlgeschlagen.", p.ConsecutiveFailures)
	return b.msg(KindScrapeFailing, "Abruf schlägt fehl: "+b.name(), lead, lines), true
}

// berlin is used to show times to German users; falls back to UTC if the
// zone database is unavailable.
var berlin = func() *time.Location {
	if l, err := time.LoadLocation("Europe/Berlin"); err == nil {
		return l
	}
	return time.UTC
}()

func lastPrice(s store.SuccessSummary) *int64 {
	if s.Last == nil {
		return nil
	}
	return s.Last.PriceCents
}

// builder renders messages for one product.
type builder struct {
	p store.Product
	r Rules
}

func (b builder) name() string                 { return b.p.DisplayName() }
func (b builder) money(c int64) string         { return formatCents(c, b.p.Currency) }
func (b builder) frac(prev, cur int64) float64 { return float64(cur-prev) / float64(prev) }

func (b builder) prevLine(prev, cur int64) string {
	return fmt.Sprintf("Vorheriger Preis: %s (%s, %s)", b.money(prev),
		formatSignedCents(cur-prev, b.p.Currency), formatPct(b.frac(prev, cur)))
}

func (b builder) targetLine(add func(string)) {
	if t := b.p.TargetPriceCents; t != nil {
		add("Zielpreis: " + b.money(*t))
	}
}

func (b builder) appURL() string {
	if b.r.PublicURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/products/%d", b.r.PublicURL, b.p.ID)
}

func (b builder) msg(kind, title, lead string, lines []string) Message {
	var sb strings.Builder
	sb.WriteString("**" + escapeMD(b.name()) + "**\n\n")
	sb.WriteString(lead + "\n\n")
	for _, l := range lines {
		sb.WriteString("- " + l + "\n")
	}
	sb.WriteString("\n")
	links := []string{}
	if u := b.appURL(); u != "" {
		links = append(links, "[In PriceRadar öffnen]("+u+")")
	}
	links = append(links, "[Im Shop ansehen]("+b.p.URL+")")
	sb.WriteString(strings.Join(links, " · "))
	click := b.appURL()
	if click == "" {
		click = b.p.URL
	}
	return Message{Kind: kind, Title: title, Body: sb.String(), Priority: priorities[kind], ClickURL: click}
}
