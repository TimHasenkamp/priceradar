package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

var t0 = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

func newTestStore(t *testing.T) (*Store, *fakeClock) {
	t.Helper()
	clk := &fakeClock{t: t0}
	s, err := Open(context.Background(), ":memory:", WithClock(clk.now))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, clk
}

func ptr[T any](v T) *T { return &v }

func mustCreate(t *testing.T, s *Store, url string) Product {
	t.Helper()
	p, err := s.CreateProduct(context.Background(), NewProduct{Shop: "otto", URL: url, Name: "N " + url})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func okCheck(pid int64, at time.Time, price int64) PriceCheck {
	return PriceCheck{ProductID: pid, CheckedAt: at, Success: true, PriceCents: ptr(price)}
}

func failCheck(pid int64) PriceCheck {
	return PriceCheck{ProductID: pid, ErrorKind: ptr("blocked"), ErrorMessage: ptr("403"), DurationMS: 12}
}

func TestPing(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCreateAndGetProduct(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	p, err := s.CreateProduct(ctx, NewProduct{
		Shop: "otto", URL: "https://x/1", SKU: "S1", Name: "Name", ImageURL: "img",
		Currency: "USD", TargetPriceCents: ptr(int64(4500)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == 0 || !p.CreatedAt.Equal(t0) || !p.UpdatedAt.Equal(t0) || p.CreatedAt.Location() != time.UTC {
		t.Errorf("bad timestamps/id: %+v", p)
	}
	got, err := s.GetProduct(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Shop != "otto" || got.SKU != "S1" || got.Currency != "USD" || got.ImageURL != "img" ||
		got.TargetPriceCents == nil || *got.TargetPriceCents != 4500 || got.Paused ||
		got.CustomName != nil || got.LastCheckedAt != nil || got.LastSuccessAt != nil ||
		got.LastError != nil || got.LastErrorKind != nil || got.ConsecutiveFailures != 0 {
		t.Errorf("unexpected product: %+v", got)
	}
}

func TestCreateProductDefaultCurrency(t *testing.T) {
	s, _ := newTestStore(t)
	p := mustCreate(t, s, "u")
	if p.Currency != "EUR" {
		t.Errorf("currency = %q, want EUR", p.Currency)
	}
}

func TestCreateDuplicateAndNotFound(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	mustCreate(t, s, "dup")
	if _, err := s.CreateProduct(ctx, NewProduct{Shop: "otto", URL: "dup", Name: "x"}); !errors.Is(err, ErrDuplicate) {
		t.Errorf("err = %v, want ErrDuplicate", err)
	}
	if _, err := s.GetProduct(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetProduct err = %v", err)
	}
	if _, err := s.GetProductWithStats(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetProductWithStats err = %v", err)
	}
}

func TestUpdateProductPatch(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name  string
		setup ProductPatch
		patch ProductPatch
		check func(t *testing.T, p Product)
	}{
		{"empty patch changes nothing", ProductPatch{CustomName: ptr("c"), TargetPriceCents: ptr(int64(5))}, ProductPatch{},
			func(t *testing.T, p Product) {
				if p.CustomName == nil || *p.CustomName != "c" || p.TargetPriceCents == nil || *p.TargetPriceCents != 5 || p.Paused {
					t.Errorf("%+v", p)
				}
			}},
		{"set values", ProductPatch{}, ProductPatch{CustomName: ptr("x"), Paused: ptr(true), TargetPriceCents: ptr(int64(9))},
			func(t *testing.T, p Product) {
				if *p.CustomName != "x" || !p.Paused || *p.TargetPriceCents != 9 {
					t.Errorf("%+v", p)
				}
			}},
		{"clear flags", ProductPatch{CustomName: ptr("c"), TargetPriceCents: ptr(int64(5))}, ProductPatch{ClearCustomName: true, ClearTargetPrice: true},
			func(t *testing.T, p Product) {
				if p.CustomName != nil || p.TargetPriceCents != nil {
					t.Errorf("%+v", p)
				}
			}},
		{"unpause", ProductPatch{Paused: ptr(true)}, ProductPatch{Paused: ptr(false)},
			func(t *testing.T, p Product) {
				if p.Paused {
					t.Errorf("%+v", p)
				}
			}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, clk := newTestStore(t)
			p := mustCreate(t, s, "u")
			if _, err := s.UpdateProduct(ctx, p.ID, tc.setup); err != nil {
				t.Fatal(err)
			}
			clk.advance(time.Hour)
			got, err := s.UpdateProduct(ctx, p.ID, tc.patch)
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, got)
			if !got.UpdatedAt.Equal(t0.Add(time.Hour)) {
				t.Errorf("updated_at = %v", got.UpdatedAt)
			}
			if !got.CreatedAt.Equal(t0) {
				t.Errorf("created_at changed: %v", got.CreatedAt)
			}
		})
	}
	s, _ := newTestStore(t)
	if _, err := s.UpdateProduct(ctx, 42, ProductPatch{Paused: ptr(true)}); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestUpdateProductMeta(t *testing.T) {
	ctx := context.Background()
	s, clk := newTestStore(t)
	p, _ := s.CreateProduct(ctx, NewProduct{Shop: "otto", URL: "u", Name: "Old", SKU: "S", ImageURL: "i", Currency: "EUR"})
	clk.advance(time.Minute)
	if err := s.UpdateProductMeta(ctx, p.ID, ProductMeta{Name: "New", Currency: "USD"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetProduct(ctx, p.ID)
	if got.Name != "New" || got.Currency != "USD" || got.SKU != "S" || got.ImageURL != "i" {
		t.Errorf("%+v", got)
	}
	if err := s.UpdateProductMeta(ctx, p.ID, ProductMeta{SKU: "S2", ImageURL: "i2"}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetProduct(ctx, p.ID)
	if got.Name != "New" || got.SKU != "S2" || got.ImageURL != "i2" {
		t.Errorf("%+v", got)
	}
	if err := s.UpdateProductMeta(ctx, 999, ProductMeta{Name: "x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestDeleteCascades(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	p := mustCreate(t, s, "u")
	if _, err := s.RecordCheck(ctx, okCheck(p.ID, t0, 100)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProduct(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM price_checks`).Scan(&n); err != nil || n != 0 {
		t.Errorf("checks left = %d err=%v", n, err)
	}
	if err := s.DeleteProduct(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestRecordCheck(t *testing.T) {
	ctx := context.Background()
	s, clk := newTestStore(t)
	p := mustCreate(t, s, "u")

	// two failures; zero CheckedAt defaults to now
	clk.advance(time.Minute)
	c, err := s.RecordCheck(ctx, failCheck(p.ID))
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == 0 || !c.CheckedAt.Equal(clk.t) || c.Success {
		t.Errorf("%+v", c)
	}
	clk.advance(time.Minute)
	if _, err := s.RecordCheck(ctx, failCheck(p.ID)); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetProduct(ctx, p.ID)
	if got.ConsecutiveFailures != 2 || got.LastErrorKind == nil || *got.LastErrorKind != "blocked" ||
		got.LastError == nil || *got.LastError != "403" || got.LastCheckedAt == nil ||
		!got.LastCheckedAt.Equal(clk.t) || got.LastSuccessAt != nil {
		t.Errorf("after failures: %+v", got)
	}

	// success resets
	clk.advance(time.Minute)
	ok := okCheck(p.ID, clk.t, 1000)
	ok.OldPriceCents, ok.RRPCents, ok.Availability, ok.DurationMS = ptr(int64(1200)), ptr(int64(1500)), ptr("in_stock"), 5
	rec, err := s.RecordCheck(ctx, ok)
	if err != nil {
		t.Fatal(err)
	}
	if *rec.OldPriceCents != 1200 || *rec.RRPCents != 1500 || *rec.Availability != "in_stock" || rec.DurationMS != 5 {
		t.Errorf("%+v", rec)
	}
	got, _ = s.GetProduct(ctx, p.ID)
	if got.ConsecutiveFailures != 0 || got.LastError != nil || got.LastErrorKind != nil ||
		got.LastSuccessAt == nil || !got.LastSuccessAt.Equal(clk.t) || !got.LastCheckedAt.Equal(clk.t) {
		t.Errorf("after success: %+v", got)
	}

	// failure after success keeps last_success_at
	successAt := clk.t
	clk.advance(time.Minute)
	if _, err := s.RecordCheck(ctx, failCheck(p.ID)); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetProduct(ctx, p.ID)
	if got.ConsecutiveFailures != 1 || !got.LastSuccessAt.Equal(successAt) {
		t.Errorf("%+v", got)
	}

	if _, err := s.RecordCheck(ctx, failCheck(999)); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM price_checks WHERE product_id=999`).Scan(&n)
	if n != 0 {
		t.Error("orphan check stored")
	}
}

func TestListPricesRange(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	p := mustCreate(t, s, "a")
	other := mustCreate(t, s, "b")
	for _, h := range []int{3, 1, 2, 4} { // out of order on purpose
		if _, err := s.RecordCheck(ctx, okCheck(p.ID, t0.Add(time.Duration(h)*time.Hour), int64(h*100))); err != nil {
			t.Fatal(err)
		}
	}
	f := failCheck(p.ID)
	f.CheckedAt = t0.Add(90 * time.Minute)
	s.RecordCheck(ctx, f)
	s.RecordCheck(ctx, okCheck(other.ID, t0.Add(time.Hour), 7))

	h := func(n int) *time.Time { v := t0.Add(time.Duration(n) * time.Hour); return &v }
	tests := []struct {
		name     string
		from, to *time.Time
		want     []int64
	}{
		{"all", nil, nil, []int64{100, 200, 300, 400}},
		{"from inclusive", h(2), nil, []int64{200, 300, 400}},
		{"to inclusive", nil, h(2), []int64{100, 200}},
		{"both", h(2), h(3), []int64{200, 300}},
		{"empty", h(5), nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.ListPrices(ctx, p.ID, tc.from, tc.to)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tc.want))
			}
			for i, w := range tc.want {
				if !got[i].Success || *got[i].PriceCents != w {
					t.Errorf("[%d] = %d, want %d", i, *got[i].PriceCents, w)
				}
			}
		})
	}
}

func TestListChecks(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	p := mustCreate(t, s, "a")
	for i := 1; i <= 5; i++ {
		at := t0.Add(time.Duration(i) * time.Minute)
		c := okCheck(p.ID, at, int64(i))
		if i%2 == 0 {
			c = PriceCheck{ProductID: p.ID, CheckedAt: at, ErrorKind: ptr("transient"), ErrorMessage: ptr("e")}
		}
		if _, err := s.RecordCheck(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.ListChecks(ctx, p.ID, 50)
	if err != nil || len(all) != 5 {
		t.Fatalf("len=%d err=%v", len(all), err)
	}
	for i := 1; i < len(all); i++ {
		if !all[i-1].CheckedAt.After(all[i].CheckedAt) {
			t.Error("not newest first")
		}
	}
	if !all[0].Success {
		t.Errorf("newest (i=5) should be success: %+v", all[0])
	}
	if all[1].Success || all[1].ErrorKind == nil || *all[1].ErrorKind != "transient" {
		t.Errorf("%+v", all[1])
	}
	two, _ := s.ListChecks(ctx, p.ID, 2)
	if len(two) != 2 || !two[0].CheckedAt.Equal(all[0].CheckedAt) {
		t.Errorf("limit failed: %+v", two)
	}
	none, err := s.ListChecks(ctx, 999, 10)
	if err != nil || len(none) != 0 {
		t.Errorf("none=%v err=%v", none, err)
	}
}

func TestListActiveProducts(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	a := mustCreate(t, s, "a")
	b := mustCreate(t, s, "b")
	c := mustCreate(t, s, "c")
	d := mustCreate(t, s, "d")
	s.UpdateProduct(ctx, d.ID, ProductPatch{Paused: ptr(true)})
	s.RecordCheck(ctx, okCheck(a.ID, t0.Add(2*time.Hour), 1))
	s.RecordCheck(ctx, okCheck(b.ID, t0.Add(time.Hour), 1))
	got, err := s.ListActiveProducts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != c.ID || got[1].ID != b.ID || got[2].ID != a.ID {
		var ids []int64
		for _, p := range got {
			ids = append(ids, p.ID)
		}
		t.Errorf("order = %v, want [%d %d %d]", ids, c.ID, b.ID, a.ID)
	}
}

func TestListProductsOrderAndEmpty(t *testing.T) {
	ctx := context.Background()
	s, clk := newTestStore(t)
	got, err := s.ListProducts(ctx)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
	a := mustCreate(t, s, "a")
	b := mustCreate(t, s, "b") // same created_at: id desc tiebreak
	clk.advance(time.Second)
	c := mustCreate(t, s, "c")
	got, _ = s.ListProducts(ctx)
	if len(got) != 3 || got[0].ID != c.ID || got[1].ID != b.ID || got[2].ID != a.ID {
		t.Fatalf("order wrong: %+v", got)
	}
	if got[0].Stats.CurrentCents != nil {
		t.Error("stats should be nil")
	}
}

func TestStats(t *testing.T) {
	day := 24 * time.Hour
	type chk struct {
		ago   time.Duration // before now
		price int64         // 0 = failed check
	}
	i := func(v int64) *int64 { return &v }
	tests := []struct {
		name                     string
		checks                   []chk
		cur, prev, avg, min, max *int64
	}{
		{"no checks", nil, nil, nil, nil, nil, nil},
		{"only failures", []chk{{day, 0}, {2 * day, 0}}, nil, nil, nil, nil, nil},
		{"never changed", []chk{{3 * day, 100}, {2 * day, 100}, {day, 100}}, i(100), nil, i(100), i(100), i(100)},
		{"A B B", []chk{{3 * day, 100}, {2 * day, 200}, {day, 200}}, i(200), i(100), i(167), i(100), i(200)},
		{"A B A", []chk{{3 * day, 100}, {2 * day, 200}, {day, 100}}, i(100), i(200), i(133), i(100), i(200)},
		{"failure ignored", []chk{{3 * day, 100}, {2 * day, 200}, {day, 0}}, i(200), i(100), i(150), i(100), i(200)},
		{"avg30 excludes old, min/max do not", []chk{{40 * day, 1000}, {31 * day, 1000}, {2 * day, 200}, {day, 100}},
			i(100), i(200), i(150), i(100), i(1000)},
		{"only old checks: avg nil", []chk{{40 * day, 500}}, i(500), nil, nil, i(500), i(500)},
		{"exactly 30 days is included", []chk{{30 * day, 100}, {day, 200}}, i(200), i(100), i(150), i(100), i(200)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s, clk := newTestStore(t)
			p := mustCreate(t, s, "u")
			for _, c := range tc.checks {
				pc := PriceCheck{ProductID: p.ID, CheckedAt: clk.t.Add(-c.ago)}
				if c.price == 0 {
					pc.ErrorKind = ptr("blocked")
				} else {
					pc.Success, pc.PriceCents = true, ptr(c.price)
				}
				if _, err := s.RecordCheck(ctx, pc); err != nil {
					t.Fatal(err)
				}
			}
			eq := func(name string, got, want *int64) {
				t.Helper()
				if (got == nil) != (want == nil) || (got != nil && *got != *want) {
					t.Errorf("%s = %v, want %v", name, deref(got), deref(want))
				}
			}
			check := func(st ProductStats) {
				t.Helper()
				eq("current", st.CurrentCents, tc.cur)
				eq("previous", st.PreviousCents, tc.prev)
				eq("avg30", st.Avg30Cents, tc.avg)
				eq("min", st.MinCents, tc.min)
				eq("max", st.MaxCents, tc.max)
			}
			one, err := s.GetProductWithStats(ctx, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			check(one.Stats)
			all, err := s.ListProducts(ctx)
			if err != nil || len(all) != 1 {
				t.Fatal(err)
			}
			check(all[0].Stats)
		})
	}
}

func deref(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestStatsLatestFieldsAndIsolation(t *testing.T) {
	ctx := context.Background()
	s, clk := newTestStore(t)
	p := mustCreate(t, s, "a")
	q := mustCreate(t, s, "b")
	c1 := okCheck(p.ID, clk.t.Add(-2*time.Hour), 100)
	c1.OldPriceCents, c1.RRPCents, c1.Availability = ptr(int64(999)), ptr(int64(888)), ptr("limited")
	c2 := okCheck(p.ID, clk.t.Add(-time.Hour), 90)
	c2.OldPriceCents, c2.RRPCents, c2.Availability = ptr(int64(300)), ptr(int64(400)), ptr("in_stock")
	for _, c := range []PriceCheck{c1, c2, okCheck(q.ID, clk.t, 5000)} {
		if _, err := s.RecordCheck(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetProductWithStats(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	st := got.Stats
	if *st.OldPriceCents != 300 || *st.RRPCents != 400 || *st.Availability != "in_stock" || *st.CurrentCents != 90 || *st.PreviousCents != 100 {
		t.Errorf("%+v", st)
	}
	if *st.MaxCents != 100 {
		t.Errorf("stats leaked across products: max=%d", *st.MaxCents)
	}
}

func TestOpenErrors(t *testing.T) {
	if _, err := Open(context.Background(), t.TempDir()+"/no/such/dir/x.db"); err == nil {
		t.Error("expected error for unreachable path")
	}
}

func TestCanceledContext(t *testing.T) {
	s, _ := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ListProducts(ctx); err == nil {
		t.Error("ListProducts: expected error")
	}
	if _, err := s.GetProduct(ctx, 1); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("GetProduct: err = %v", err)
	}
	if _, err := s.ListActiveProducts(ctx); err == nil {
		t.Error("ListActiveProducts: expected error")
	}
	if _, err := s.ListPrices(ctx, 1, nil, nil); err == nil {
		t.Error("ListPrices: expected error")
	}
	if _, err := s.ListChecks(ctx, 1, 1); err == nil {
		t.Error("ListChecks: expected error")
	}
	if _, err := s.CreateProduct(ctx, NewProduct{URL: "x"}); err == nil {
		t.Error("CreateProduct: expected error")
	}
	if _, err := s.RecordCheck(ctx, failCheck(1)); err == nil {
		t.Error("RecordCheck: expected error")
	}
	if _, err := s.UpdateProduct(ctx, 1, ProductPatch{}); err == nil {
		t.Error("UpdateProduct: expected error")
	}
	if err := s.UpdateProductMeta(ctx, 1, ProductMeta{Name: "x"}); err == nil {
		t.Error("UpdateProductMeta: expected error")
	}
	if err := s.DeleteProduct(ctx, 1); err == nil {
		t.Error("DeleteProduct: expected error")
	}
	if err := s.Ping(ctx); err == nil {
		t.Error("Ping: expected error")
	}
}
