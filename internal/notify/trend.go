package notify

import (
	"math"
	"time"
)

// Point is one successful price observation.
type Point struct {
	At    time.Time
	Cents int64
}

// line is a least-squares fit evaluated at the first and last point's time.
type line struct {
	Slope      float64 // cents per second
	Start, End float64 // fitted values at the first / last timestamp
}

// fitLine fits price = a + b*t by ordinary least squares. It needs at least
// two points with different timestamps.
func fitLine(pts []Point) (line, bool) {
	n := float64(len(pts))
	if len(pts) < 2 {
		return line{}, false
	}
	// x in seconds relative to the first point keeps the numbers small.
	base := pts[0].At
	var sx, sy float64
	for _, p := range pts {
		sx += p.At.Sub(base).Seconds()
		sy += float64(p.Cents)
	}
	mx, my := sx/n, sy/n
	var sxx, sxy float64
	for _, p := range pts {
		dx := p.At.Sub(base).Seconds() - mx
		sxx += dx * dx
		sxy += dx * (float64(p.Cents) - my)
	}
	if sxx == 0 {
		return line{}, false
	}
	b := sxy / sxx
	a := my - b*mx
	first, last := pts[0].At.Sub(base).Seconds(), pts[len(pts)-1].At.Sub(base).Seconds()
	return line{Slope: b, Start: a + b*first, End: a + b*last}, true
}

// Trend is a detected price trend.
type Trend struct {
	Direction  int     // -1 falling, +1 rising
	Change     float64 // relative change of the regression line, e.g. -0.095
	Start, End float64 // regression line values (cents) at the window's first and last point
	Points     int
}

// Trend detection thresholds (see README "Notifications").
const (
	trendMinPoints   = 4
	trendMinDistinct = 2
)

// DetectTrend reports a trend over pts (ascending by time) if there are at
// least 4 points with at least 2 distinct prices, and the least-squares line
// changes by at least minPct percent between the first and the last point.
// The direction is the sign of the slope.
func DetectTrend(pts []Point, minPct float64) (Trend, bool) {
	if len(pts) < trendMinPoints {
		return Trend{}, false
	}
	distinct := map[int64]struct{}{}
	for _, p := range pts {
		distinct[p.Cents] = struct{}{}
	}
	if len(distinct) < trendMinDistinct {
		return Trend{}, false
	}
	l, ok := fitLine(pts)
	if !ok || l.Start <= 0 || l.Slope == 0 {
		return Trend{}, false
	}
	change := (l.End - l.Start) / l.Start
	// small epsilon so that an exact threshold is not lost to float rounding
	if math.Abs(change)*100 < minPct-1e-9 {
		return Trend{}, false
	}
	dir := 1
	if l.Slope < 0 {
		dir = -1
	}
	return Trend{Direction: dir, Change: change, Start: l.Start, End: l.End, Points: len(pts)}, true
}
