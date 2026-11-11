package otto

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"priceradar/internal/scraper"
)

type fakeFetcher struct {
	page scraper.Page
	err  error
	got  string
}

func (f *fakeFetcher) Get(_ context.Context, u string) (scraper.Page, error) {
	f.got = u
	return f.page, f.err
}

func TestScraper_Fetch(t *testing.T) {
	regular := fixture(t, "product_regular.html")
	tests := []struct {
		name      string
		page      scraper.Page
		fetchErr  error
		wantErr   error
		wantPrice int64
	}{
		{"ok", scraper.Page{Status: 200, HTML: regular}, nil, nil, 49360},
		{"404", scraper.Page{Status: 404, HTML: []byte("nope")}, nil, scraper.ErrNotFound, 0},
		{"410", scraper.Page{Status: 410}, nil, scraper.ErrNotFound, 0},
		{"403", scraper.Page{Status: 403}, nil, scraper.ErrBlocked, 0},
		{"429", scraper.Page{Status: 429}, nil, scraper.ErrBlocked, 0},
		{"400 kasada", scraper.Page{Status: 400, HTML: fixture(t, "kasada_challenge.html")}, nil, scraper.ErrBlocked, 0},
		{"200 kasada", scraper.Page{Status: 200, HTML: fixture(t, "kasada_challenge.html")}, nil, scraper.ErrBlocked, 0},
		{"503", scraper.Page{Status: 503}, nil, scraper.ErrTransient, 0},
		{"transport error", scraper.Page{}, errors.Join(errors.New("boom"), scraper.ErrTransient), scraper.ErrTransient, 0},
		{"other 4xx", scraper.Page{Status: 418}, nil, scraper.ErrLayoutChanged, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeFetcher{page: tt.page, err: tt.fetchErr}
			got, err := New(f).Fetch(context.Background(), "https://www.otto.de/p/x-CS1/")
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got.PriceCents != tt.wantPrice {
				t.Errorf("price = %d, want %d", got.PriceCents, tt.wantPrice)
			}
		})
	}
}

func TestScraper_SupportsAndNormalize(t *testing.T) {
	s := New(nil)
	tests := []struct {
		in       string
		supports bool
		norm     string
	}{
		{"https://www.otto.de/p/bambu-lab-3d-drucker-a1-combo-CS00EO0AU/", true, "https://www.otto.de/p/bambu-lab-3d-drucker-a1-combo-CS00EO0AU/"},
		{"https://otto.de/p/x-CS1/?variationId=S8Z7&utm_source=x&wt_mc=y#reviews", true, "https://www.otto.de/p/x-CS1/?variationId=S8Z7"},
		{"http://www.otto.de/p/x-CS1/?qid=abc", true, "https://www.otto.de/p/x-CS1/"},
		{"https://WWW.OTTO.DE/p/x-CS1/", true, "https://www.otto.de/p/x-CS1/"},
		{"https://www.otto.de/technik/drucker/", false, ""},
		{"https://www.otto.de/", false, ""},
		{"https://www.amazon.de/p/x", false, ""},
		{"https://evilotto.de/p/x", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			u, _ := url.Parse(tt.in)
			if got := s.Supports(u); got != tt.supports {
				t.Fatalf("Supports = %v, want %v", got, tt.supports)
			}
			if tt.supports {
				if got := s.NormalizeURL(u); got != tt.norm {
					t.Errorf("NormalizeURL = %q, want %q", got, tt.norm)
				}
			}
		})
	}
	if s.Name() != "otto" {
		t.Errorf("Name = %q", s.Name())
	}
}
