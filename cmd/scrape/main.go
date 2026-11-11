// Command scrape fetches one product URL and prints the parsed offer.
//
//	go run ./cmd/scrape https://www.otto.de/p/...
//	go run ./cmd/scrape -json https://www.otto.de/p/...
//
// It needs the fetcher sidecar (see fetcher/) at $PR_FETCHER_URL
// (default http://localhost:8000).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"priceradar/internal/scraper"
	"priceradar/internal/scraper/otto"
)

func main() {
	asJSON := flag.Bool("json", false, "print the offer as JSON")
	fetcherURL := flag.String("fetcher", envOr("PR_FETCHER_URL", "http://localhost:8000"), "fetcher sidecar base URL")
	ua := flag.String("ua", envOr("PR_USER_AGENT", "PriceRadar/0.1 (+self-hosted)"), "user agent for robots.txt")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: scrape [-json] <product-url>")
		os.Exit(2)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	sidecar := &scraper.SidecarFetcher{BaseURL: *fetcherURL, Timeout: 60 * time.Second, WaitSelector: otto.WaitSelector}
	fetcher := scraper.NewPolite(sidecar, scraper.NewRobots(*ua, nil, time.Hour), scraper.PoliteConfig{})
	reg := scraper.NewRegistry(otto.New(fetcher))

	s, normURL, err := reg.Resolve(flag.Arg(0))
	if err != nil {
		log.Error("resolve", "err", err)
		os.Exit(1)
	}
	start := time.Now()
	offer, err := s.Fetch(ctx, normURL)
	if err != nil {
		log.Error("scrape failed", "url", normURL, "kind", scraper.KindOf(err), "err", err, "took", time.Since(start).Round(time.Millisecond))
		os.Exit(1)
	}
	for _, w := range offer.Warnings {
		log.Warn("parser warning", "msg", w)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(offer)
		return
	}
	fmt.Printf("%s\n  Preis:          %s %s\n", offer.Name, cents(offer.PriceCents), offer.Currency)
	if offer.OldPriceCents != nil {
		fmt.Printf("  Streichpreis:   %s %s\n", cents(*offer.OldPriceCents), offer.Currency)
	}
	if offer.RRPCents != nil {
		fmt.Printf("  UVP:            %s %s\n", cents(*offer.RRPCents), offer.Currency)
	}
	fmt.Printf("  Verfügbarkeit:  %s\n  SKU:            %s\n  Bild:           %s\n  Dauer:          %s\n",
		offer.Availability, offer.SKU, offer.ImageURL, time.Since(start).Round(time.Millisecond))
}

func cents(c int64) string { return fmt.Sprintf("%d,%02d", c/100, c%100) }

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
