package otto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"priceradar/internal/scraper"
)

// Parse extracts an offer from an otto.de product page.
//
// Sources, in order of preference:
//  1. schema.org Product JSON-LD: name, image, sku, price, currency, availability.
//  2. The main price block (div.js_pdp_price > ofc-pricing-item-v1): its
//     attributes carry old-price (Streichpreis) and suggested-retail-price (UVP),
//     which JSON-LD lacks, and serve as price fallback.
func Parse(doc []byte) (scraper.Offer, error) {
	root, err := html.Parse(bytes.NewReader(doc))
	if err != nil {
		return scraper.Offer{}, fmt.Errorf("parse html: %v: %w", err, scraper.ErrLayoutChanged)
	}
	p := extract(root)

	// Real product pages also load the Kasada SDK, so "KPSDK" alone is not
	// enough: the challenge page is an empty shell without title or product data.
	if p.kasada && strings.TrimSpace(p.title) == "" && len(p.ldBlocks) == 0 && p.block == nil {
		return scraper.Offer{}, fmt.Errorf("kasada challenge page: %w", scraper.ErrBlocked)
	}

	var offer scraper.Offer
	ld, ldErr := productFromLD(p.ldBlocks)
	haveLD := ldErr == nil
	if haveLD {
		offer = ld
	}
	if offer.Name == "" {
		offer.Name = strings.TrimSpace(p.title)
	}

	if b := p.block; b != nil {
		offer.OldPriceCents = attrCents(b.pricing, "old-price")
		offer.RRPCents = attrCents(b.pricing, "suggested-retail-price")
		blockPrice := attrCents(b.pricing, "retail-price")
		if blockPrice == nil {
			blockPrice = b.tagCents
		}
		switch {
		case !haveLD && blockPrice != nil:
			offer.PriceCents = *blockPrice
			offer.Warnings = append(offer.Warnings, fmt.Sprintf("JSON-LD unusable (%v), price taken from price block", ldErr))
		case haveLD && blockPrice != nil && *blockPrice != offer.PriceCents:
			offer.Warnings = append(offer.Warnings, fmt.Sprintf("price mismatch: JSON-LD %d vs price block %d", offer.PriceCents, *blockPrice))
		}
		if offer.Availability == "" || offer.Availability == scraper.AvailabilityUnknown {
			if b.soldOut == "true" {
				offer.Availability = scraper.AvailabilityOutOfStock
			} else if !haveLD && b.soldOut == "false" {
				offer.Availability = scraper.AvailabilityInStock
			}
		}
	}

	if offer.PriceCents <= 0 {
		return scraper.Offer{}, fmt.Errorf("no price in JSON-LD (%v) and no price block: %w", ldErr, scraper.ErrLayoutChanged)
	}
	if offer.Currency == "" {
		offer.Currency = "EUR" // otto.de only sells in EUR
	}
	if offer.Availability == "" {
		offer.Availability = scraper.AvailabilityUnknown
	}
	return offer, nil
}

type priceBlock struct {
	soldOut  string
	pricing  *html.Node // ofc-pricing-item-v1
	tagCents *int64     // data-price-cents of .js_pdp_price__tag
}

type parsed struct {
	ldBlocks [][]byte
	block    *priceBlock
	title    string
	kasada   bool
}

func extract(root *html.Node) parsed {
	var p parsed
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch {
			case n.Data == "script":
				text := textOf(n)
				if strings.EqualFold(attr(n, "type"), "application/ld+json") {
					p.ldBlocks = append(p.ldBlocks, []byte(text))
				} else if strings.Contains(text, "KPSDK") {
					p.kasada = true
				}
				return
			case n.Data == "title" && p.title == "":
				p.title = textOf(n)
			case p.block == nil && hasClass(n, "js_pdp_price"):
				p.block = readPriceBlock(n)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return p
}

func readPriceBlock(n *html.Node) *priceBlock {
	b := &priceBlock{soldOut: attr(n, "data-is-sold-out")}
	var walk func(*html.Node)
	walk = func(c *html.Node) {
		if c.Type == html.ElementNode {
			if b.pricing == nil && c.Data == "ofc-pricing-item-v1" {
				b.pricing = c
			}
			if b.tagCents == nil && hasClass(c, "js_pdp_price__tag") {
				b.tagCents = attrCents(c, "data-price-cents")
			}
		}
		for x := c.FirstChild; x != nil; x = x.NextSibling {
			walk(x)
		}
	}
	walk(n)
	if b.pricing == nil && b.tagCents == nil {
		return nil
	}
	return b
}

// ---- JSON-LD ----

type ldOffer struct {
	Type         any             `json:"@type"`
	Price        json.RawMessage `json:"price"`
	LowPrice     json.RawMessage `json:"lowPrice"`
	Currency     string          `json:"priceCurrency"`
	Availability string          `json:"availability"`
}

type ldProduct struct {
	Name   string          `json:"name"`
	SKU    string          `json:"sku"`
	Image  json.RawMessage `json:"image"`
	Offers json.RawMessage `json:"offers"`
}

func productFromLD(blocks [][]byte) (scraper.Offer, error) {
	if len(blocks) == 0 {
		return scraper.Offer{}, errors.New("no JSON-LD")
	}
	var lastErr error = errors.New("no Product in JSON-LD")
	for _, b := range blocks {
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			continue
		}
		for _, obj := range collectProducts(v) {
			raw, _ := json.Marshal(obj)
			var p ldProduct
			if err := json.Unmarshal(raw, &p); err != nil {
				continue
			}
			o, err := offerFromLD(p)
			if err != nil {
				lastErr = err
				continue
			}
			return o, nil
		}
	}
	return scraper.Offer{}, lastErr
}

