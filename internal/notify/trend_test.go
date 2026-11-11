package notify

import (
	"math"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// pts builds points spaced one day apart.
func pts(prices ...int64) []Point {
	out := make([]Point, len(prices))
	for i, p := range prices {
		out[i] = Point{At: t0.Add(time.Duration(i) * 24 * time.Hour), Cents: p}
	}
	return out
}

func TestFitLine(t *testing.T) {
	tests := []struct {
		name       string
		in         []Point
		ok         bool
		start, end float64
	}{
		{"empty", nil, false, 0, 0},
		{"single point", pts(100), false, 0, 0},
		{"same timestamp", []Point{{t0, 100}, {t0, 200}}, false, 0, 0},
		{"exact line", pts(1000, 900, 800, 700), true, 1000, 700},
		{"flat", pts(500, 500, 500), true, 500, 500},
		// symmetric noise around a rising line: 100,120,110,130 → slope 8/day, mean 115
		{"noisy rise", pts(100, 120, 110, 130), true, 103, 127},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, ok := fitLine(tt.in)
			if ok != tt.ok {
				t.Fatalf("ok = %v", ok)
			}
			if !ok {
				return
			}
			if math.Abs(l.Start-tt.start) > 1e-6 || math.Abs(l.End-tt.end) > 1e-6 {
				t.Fatalf("line = %+v, want start %v end %v", l, tt.start, tt.end)
			}
		})
	}
}

func TestDetectTrend(t *testing.T) {
	irregular := []Point{
		{t0, 1000},
		{t0.Add(time.Hour), 1000},
		{t0.Add(5 * 24 * time.Hour), 880},
		{t0.Add(6 * 24 * time.Hour), 870},
	}
	tests := []struct {
		name    string
		in      []Point
		pct     float64
		wantOK  bool
		wantDir int
	}{
		{"too few points", pts(1000, 800, 600), 8, false, 0},
		{"only one distinct price", pts(1000, 1000, 1000, 1000), 8, false, 0},
		{"steady fall", pts(1000, 970, 940, 910, 880), 8, true, -1},
		{"steady rise", pts(1000, 1030, 1060, 1090), 8, true, 1},
		{"below threshold", pts(1000, 990, 980, 970), 8, false, 0},
		{"single spike is no trend", pts(1000, 1000, 1500, 1000, 1000), 8, false, 0},
		{"irregular spacing uses time", irregular, 8, true, -1},
		{"zero start value", pts(0, 0, 0, 10), 8, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr, ok := DetectTrend(tt.in, tt.pct)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v (%+v)", ok, tr)
			}
			if ok && tr.Direction != tt.wantDir {
				t.Fatalf("direction = %d, want %d", tr.Direction, tt.wantDir)
			}
			if ok && tr.Points != len(tt.in) {
				t.Fatalf("points = %d", tr.Points)
			}
		})
	}

	// exact threshold: line 1000 → 920 is −8 % and must fire (≥)
	exact := []Point{{t0, 1000}, {t0.Add(24 * time.Hour), 980}, {t0.Add(48 * time.Hour), 960}, {t0.Add(72 * time.Hour), 940}, {t0.Add(96 * time.Hour), 920}}
	if tr, ok := DetectTrend(exact, 8); !ok || math.Abs(tr.Change+0.08) > 1e-9 {
		t.Fatalf("exact −8 %%: %+v %v", tr, ok)
	}
	if _, ok := DetectTrend(exact, 8.01); ok {
		t.Fatal("−8 % must not pass a 8.01 % threshold")
	}
}
