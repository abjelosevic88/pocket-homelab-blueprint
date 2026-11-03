-- A codebase shared with another person on this box: they find it in their
-- list and read every scan of it (view), or also scan it again (scan).
-- Settings, deleting and sharing stay with the owner; notes stay personal.
CREATE TABLE shares (
  codebase_id INTEGER NOT NULL REFERENCES codebases(id) ON DELETE CASCADE,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role        TEXT NOT NULL DEFAULT 'view',   -- view | scan
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  PRIMARY KEY (codebase_id, user_id)
);
CREATE INDEX shares_user ON shares(user_id);
