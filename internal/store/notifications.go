package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const notificationCols = `id, product_id, kind, title, message, priority, status, error,
	target_price_cents, created_at, sent_at`

func scanNotification(r scanner) (Notification, error) {
	var (
		n       Notification
		errMsg  sql.NullString
		target  sql.NullInt64
		created int64
		sent    sql.NullInt64
	)
	if err := r.Scan(&n.ID, &n.ProductID, &n.Kind, &n.Title, &n.Message, &n.Priority, &n.Status,
		&errMsg, &target, &created, &sent); err != nil {
		return Notification{}, err
	}
	n.Error, n.TargetPriceCents = strPtr(errMsg), intPtr(target)
	n.CreatedAt, n.SentAt = fromMS(created), fromNullMS(sent)
	return n, nil
}

func isForeignKey(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code() == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY
}

func nullTime(t *time.Time) sql.NullInt64 {
	if t == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: toMS(*t), Valid: true}
}

// InsertNotification stores a notification. A zero CreatedAt is set to now.
// It returns ErrNotFound if the product does not exist.
func (s *Store) InsertNotification(ctx context.Context, n Notification) (Notification, error) {
	if n.CreatedAt.IsZero() {
		n.CreatedAt = s.now()
	}
	n.CreatedAt = n.CreatedAt.UTC()
	res, err := s.db.ExecContext(ctx, `INSERT INTO notifications
		(product_id, kind, title, message, priority, status, error, target_price_cents, created_at, sent_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ProductID, n.Kind, n.Title, n.Message, n.Priority, n.Status, nullStr(n.Error),
		nullInt(n.TargetPriceCents), toMS(n.CreatedAt), nullTime(n.SentAt))
	if err != nil {
		if isForeignKey(err) {
			return Notification{}, fmt.Errorf("store: insert notification: product %d: %w", n.ProductID, ErrNotFound)
		}
		return Notification{}, fmt.Errorf("store: insert notification: %w", err)
	}
	if n.ID, err = res.LastInsertId(); err != nil {
		return Notification{}, fmt.Errorf("store: insert notification: %w", err)
	}
	return n, nil
}

// UpdateNotificationStatus records the delivery outcome of a notification.
func (s *Store) UpdateNotificationStatus(ctx context.Context, id int64, status string, errMsg *string, sentAt *time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE notifications SET status = ?, error = ?, sent_at = ? WHERE id = ?`,
		status, nullStr(errMsg), nullTime(sentAt), id)
	if err != nil {
		return fmt.Errorf("store: update notification %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: update notification %d: %w", id, ErrNotFound)
	}
	return nil
}

// FailPendingNotifications marks all pending notifications as failed with msg,
// e.g. at startup for messages that were queued when the process stopped.
func (s *Store) FailPendingNotifications(ctx context.Context, msg string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE notifications SET status = ?, error = ? WHERE status = ?`,
		NotificationFailed, msg, NotificationPending)
	if err != nil {
		return 0, fmt.Errorf("store: fail pending notifications: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (s *Store) queryNotifications(ctx context.Context, q string, args ...any) ([]Notification, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ListNotifications returns up to limit notifications of a product, newest first.
func (s *Store) ListNotifications(ctx context.Context, productID int64, limit int) ([]Notification, error) {
	out, err := s.queryNotifications(ctx, `SELECT `+notificationCols+` FROM notifications
		WHERE product_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, productID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list notifications: %w", err)
	}
	return out, nil
}

// LastNotifications returns the latest notification of each kind for a product
// (the dedupe/cooldown state of the notification evaluator).
func (s *Store) LastNotifications(ctx context.Context, productID int64) (map[string]Notification, error) {
	list, err := s.queryNotifications(ctx, `SELECT `+notificationCols+` FROM (
		SELECT *, ROW_NUMBER() OVER (PARTITION BY kind ORDER BY created_at DESC, id DESC) AS rn
		FROM notifications WHERE product_id = ?) WHERE rn = 1`, productID)
	if err != nil {
		return nil, fmt.Errorf("store: last notifications: %w", err)
	}
	out := make(map[string]Notification, len(list))
	for _, n := range list {
		out[n.Kind] = n
	}
	return out, nil
}

// SuccessSummary aggregates all successful checks of a product except the
// check with id excludeID (pass the id of a just-recorded check to get the
// state before it, or 0 for all checks).
func (s *Store) SuccessSummary(ctx context.Context, productID, excludeID int64) (SuccessSummary, error) {
	var (
		sum   SuccessSummary
		first sql.NullInt64
		min   sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), MIN(checked_at), MIN(price_cents) FROM price_checks
		WHERE product_id = ? AND success = 1 AND price_cents IS NOT NULL AND id <> ?`, productID, excludeID).
		Scan(&sum.Count, &first, &min)
	if err != nil {
		return SuccessSummary{}, fmt.Errorf("store: success summary: %w", err)
	}
	sum.FirstAt, sum.MinCents = fromNullMS(first), intPtr(min)
	last, err := s.queryChecks(ctx, `SELECT `+checkCols+` FROM price_checks
		WHERE product_id = ? AND success = 1 AND price_cents IS NOT NULL AND id <> ?
		ORDER BY checked_at DESC, id DESC LIMIT 1`, productID, excludeID)
	if err != nil {
		return SuccessSummary{}, fmt.Errorf("store: success summary: %w", err)
	}
	if len(last) == 1 {
		sum.Last = &last[0]
	}
	return sum, nil
}
