package notify

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// formatCents formats integer cents the German way: 129900 → "1.299,00 €".
func formatCents(c int64, currency string) string {
	sign := ""
	if c < 0 {
		sign, c = "−", -c
	}
	euros, cents := c/100, c%100
	digits := strconv.FormatInt(euros, 10)
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	sym := currency
	if currency == "" || currency == "EUR" {
		sym = "€"
	}
	return fmt.Sprintf("%s%s,%02d %s", sign, b.String(), cents, sym)
}

// formatSignedCents is formatCents with an explicit "+" for positive values.
func formatSignedCents(c int64, currency string) string {
	if c > 0 {
		return "+" + formatCents(c, currency)
	}
	return formatCents(c, currency)
}

// formatPct formats a fraction as a signed German percentage: -0.1 → "−10,0 %".
func formatPct(f float64) string {
	s := strconv.FormatFloat(math.Abs(f)*100, 'f', 1, 64)
	s = strings.Replace(s, ".", ",", 1)
	switch {
	case s == "0,0":
	case f < 0:
		s = "−" + s
	default:
		s = "+" + s
	}
	return s + " %"
}

// formatSpan renders a duration in German: 7 Tage, 36 Stunden, 1 Tag.
func formatSpan(d time.Duration) string {
	if d >= 24*time.Hour && d%(24*time.Hour) == 0 {
		if n := int(d / (24 * time.Hour)); n != 1 {
			return fmt.Sprintf("%d Tage", n)
		}
		return "1 Tag"
	}
	h := int(math.Round(d.Hours()))
	if h == 1 {
		return "1 Stunde"
	}
	return fmt.Sprintf("%d Stunden", h)
}

var mdEscaper = strings.NewReplacer(
	`\`, `\\`, "`", "\\`", `*`, `\*`, `_`, `\_`, `[`, `\[`, `]`, `\]`,
	`<`, `\<`, `>`, `\>`, `#`, `\#`, `|`, `\|`,
)

// escapeMD escapes Markdown control characters in user/shop provided text.
func escapeMD(s string) string { return mdEscaper.Replace(s) }

var availabilityLabels = map[string]string{
	"in_stock":     "Lieferbar",
	"out_of_stock": "Ausverkauft",
	"preorder":     "Vorbestellbar",
	"limited":      "Begrenzt verfügbar",
	"unknown":      "Verfügbarkeit unbekannt",
}

func availabilityLabel(a string) string {
	if l, ok := availabilityLabels[a]; ok {
		return l
	}
	return a
}

var errorKindLabels = map[string]string{
	"blocked":           "Blockiert (Bot-Schutz)",
	"layout_changed":    "Seitenlayout geändert?",
	"not_found":         "Nicht gefunden",
	"transient":         "Temporärer Fehler",
	"robots_disallowed": "robots.txt verbietet Abruf",
	"internal":          "Interner Fehler",
}

func errorKindLabel(k string) string {
	if l, ok := errorKindLabels[k]; ok {
		return l
	}
	if k == "" {
		return "Fehler"
	}
	return k
}
