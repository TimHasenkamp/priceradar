// Package store is the SQLite persistence layer of PriceRadar.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Store wraps the SQLite database.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Option customises a Store at Open time.
type Option func(*Store)

// WithClock injects the clock used for created_at/updated_at and the 30-day window.
func WithClock(now func() time.Time) Option {
	return func(s *Store) { s.now = now }
}

// _txlock=immediate: write transactions take the write lock up front, so a
// read-then-write tx can't fail with SQLITE_BUSY when another writer commits.
const pragmas = "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate"

// Open opens (and migrates) the database at path. Use ":memory:" for tests.
func Open(ctx context.Context, path string, opts ...Option) (*Store, error) {
	var dsn string
	memory := path == ":memory:"
	if memory {
		dsn = "file::memory:?" + pragmas
	} else {
		dsn = "file:" + filepath.ToSlash(path) + "?" + pragmas + "&_pragma=journal_mode(WAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	if memory {
		// Every connection to :memory: would be its own database.
		db.SetMaxOpenConns(1)
	}
	s := &Store{db: db, now: func() time.Time { return time.Now().UTC() }}
	for _, o := range opts {
		o(s)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: open: %w", err)
	}
	migs, err := loadMigrations()
	if err == nil {
		err = applyMigrations(ctx, db, migs, s.now)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// Ping verifies the database is reachable.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("store: ping: %w", err)
	}
	return nil
}

// ---- helpers ----

func toMS(t time.Time) int64 { return t.UTC().UnixMilli() }

func fromMS(v int64) time.Time { return time.UnixMilli(v).UTC() }

func fromNullMS(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := fromMS(n.Int64)
	return &t
}

