ALTER TABLE products ADD COLUMN notify INTEGER NOT NULL DEFAULT 1;

-- One row per notification event (also when it was not sent).
-- status: pending (queued for the dispatcher) | sent | failed | skipped (Gotify not configured).
-- target_price_cents: the product's target at the time; used to re-arm
-- target_reached when the target changes.
CREATE TABLE notifications (
    id INTEGER PRIMARY KEY,
    product_id INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    title TEXT NOT NULL,
    message TEXT NOT NULL,
    priority INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'sent', 'failed', 'skipped')),
    error TEXT NULL,
    target_price_cents INTEGER NULL,
    created_at INTEGER NOT NULL,
    sent_at INTEGER NULL
);

CREATE INDEX idx_notifications_product_time ON notifications(product_id, created_at);
