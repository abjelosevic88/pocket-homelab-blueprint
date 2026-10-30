ALTER TABLE orders ADD COLUMN status TEXT NOT NULL DEFAULT 'new';
CREATE TABLE order_items (
    order_id INTEGER,
    sku TEXT,
    PRIMARY KEY (order_id, sku),
    FOREIGN KEY (order_id) REFERENCES orders(id)
);
DROP TABLE IF EXISTS tmp;
ALTER TABLE users RENAME COLUMN name TO full_name;
