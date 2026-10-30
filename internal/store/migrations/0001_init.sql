CREATE TABLE users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'member',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  token_hash   TEXT NOT NULL UNIQUE,
  prefix       TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Box-wide settings (title, setup flag); everything else is personal.
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE user_settings (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key     TEXT NOT NULL,
  value   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, key)
);

CREATE TABLE cache (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL DEFAULT '',
  fetched_at TEXT NOT NULL
);

-- A codebase: one folder of source code to inspect. Where it comes from:
-- a folder on this server (path, admins only), a git repository (git, cloned
-- into DATA_DIR/work/<id>), an uploaded archive (upload) or a folder in
-- Pocket Files (files, "space:path", fetched through a Pocket Core link).
CREATE TABLE codebases (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  description  TEXT NOT NULL DEFAULT '',
  source       TEXT NOT NULL,                 -- path | git | upload | files
  location     TEXT NOT NULL DEFAULT '',      -- the folder, the clone URL, the archive name or space:path
  branch       TEXT NOT NULL DEFAULT '',
  subdir       TEXT NOT NULL DEFAULT '',      -- scan only this folder inside it
  git_user     TEXT NOT NULL DEFAULT '',
  git_token    TEXT NOT NULL DEFAULT '',      -- never returned by the API
  vendored     INTEGER NOT NULL DEFAULT 0,    -- also read vendor/, node_modules/, third_party/
  rescan_hours INTEGER NOT NULL DEFAULT 0,    -- scan again on its own every n hours (0 = never)
  pinned       INTEGER NOT NULL DEFAULT 0,
  last_scan_id INTEGER,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX codebases_user ON codebases(user_id, pinned DESC, name);

-- One reading of a codebase. The whole result (files, symbols, imports,
-- endpoints, tables…) is kept as gzipped JSON; stats is the headline numbers.
CREATE TABLE scans (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  codebase_id  INTEGER NOT NULL REFERENCES codebases(id) ON DELETE CASCADE,
  status       TEXT NOT NULL DEFAULT 'queued', -- queued | running | done | failed | cancelled
  stage        TEXT NOT NULL DEFAULT '',
  progress     INTEGER NOT NULL DEFAULT 0,     -- 0..100
  error        TEXT NOT NULL DEFAULT '',
  trigger      TEXT NOT NULL DEFAULT 'manual', -- manual | auto | new | link:<app>
  commit_sha   TEXT NOT NULL DEFAULT '',
  branch       TEXT NOT NULL DEFAULT '',
  stats        TEXT NOT NULL DEFAULT '{}',
  result       BLOB,
  result_bytes INTEGER NOT NULL DEFAULT 0,
  started_at   TEXT NOT NULL DEFAULT '',
  finished_at  TEXT NOT NULL DEFAULT '',
  duration_ms  INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX scans_user_codebase ON scans(user_id, codebase_id, id DESC);
CREATE INDEX scans_status ON scans(status);

-- Notes on a folder, a file or a symbol of a codebase: your own, or an AI
-- explanation (kind 'ai', tied to the scan it explained).
CREATE TABLE notes (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  codebase_id INTEGER NOT NULL REFERENCES codebases(id) ON DELETE CASCADE,
  path        TEXT NOT NULL DEFAULT '',       -- '' = the whole codebase; "dir/file.go" or "dir/file.go#Symbol"
  kind        TEXT NOT NULL DEFAULT 'note',   -- note | ai
  text        TEXT NOT NULL DEFAULT '',
  scan_id     INTEGER,
  model       TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX notes_user_codebase ON notes(user_id, codebase_id, path);
