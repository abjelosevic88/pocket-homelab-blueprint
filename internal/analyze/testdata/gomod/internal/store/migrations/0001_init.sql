-- Users and orders.
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    name TEXT,
    created_at TEXT DEFAULT (strftime('%Y-%m-%d', 'now'))
);

CREATE TABLE "orders" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, -- owner; not a ; terminator
    total NUMERIC(10, 2) NOT NULL,
    /* free text */ note TEXT
);

CREATE TABLE tmp (x int);