func nullInt(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

func nullStr(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

func intPtr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func strPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

func isUnique(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}

type scanner interface{ Scan(dest ...any) error }

const productCols = `p.id, p.shop, p.url, p.sku, p.name, p.custom_name, p.image_url, p.currency,
	p.target_price_cents, p.paused, p.notify, p.created_at, p.updated_at, p.last_checked_at, p.last_success_at,
	p.consecutive_failures, p.last_error_kind, p.last_error`

// productDests returns scan destinations; call finish afterwards to fill p.
func productDests(p *Product) (dests []any, finish func()) {
	var (
		custom, errKind, errMsg sql.NullString
		target, lastChk, lastOK sql.NullInt64
		paused, notify          int64
		created, updated        int64
	)
	dests = []any{&p.ID, &p.Shop, &p.URL, &p.SKU, &p.Name, &custom, &p.ImageURL, &p.Currency,
		&target, &paused, &notify, &created, &updated, &lastChk, &lastOK,
		&p.ConsecutiveFailures, &errKind, &errMsg}
	finish = func() {
		p.CustomName = strPtr(custom)
		p.TargetPriceCents = intPtr(target)
		p.Paused = paused != 0
		p.Notify = notify != 0
		p.CreatedAt = fromMS(created)
		p.UpdatedAt = fromMS(updated)
		p.LastCheckedAt = fromNullMS(lastChk)
		p.LastSuccessAt = fromNullMS(lastOK)
		p.LastErrorKind = strPtr(errKind)
		p.LastError = strPtr(errMsg)
	}
	return
}

func scanProduct(r scanner) (Product, error) {
	var p Product
	dests, finish := productDests(&p)
	if err := r.Scan(dests...); err != nil {
		return Product{}, err
	}
	finish()
	return p, nil
}

// ---- products ----

// CreateProduct inserts a new product. It returns ErrDuplicate if the URL exists.
func (s *Store) CreateProduct(ctx context.Context, np NewProduct) (Product, error) {
	currency := np.Currency
	if currency == "" {
		currency = "EUR"
	}
	now := toMS(s.now())
	res, err := s.db.ExecContext(ctx, `INSERT INTO products
		(shop, url, sku, name, image_url, currency, target_price_cents, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		np.Shop, np.URL, np.SKU, np.Name, np.ImageURL, currency, nullInt(np.TargetPriceCents), now, now)
	if err != nil {
		if isUnique(err) {
			return Product{}, fmt.Errorf("store: create product: %w", ErrDuplicate)
		}
		return Product{}, fmt.Errorf("store: create product: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Product{}, fmt.Errorf("store: create product: %w", err)
	}
	return s.GetProduct(ctx, id)
}

// GetProduct returns the product with the given id or ErrNotFound.
func (s *Store) GetProduct(ctx context.Context, id int64) (Product, error) {
	p, err := scanProduct(s.db.QueryRowContext(ctx, `SELECT `+productCols+` FROM products p WHERE p.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Product{}, fmt.Errorf("store: get product %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return Product{}, fmt.Errorf("store: get product %d: %w", id, err)
	}
	return p, nil
}

// statsSelect computes all stats in one statement. The single bound parameter
// (first) is the start of the 30-day window in unix ms.
const statsSelect = `
WITH ok AS (
	SELECT * FROM price_checks WHERE success = 1 AND price_cents IS NOT NULL
), cur AS (
	SELECT product_id, price_cents, old_price_cents, rrp_cents, availability FROM (
		SELECT *, ROW_NUMBER() OVER (PARTITION BY product_id ORDER BY checked_at DESC, id DESC) AS rn FROM ok
	) WHERE rn = 1
), agg AS (
	SELECT product_id, MIN(price_cents) AS mn, MAX(price_cents) AS mx,
		CAST(ROUND(AVG(CASE WHEN checked_at >= ? THEN price_cents END)) AS INTEGER) AS avg30
	FROM ok GROUP BY product_id
)
SELECT ` + productCols + `,
	cur.price_cents,
	(SELECT o.price_cents FROM ok o WHERE o.product_id = p.id AND o.price_cents <> cur.price_cents
		ORDER BY o.checked_at DESC, o.id DESC LIMIT 1),
	agg.avg30, agg.mn, agg.mx, cur.old_price_cents, cur.rrp_cents, cur.availability
FROM products p
LEFT JOIN cur ON cur.product_id = p.id
LEFT JOIN agg ON agg.product_id = p.id
`

func (s *Store) queryWithStats(ctx context.Context, where, order string, args ...any) ([]ProductWithStats, error) {
	cutoff := toMS(s.now().Add(-30 * 24 * time.Hour))
	rows, err := s.db.QueryContext(ctx, statsSelect+where+order, append([]any{cutoff}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProductWithStats
	for rows.Next() {
		var pw ProductWithStats
		dests, finish := productDests(&pw.Product)
		var cur, prev, avg, mn, mx, old, rrp sql.NullInt64
		var avail sql.NullString
		dests = append(dests, &cur, &prev, &avg, &mn, &mx, &old, &rrp, &avail)
		if err := rows.Scan(dests...); err != nil {
			return nil, err
		}
		finish()
		pw.Stats = ProductStats{
			CurrentCents: intPtr(cur), PreviousCents: intPtr(prev), Avg30Cents: intPtr(avg),
			MinCents: intPtr(mn), MaxCents: intPtr(mx), OldPriceCents: intPtr(old),
			RRPCents: intPtr(rrp), Availability: strPtr(avail),
		}
		out = append(out, pw)
	}
	return out, rows.Err()
}

// GetProductWithStats returns a product with its aggregates or ErrNotFound.
func (s *Store) GetProductWithStats(ctx context.Context, id int64) (ProductWithStats, error) {
	res, err := s.queryWithStats(ctx, ` WHERE p.id = ?`, ``, id)
	if err != nil {
		return ProductWithStats{}, fmt.Errorf("store: get product %d with stats: %w", id, err)
	}
	if len(res) == 0 {
		return ProductWithStats{}, fmt.Errorf("store: get product %d with stats: %w", id, ErrNotFound)
	}
	return res[0], nil
}

// ListProducts returns all products with stats, newest first.
func (s *Store) ListProducts(ctx context.Context) ([]ProductWithStats, error) {
	res, err := s.queryWithStats(ctx, ``, ` ORDER BY p.created_at DESC, p.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: list products: %w", err)
	}
	return res, nil
}

// ListActiveProducts returns non-paused products, least recently checked first
// (never-checked products come first).
func (s *Store) ListActiveProducts(ctx context.Context) ([]Product, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+productCols+` FROM products p
		WHERE p.paused = 0 ORDER BY p.last_checked_at IS NOT NULL, p.last_checked_at ASC, p.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: list active products: %w", err)
	}
	defer rows.Close()
	var out []Product
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list active products: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list active products: %w", err)
	}
	return out, nil
}

// UpdateProduct applies a partial update and returns the updated product.
func (s *Store) UpdateProduct(ctx context.Context, id int64, patch ProductPatch) (Product, error) {
	sets := []string{"updated_at = ?"}
	args := []any{toMS(s.now())}
	switch {
	case patch.ClearCustomName:
		sets = append(sets, "custom_name = NULL")
	case patch.CustomName != nil:
		sets = append(sets, "custom_name = ?")
		args = append(args, *patch.CustomName)
	}
	if patch.Paused != nil {
		sets = append(sets, "paused = ?")
		args = append(args, *patch.Paused)
	}
	if patch.Notify != nil {
		sets = append(sets, "notify = ?")
		args = append(args, *patch.Notify)
	}
	switch {
	case patch.ClearTargetPrice:
		sets = append(sets, "target_price_cents = NULL")
	case patch.TargetPriceCents != nil:
		sets = append(sets, "target_price_cents = ?")
		args = append(args, *patch.TargetPriceCents)
	}
	args = append(args, id)
	res, err := s.db.ExecContext(ctx, `UPDATE products SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return Product{}, fmt.Errorf("store: update product %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Product{}, fmt.Errorf("store: update product %d: %w", id, ErrNotFound)
	}
	return s.GetProduct(ctx, id)
}

// UpdateProductMeta overwrites scraped fields; empty strings are skipped.
func (s *Store) UpdateProductMeta(ctx context.Context, id int64, m ProductMeta) error {
	res, err := s.db.ExecContext(ctx, `UPDATE products SET
		sku = COALESCE(NULLIF(?, ''), sku),
		name = COALESCE(NULLIF(?, ''), name),
		image_url = COALESCE(NULLIF(?, ''), image_url),
		currency = COALESCE(NULLIF(?, ''), currency),
		updated_at = ?
		WHERE id = ?`, m.SKU, m.Name, m.ImageURL, m.Currency, toMS(s.now()), id)
	if err != nil {
		return fmt.Errorf("store: update product meta %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: update product meta %d: %w", id, ErrNotFound)
	}
	return nil
}

// DeleteProduct removes a product and (by cascade) its checks.
func (s *Store) DeleteProduct(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM products WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete product %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: delete product %d: %w", id, ErrNotFound)
	}
	return nil
}

// ---- checks ----

const checkCols = `id, product_id, checked_at, success, price_cents, old_price_cents, rrp_cents,
	availability, error_kind, error_message, duration_ms`

func scanCheck(r scanner) (PriceCheck, error) {
	var (
		c                      PriceCheck
		at                     int64
		ok                     int64
		price, old, rrp        sql.NullInt64
		avail, errKind, errMsg sql.NullString
	)
	if err := r.Scan(&c.ID, &c.ProductID, &at, &ok, &price, &old, &rrp, &avail, &errKind, &errMsg, &c.DurationMS); err != nil {
		return PriceCheck{}, err
	}
	c.CheckedAt = fromMS(at)
	c.Success = ok != 0
	c.PriceCents, c.OldPriceCents, c.RRPCents = intPtr(price), intPtr(old), intPtr(rrp)
	c.Availability, c.ErrorKind, c.ErrorMessage = strPtr(avail), strPtr(errKind), strPtr(errMsg)
	return c, nil
}

// RecordCheck stores a check and updates the product's status in one transaction.
func (s *Store) RecordCheck(ctx context.Context, c PriceCheck) (PriceCheck, error) {
	if c.CheckedAt.IsZero() {
		c.CheckedAt = s.now()
	}
	c.CheckedAt = c.CheckedAt.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PriceCheck{}, fmt.Errorf("store: record check: %w", err)
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM products WHERE id = ?`, c.ProductID).Scan(&exists); err != nil {
		return PriceCheck{}, fmt.Errorf("store: record check: %w", err)
	}
	if exists == 0 {
		return PriceCheck{}, fmt.Errorf("store: record check: product %d: %w", c.ProductID, ErrNotFound)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO price_checks
		(product_id, checked_at, success, price_cents, old_price_cents, rrp_cents, availability,
		 error_kind, error_message, duration_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ProductID, toMS(c.CheckedAt), c.Success, nullInt(c.PriceCents), nullInt(c.OldPriceCents),
		nullInt(c.RRPCents), nullStr(c.Availability), nullStr(c.ErrorKind), nullStr(c.ErrorMessage), c.DurationMS)
	if err != nil {
		return PriceCheck{}, fmt.Errorf("store: record check: %w", err)
	}
	if c.ID, err = res.LastInsertId(); err != nil {
		return PriceCheck{}, fmt.Errorf("store: record check: %w", err)
	}
	if c.Success {
		_, err = tx.ExecContext(ctx, `UPDATE products SET last_checked_at = ?, last_success_at = ?,
			consecutive_failures = 0, last_error_kind = NULL, last_error = NULL WHERE id = ?`,
			toMS(c.CheckedAt), toMS(c.CheckedAt), c.ProductID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE products SET last_checked_at = ?,
			consecutive_failures = consecutive_failures + 1, last_error_kind = ?, last_error = ? WHERE id = ?`,
			toMS(c.CheckedAt), nullStr(c.ErrorKind), nullStr(c.ErrorMessage), c.ProductID)
	}
	if err != nil {
		return PriceCheck{}, fmt.Errorf("store: record check: update product: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PriceCheck{}, fmt.Errorf("store: record check: %w", err)
	}
	return c, nil
}

func (s *Store) queryChecks(ctx context.Context, q string, args ...any) ([]PriceCheck, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PriceCheck
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListPrices returns successful checks of a product in ascending time order.
// from and to are inclusive and optional.
func (s *Store) ListPrices(ctx context.Context, productID int64, from, to *time.Time) ([]PriceCheck, error) {
	q := `SELECT ` + checkCols + ` FROM price_checks WHERE product_id = ? AND success = 1`
	args := []any{productID}
	if from != nil {
		q += ` AND checked_at >= ?`
		args = append(args, toMS(*from))
	}
	if to != nil {
		q += ` AND checked_at <= ?`
		args = append(args, toMS(*to))
	}
	out, err := s.queryChecks(ctx, q+` ORDER BY checked_at ASC, id ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list prices: %w", err)
	}
	return out, nil
}

// ListChecks returns up to limit checks (successful or not), newest first.
func (s *Store) ListChecks(ctx context.Context, productID int64, limit int) ([]PriceCheck, error) {
	out, err := s.queryChecks(ctx, `SELECT `+checkCols+` FROM price_checks WHERE product_id = ?
		ORDER BY checked_at DESC, id DESC LIMIT ?`, productID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list checks: %w", err)
	}
	return out, nil
}
