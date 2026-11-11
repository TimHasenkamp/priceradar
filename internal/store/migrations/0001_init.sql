CREATE TABLE products (
    id INTEGER PRIMARY KEY,
    shop TEXT NOT NULL,
    url TEXT NOT NULL UNIQUE,
    sku TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    custom_name TEXT NULL,
    image_url TEXT NOT NULL DEFAULT '',
    currency TEXT NOT NULL DEFAULT 'EUR',
    target_price_cents INTEGER NULL,
    paused INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    last_checked_at INTEGER NULL,
    last_success_at INTEGER NULL,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    last_error_kind TEXT NULL,
    last_error TEXT NULL
);

CREATE TABLE price_checks (
    id INTEGER PRIMARY KEY,
    product_id INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    checked_at INTEGER NOT NULL,
    success INTEGER NOT NULL,
    price_cents INTEGER NULL,
    old_price_cents INTEGER NULL,
    rrp_cents INTEGER NULL,
    availability TEXT NULL,
    error_kind TEXT NULL,
    error_message TEXT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_price_checks_product_time ON price_checks(product_id, checked_at);
