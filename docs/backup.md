# Backup

One SQLite file: `DATA_DIR/pocket-blueprint.db` (WAL mode). Take a consistent copy with `pocket-blueprint backup` (writes `DATA_DIR/backups/pocket-blueprint-<time>.db` with `VACUUM INTO`) or Settings → Backup & data → Download database (admin). Back up that copy, not the live file. `DATA_DIR/core.token*` is the Pocket Core registration.

Restore: stop the app, replace `pocket-blueprint.db` (remove `-wal`/`-shm` files), start.

`DATA_DIR/work/` holds the clones and unpacked archives: no need to back it up — git codebases are cloned again and server folders read again on the next scan; uploaded archives need uploading again. Leave it out of backups.

Every person can also export their codebases and notes as JSON (`/api/v1/export.json`, re-importable; never git tokens).
