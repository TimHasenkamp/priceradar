package notify

import (
	"testing"
	"time"
)

func TestFormatting(t *testing.T) {
	cents := map[int64]string{
		0: "0,00 €", 5: "0,05 €", 49360: "493,60 €", 129900: "1.299,00 €",
		123456789: "1.234.567,89 €", -2630: "−26,30 €",
	}
	for in, want := range cents {
		if got := formatCents(in, "EUR"); got != want {
			t.Errorf("formatCents(%d) = %q, want %q", in, got, want)
		}
	}
	if got := formatCents(100, "USD"); got != "1,00 USD" {
		t.Errorf("USD: %q", got)
	}
	if got := formatSignedCents(300, ""); got != "+3,00 €" {
		t.Errorf("signed +: %q", got)
	}
	if got := formatSignedCents(-300, ""); got != "−3,00 €" {
		t.Errorf("signed −: %q", got)
	}
	pcts := map[float64]string{-0.1: "−10,0 %", 0.0512: "+5,1 %", 0: "0,0 %", -0.0001: "0,0 %"}
	for in, want := range pcts {
		if got := formatPct(in); got != want {
			t.Errorf("formatPct(%v) = %q, want %q", in, got, want)
		}
	}
	spans := map[time.Duration]string{
		7 * 24 * time.Hour: "7 Tage", 24 * time.Hour: "1 Tag", 36 * time.Hour: "36 Stunden", time.Hour: "1 Stunde",
	}
	for in, want := range spans {
		if got := formatSpan(in); got != want {
			t.Errorf("formatSpan(%v) = %q, want %q", in, got, want)
		}
	}
	if got := escapeMD("A*B_[c](d) `x` #1 <b>"); got != "A\\*B\\_\\[c\\](d) \\`x\\` \\#1 \\<b\\>" {
		t.Errorf("escapeMD: %q", got)
	}
	if availabilityLabel("limited") != "Begrenzt verfügbar" || availabilityLabel("weird") != "weird" {
		t.Error("availabilityLabel")
	}
	labels := map[string]string{
		"blocked": "Blockiert (Bot-Schutz)", "layout_changed": "Seitenlayout geändert?", "": "Fehler", "new": "new",
	}
	for in, want := range labels {
		if got := errorKindLabel(in); got != want {
			t.Errorf("errorKindLabel(%q) = %q", in, got)
		}
	}
}
