package otto

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"priceradar/internal/scraper"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "otto", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

func ptr(v int64) *int64 { return &v }

var (
	reLDJSON     = regexp.MustCompile(`(?s)<script[^>]*application/ld\+json[^>]*>.*?</script>`)
	rePriceBlock = regexp.MustCompile(`(?s)<ofc-pricing-item-v1.*?</ofc-pricing-item-v1>`)
)

func TestParse_Fixtures(t *testing.T) {
	tests := []struct {
		name         string
		file         string
		mutate       func(string) string
		wantName     string
		wantSKU      string
		wantPrice    int64
		wantOld      *int64
		wantRRP      *int64
		wantAvail    string
		wantImage    string // prefix
		wantWarnings bool
	}{
		{
			name: "regular price", file: "product_regular.html",
			wantName: "Bambu Lab 3D-Drucker A1 Combo", wantSKU: "S8Z7V9T27LCL",
			wantPrice: 49360, wantAvail: scraper.AvailabilityInStock,
			wantImage: "https://i.otto.de/i/otto/236200c1-58b1-428f-be54-b24744dd3f49/",
		},
		{
			name: "reduced with strike price", file: "product_old_price.html",
			wantName: "Bambu Lab 3D-Drucker Bambu Lab A2L 3D-Drucker", wantSKU: "SNUMLNNNVMND",
			wantPrice: 51742, wantOld: ptr(61900), wantAvail: scraper.AvailabilityInStock,
			wantImage: "https://i.otto.de/",
		},
		{
			name: "with UVP above price", file: "product_rrp.html",
			wantSKU:   "S7KFZCRVN9SZ",
			wantPrice: 37791, wantRRP: ptr(36900), wantAvail: scraper.AvailabilityInStock,
			wantImage: "https://i.otto.de/",
		},
		{
			name: "strike price and UVP together", file: "product_old_price_and_rrp.html",
			wantSKU:   "S0O1G0UY195D",
			wantPrice: 3995, wantOld: ptr(5397), wantRRP: ptr(5397), wantAvail: scraper.AvailabilityInStock,
			wantImage: "https://i.otto.de/",
		},
		{
			name: "variant URL returns the variant's sku", file: "product_variant.html",
			wantSKU:   "S0O1G0UY43U9",
			wantPrice: 3995, wantOld: ptr(5397), wantRRP: ptr(5397), wantAvail: scraper.AvailabilityInStock,
		},
		{
			// SYNTHETIC: no real sold-out otto page available yet (otto hides
			// sold-out sizes). Derived from a real page by flipping the flags.
			name: "sold out (synthetic)", file: "product_regular.html",
			mutate: func(s string) string {
				s = strings.Replace(s, "https://schema.org/InStock", "https://schema.org/OutOfStock", 1)
				return strings.Replace(s, `data-is-sold-out="false"`, `data-is-sold-out="true"`, 1)
			},
			wantPrice: 49360, wantAvail: scraper.AvailabilityOutOfStock,
		},
		{
			// SYNTHETIC: availability missing in JSON-LD, sold-out flag on price block
			name: "sold out flag from price block (synthetic)", file: "product_regular.html",
			mutate: func(s string) string {
				s = strings.Replace(s, `"availability": "https://schema.org/InStock",`, "", 1)
				return strings.Replace(s, `data-is-sold-out="false"`, `data-is-sold-out="true"`, 1)
			},
			wantPrice: 49360, wantAvail: scraper.AvailabilityOutOfStock,
		},
		{
			name: "fallback to price block when JSON-LD missing", file: "product_old_price.html",
			mutate:    func(s string) string { return reLDJSON.ReplaceAllString(s, "") },
			wantPrice: 51742, wantOld: ptr(61900), wantAvail: scraper.AvailabilityInStock,
			wantWarnings: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html := fixture(t, tt.file)
			if tt.mutate != nil {
				html = []byte(tt.mutate(string(html)))
			}
			got, err := Parse(html)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if tt.wantName != "" && got.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", got.Name, tt.wantName)
			}
			if got.Name == "" {
				t.Error("Name is empty")
			}
			if tt.wantSKU != "" && got.SKU != tt.wantSKU {
				t.Errorf("SKU = %q, want %q", got.SKU, tt.wantSKU)
			}
			if got.PriceCents != tt.wantPrice {
				t.Errorf("PriceCents = %d, want %d", got.PriceCents, tt.wantPrice)
			}
			if got.Currency != "EUR" {
				t.Errorf("Currency = %q, want EUR", got.Currency)
			}
			assertPtr(t, "OldPriceCents", got.OldPriceCents, tt.wantOld)
			assertPtr(t, "RRPCents", got.RRPCents, tt.wantRRP)
			if got.Availability != tt.wantAvail {
				t.Errorf("Availability = %q, want %q", got.Availability, tt.wantAvail)
			}
			if tt.wantImage != "" && !strings.HasPrefix(got.ImageURL, tt.wantImage) {
				t.Errorf("ImageURL = %q, want prefix %q", got.ImageURL, tt.wantImage)
			}
			if tt.wantWarnings != (len(got.Warnings) > 0) {
				t.Errorf("Warnings = %v, want present=%v", got.Warnings, tt.wantWarnings)
			}
		})
	}
}

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		name string
		html func(t *testing.T) []byte
		want error
	}{
		{"kasada challenge", func(t *testing.T) []byte { return fixture(t, "kasada_challenge.html") }, scraper.ErrBlocked},
		{"no JSON-LD and no price block", func(t *testing.T) []byte {
			s := string(fixture(t, "product_regular.html"))
			s = reLDJSON.ReplaceAllString(s, "")
			s = rePriceBlock.ReplaceAllString(s, "")
			s = strings.ReplaceAll(s, "data-price-cents", "data-x")
			return []byte(s)
		}, scraper.ErrLayoutChanged},
		{"empty document", func(*testing.T) []byte { return []byte("<html><body></body></html>") }, scraper.ErrLayoutChanged},
		{"JSON-LD product without price", func(*testing.T) []byte {
			return []byte(`<script type="application/ld+json">{"@type":"Product","name":"X","offers":{"@type":"Offer"}}</script>`)
		}, scraper.ErrLayoutChanged},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.html(t))
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestParse_JSONLDVariants(t *testing.T) {
	wrap := func(js string) []byte {
		return []byte(`<html><head><title>T</title><script type="application/ld+json">` + js + `</script></head></html>`)
	}
	tests := []struct {
		name      string
		html      []byte
		wantPrice int64
		wantAvail string
		wantImage string
		wantName  string
	}{
		{"numeric price, offers array, image object",
			wrap(`{"@type":"Product","name":"A","image":{"@type":"ImageObject","url":"https://img/a.jpg"},"offers":[{"@type":"Offer","price":19.9,"priceCurrency":"EUR","availability":"http://schema.org/OutOfStock"}]}`),
			1990, scraper.AvailabilityOutOfStock, "https://img/a.jpg", "A"},
		{"@graph with type array and comma decimal",
			wrap(`{"@context":"https://schema.org","@graph":[{"@type":"BreadcrumbList"},{"@type":["Product"],"name":"B","image":"https://img/b.jpg","offers":{"@type":"Offer","price":"1.299,00","priceCurrency":"EUR","availability":"https://schema.org/PreOrder"}}]}`),
			129900, scraper.AvailabilityPreorder, "https://img/b.jpg", "B"},
		{"top-level array, AggregateOffer lowPrice, limited",
			wrap(`[{"@type":"Organization"},{"@type":"Product","name":"C","offers":{"@type":"AggregateOffer","lowPrice":"5","priceCurrency":"EUR","availability":"LimitedAvailability"}}]`),
			500, scraper.AvailabilityLimited, "", "C"},
		{"unknown availability, invalid JSON block ignored",
			[]byte(`<script type="application/ld+json">{broken</script><script type="application/ld+json">{"@type":"Product","name":"D","offers":{"price":"7.5","priceCurrency":"EUR","availability":"Weird"}}</script>`),
			750, scraper.AvailabilityUnknown, "", "D"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.html)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got.PriceCents != tt.wantPrice || got.Availability != tt.wantAvail || got.ImageURL != tt.wantImage || got.Name != tt.wantName {
				t.Errorf("got price=%d avail=%q image=%q name=%q; want %d %q %q %q",
					got.PriceCents, got.Availability, got.ImageURL, got.Name, tt.wantPrice, tt.wantAvail, tt.wantImage, tt.wantName)
			}
		})
	}
}

func TestParseCents(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"493.60", 49360, false},
		{"493.6", 49360, false},
		{"493", 49300, false},
		{"0.99", 99, false},
		{"493,60", 49360, false},
		{"1.299,00", 129900, false},
		{"1,299.00", 129900, false},
		{" 12.5 ", 1250, false},
		{"", 0, true},
		{"abc", 0, true},
		{"-5", 0, true},
		{"1.234", 0, true}, // ambiguous: three decimals
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseCents(tt.in)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("parseCents(%q) = %d, %v; want %d, err=%v", tt.in, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func assertPtr(t *testing.T, field string, got, want *int64) {
	t.Helper()
	switch {
	case got == nil && want == nil:
	case got == nil || want == nil:
		t.Errorf("%s = %v, want %v", field, deref(got), deref(want))
	case *got != *want:
		t.Errorf("%s = %d, want %d", field, *got, *want)
	}
}

func deref(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