// collectProducts finds objects with @type Product in arrays and @graph.
func collectProducts(v any) []map[string]any {
	var out []map[string]any
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			out = append(out, collectProducts(e)...)
		}
	case map[string]any:
		if typeIs(t["@type"], "Product") {
			out = append(out, t)
		}
		if g, ok := t["@graph"]; ok {
			out = append(out, collectProducts(g)...)
		}
	}
	return out
}

func typeIs(v any, want string) bool {
	switch t := v.(type) {
	case string:
		return t == want
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

func offerFromLD(p ldProduct) (scraper.Offer, error) {
	var offers []ldOffer
	if len(p.Offers) > 0 {
		var one ldOffer
		if err := json.Unmarshal(p.Offers, &one); err == nil {
			offers = []ldOffer{one}
		} else {
			_ = json.Unmarshal(p.Offers, &offers)
		}
	}
	for _, o := range offers {
		price := rawNumber(o.Price)
		if price == "" {
			price = rawNumber(o.LowPrice)
		}
		cents, err := parseCents(price)
		if err != nil || cents <= 0 {
			continue
		}
		return scraper.Offer{
			Name:         strings.TrimSpace(p.Name),
			SKU:          p.SKU,
			ImageURL:     firstImage(p.Image),
			Currency:     strings.ToUpper(o.Currency),
			PriceCents:   cents,
			Availability: mapAvailability(o.Availability),
		}, nil
	}
	return scraper.Offer{}, errors.New("Product JSON-LD has no usable offer price")
}

func rawNumber(r json.RawMessage) string {
	if len(r) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(r, &s); err == nil {
		return s
	}
	var n json.Number
	if err := json.Unmarshal(r, &n); err == nil {
		return n.String()
	}
	return ""
}

func firstImage(r json.RawMessage) string {
	if len(r) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(r, &s) == nil {
		return s
	}
	var obj struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(r, &obj) == nil && obj.URL != "" {
		return obj.URL
	}
	var arr []json.RawMessage
	if json.Unmarshal(r, &arr) == nil {
		for _, e := range arr {
			if u := firstImage(e); u != "" {
				return u
			}
		}
	}
	return ""
}

func mapAvailability(s string) string {
	s = s[strings.LastIndexAny(s, "/")+1:]
	switch strings.ToLower(s) {
	case "instock", "onlineonly", "instoreonly":
		return scraper.AvailabilityInStock
	case "outofstock", "soldout", "discontinued":
		return scraper.AvailabilityOutOfStock
	case "preorder", "presale", "backorder":
		return scraper.AvailabilityPreorder
	case "limitedavailability":
		return scraper.AvailabilityLimited
	default:
		return scraper.AvailabilityUnknown
	}
}

// ---- helpers ----

// parseCents converts a decimal price string ("493.60", "1.299,00", "19.9")
// into cents without going through floating point.
func parseCents(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty price")
	}
	lastDot, lastComma := strings.LastIndex(s, "."), strings.LastIndex(s, ",")
	sep := max(lastDot, lastComma)
	intPart, frac := s, ""
	if sep >= 0 {
		intPart, frac = s[:sep], s[sep+1:]
		if lastDot >= 0 && lastComma >= 0 {
			// both present: the other one is the thousands separator
			intPart = strings.NewReplacer(".", "", ",", "").Replace(intPart)
		} else if len(frac) == 3 {
			return 0, fmt.Errorf("ambiguous price %q", s)
		}
	}
	if len(frac) > 2 {
		return 0, fmt.Errorf("too many decimals in %q", s)
	}
	if intPart == "" || !allDigits(intPart) || !allDigits(frac) {
		return 0, fmt.Errorf("invalid price %q", s)
	}
	for len(frac) < 2 {
		frac += "0"
	}
	return strconv.ParseInt(intPart+frac, 10, 64)
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func attrCents(n *html.Node, key string) *int64 {
	if n == nil {
		return nil
	}
	v := attr(n, key)
	if v == "" {
		return nil
	}
	c, err := strconv.ParseInt(v, 10, 64)
	if err != nil || c <= 0 {
		return nil
	}
	return &c
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

func textOf(n *html.Node) string {
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			sb.WriteString(c.Data)
		}
	}
	return sb.String()
}
