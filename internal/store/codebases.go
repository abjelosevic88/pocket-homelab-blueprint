package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

const codebaseCols = `id, user_id, name, description, source, location, branch, subdir, git_user, git_token, vendored, rescan_hours, pinned, last_scan_id, created_at, updated_at`

func scanCodebase(row interface{ Scan(...any) error }) (*Codebase, error) {
	var c Codebase
	var vendored, pinned int
	var last sql.NullInt64
	if err := row.Scan(&c.ID, &c.UserID, &c.Name, &c.Description, &c.Source, &c.Location, &c.Branch, &c.Subdir, &c.GitUser, &c.GitToken,
		&vendored, &c.RescanHours, &pinned, &last, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	c.Vendored, c.Pinned, c.HasToken = vendored == 1, pinned == 1, c.GitToken != ""
	if last.Valid {
		c.LastScanID = &last.Int64
	}
	return &c, nil
}

// ListCodebases returns one person's codebases, pinned first, with their
// latest scan and latest finished scan.
func (s *Store) ListCodebases(ctx context.Context, uid int64) ([]Codebase, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+codebaseCols+` FROM codebases WHERE user_id = ? ORDER BY pinned DESC, name COLLATE NOCASE`, uid)
	if err != nil {
		return nil, err
	}
	var out []Codebase
	for rows.Next() {
		c, err := scanCodebase(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Latest, out[i].Done = s.latestScans(ctx, uid, out[i].ID)
	}
	if out == nil {
		out = []Codebase{}
	}
	return out, nil
}

func (s *Store) latestScans(ctx context.Context, uid, cid int64) (latest, done *Scan) {
	latest, _ = scanScan(s.DB.QueryRowContext(ctx, `SELECT `+scanCols+` FROM scans WHERE user_id = ? AND codebase_id = ? ORDER BY id DESC LIMIT 1`, uid, cid))
	done, _ = scanScan(s.DB.QueryRowContext(ctx, `SELECT `+scanCols+` FROM scans WHERE user_id = ? AND codebase_id = ? AND status = 'done' ORDER BY id DESC LIMIT 1`, uid, cid))
	return latest, done
}

// GetCodebase returns one of the person's codebases (ErrNotFound for anyone else's).
func (s *Store) GetCodebase(ctx context.Context, uid, id int64) (*Codebase, error) {
	c, err := scanCodebase(s.DB.QueryRowContext(ctx, `SELECT `+codebaseCols+` FROM codebases WHERE id = ? AND user_id = ?`, id, uid))
	if err != nil {
		return nil, notFound(err)
	}
	c.Latest, c.Done = s.latestScans(ctx, uid, c.ID)
	return c, nil
}

// codebaseByID is for background work that already knows the owner.
func (s *Store) CodebaseByID(ctx context.Context, id int64) (*Codebase, error) {
	c, err := scanCodebase(s.DB.QueryRowContext(ctx, `SELECT `+codebaseCols+` FROM codebases WHERE id = ?`, id))
	return c, notFound(err)
}

// CreateCodebase adds a codebase for a person.
func (s *Store) CreateCodebase(ctx context.Context, uid int64, c *Codebase) error {
	now := Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO codebases (user_id, name, description, source, location, branch, subdir, git_user, git_token, vendored, rescan_hours, pinned, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uid, c.Name, c.Description, c.Source, c.Location, c.Branch, c.Subdir, c.GitUser, c.GitToken, boolInt(c.Vendored), c.RescanHours, boolInt(c.Pinned), now, now)
	if err != nil {
		return conflict(err)
	}
	c.ID, _ = res.LastInsertId()
	c.UserID, c.CreatedAt, c.UpdatedAt, c.HasToken = uid, now, now, c.GitToken != ""
	return nil
}

// UpdateCodebase saves the editable fields.
func (s *Store) UpdateCodebase(ctx context.Context, uid int64, c *Codebase) error {
	c.UpdatedAt = Now()
	res, err := s.DB.ExecContext(ctx, `UPDATE codebases SET name = ?, description = ?, location = ?, branch = ?, subdir = ?, git_user = ?, git_token = ?, vendored = ?, rescan_hours = ?, pinned = ?, updated_at = ?
		WHERE id = ? AND user_id = ?`,
		c.Name, c.Description, c.Location, c.Branch, c.Subdir, c.GitUser, c.GitToken, boolInt(c.Vendored), c.RescanHours, boolInt(c.Pinned), c.UpdatedAt, c.ID, uid)
	if err != nil {
		return conflict(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	c.HasToken = c.GitToken != ""
	return nil
}

// DeleteCodebase removes a codebase with its scans and notes.
func (s *Store) DeleteCodebase(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM codebases WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DueForRescan lists codebases whose automatic rescan is due and that have
// nothing queued or running.
func (s *Store) DueForRescan(ctx context.Context) ([]Codebase, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+codebaseCols+` FROM codebases c WHERE rescan_hours > 0
		AND NOT EXISTS (SELECT 1 FROM scans WHERE codebase_id = c.id AND status IN ('queued','running'))
		AND COALESCE((SELECT MAX(created_at) FROM scans WHERE codebase_id = c.id), '') < strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '-' || rescan_hours || ' hours')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Codebase
	for rows.Next() {
		c, err := scanCodebase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// ---- scans ----

const scanCols = `id, user_id, codebase_id, status, stage, progress, error, trigger, commit_sha, branch, stats, result_bytes, started_at, finished_at, duration_ms, created_at`

func scanScan(row interface{ Scan(...any) error }) (*Scan, error) {
	var sc Scan
	var stats string
	if err := row.Scan(&sc.ID, &sc.UserID, &sc.CodebaseID, &sc.Status, &sc.Stage, &sc.Progress, &sc.Error, &sc.Trigger, &sc.Commit, &sc.Branch,
		&stats, &sc.ResultBytes, &sc.StartedAt, &sc.FinishedAt, &sc.DurationMS, &sc.CreatedAt); err != nil {
		return nil, err
	}
	if !json.Valid([]byte(stats)) {
		stats = "{}"
	}
	sc.Stats = json.RawMessage(stats)
	return &sc, nil
}

// QueueScan adds a queued scan for a codebase, unless one is already queued
// or running (then that one is returned).
func (s *Store) QueueScan(ctx context.Context, uid, cid int64, trigger string) (*Scan, bool, error) {
	if sc, err := scanScan(s.DB.QueryRowContext(ctx, `SELECT `+scanCols+` FROM scans WHERE user_id = ? AND codebase_id = ? AND status IN ('queued','running') ORDER BY id DESC LIMIT 1`, uid, cid)); err == nil {
		return sc, false, nil
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO scans (user_id, codebase_id, trigger) VALUES (?, ?, ?)`, uid, cid, trigger)
	if err != nil {
		return nil, false, err
	}
	id, _ := res.LastInsertId()
	sc, err := s.GetScan(ctx, uid, id)
	return sc, true, err
}

// GetScan returns one of the person's scans (without the result).
func (s *Store) GetScan(ctx context.Context, uid, id int64) (*Scan, error) {
	sc, err := scanScan(s.DB.QueryRowContext(ctx, `SELECT `+scanCols+` FROM scans WHERE id = ? AND user_id = ?`, id, uid))
	return sc, notFound(err)
}

// ScanByID is for the scan worker.
func (s *Store) ScanByID(ctx context.Context, id int64) (*Scan, error) {
	sc, err := scanScan(s.DB.QueryRowContext(ctx, `SELECT `+scanCols+` FROM scans WHERE id = ?`, id))
	return sc, notFound(err)
}

// ListScans returns a codebase's scans, newest first.
func (s *Store) ListScans(ctx context.Context, uid, cid int64) ([]Scan, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+scanCols+` FROM scans WHERE user_id = ? AND codebase_id = ? ORDER BY id DESC LIMIT 200`, uid, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Scan{}
	for rows.Next() {
		sc, err := scanScan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sc)
	}
	return out, rows.Err()
}

// QueuedScans lists the scans waiting, oldest first.
func (s *Store) QueuedScans(ctx context.Context) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM scans WHERE status = 'queued' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// InterruptRunning marks scans left running by a stopped process as failed.
func (s *Store) InterruptRunning(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE scans SET status = 'failed', error = 'Stopped when Pocket Blueprint restarted; scan again.', finished_at = ? WHERE status = 'running'`, Now())
	return err
}

// StartScan marks a queued scan as running. False when it was cancelled meanwhile.
func (s *Store) StartScan(ctx context.Context, id int64) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE scans SET status = 'running', stage = 'starting', progress = 0, started_at = ? WHERE id = ? AND status = 'queued'`, Now(), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ScanProgress records how far a running scan is.
func (s *Store) ScanProgress(ctx context.Context, id int64, stage string, progress int) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE scans SET stage = ?, progress = ? WHERE id = ? AND status = 'running'`, stage, progress, id)
	return err
}

// ScanStatus reads only the status (the worker checks for cancellation).
func (s *Store) ScanStatus(ctx context.Context, id int64) string {
	var st string
	_ = s.DB.QueryRowContext(ctx, `SELECT status FROM scans WHERE id = ?`, id).Scan(&st)
	return st
}

// FinishScan stores a finished scan's result and makes it the codebase's latest.
func (s *Store) FinishScan(ctx context.Context, sc *Scan, result []byte) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE scans SET status = 'done', stage = '', progress = 100, error = '', commit_sha = ?, branch = ?, stats = ?, result = ?, result_bytes = ?, finished_at = ?, duration_ms = ?
		WHERE id = ? AND status = 'running'`, sc.Commit, sc.Branch, string(sc.Stats), result, len(result), sc.FinishedAt, sc.DurationMS, sc.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound // cancelled meanwhile
	}
	if _, err := tx.ExecContext(ctx, `UPDATE codebases SET last_scan_id = ?, updated_at = ? WHERE id = ?`, sc.ID, Now(), sc.CodebaseID); err != nil {
		return err
	}
	return tx.Commit()
}

// FailScan ends a scan with an error (or as cancelled).
func (s *Store) FailScan(ctx context.Context, id int64, status, msg string, durationMS int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE scans SET status = ?, error = ?, stage = '', finished_at = ?, duration_ms = ? WHERE id = ? AND status IN ('queued','running')`, status, msg, Now(), durationMS, id)
	return err
}

// CancelScan stops a queued or running scan of the person's.
func (s *Store) CancelScan(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE scans SET status = 'cancelled', stage = '', finished_at = ? WHERE id = ? AND user_id = ? AND status IN ('queued','running')`, Now(), id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteScan removes a finished scan of the person's.
func (s *Store) DeleteScan(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM scans WHERE id = ? AND user_id = ? AND status NOT IN ('queued','running')`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE codebases SET last_scan_id = (SELECT MAX(id) FROM scans WHERE codebase_id = codebases.id AND status = 'done') WHERE user_id = ? AND last_scan_id = ?`, uid, id)
	return err
}

// ScanResult returns the gzipped result of one of the person's finished scans.
func (s *Store) ScanResult(ctx context.Context, uid, id int64) ([]byte, error) {
	var b []byte
	err := s.DB.QueryRowContext(ctx, `SELECT result FROM scans WHERE id = ? AND user_id = ? AND status = 'done'`, id, uid).Scan(&b)
	if err != nil {
		return nil, notFound(err)
	}
	if len(b) == 0 {
		return nil, ErrNotFound
	}
	return b, nil
}

// PruneScans keeps the newest keep finished scans of a codebase (plus failed
// ones from the last day) and deletes the rest.
func (s *Store) PruneScans(ctx context.Context, cid int64, keep int) error {
	if keep < 1 {
		keep = 1
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM scans WHERE codebase_id = ? AND status = 'done' AND id NOT IN
		(SELECT id FROM scans WHERE codebase_id = ? AND status = 'done' ORDER BY id DESC LIMIT ?)`, cid, cid, keep)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM scans WHERE codebase_id = ? AND status IN ('failed','cancelled') AND created_at < strftime('%Y-%m-%dT%H:%M:%fZ','now','-1 day')
		AND id NOT IN (SELECT id FROM scans WHERE codebase_id = ? ORDER BY id DESC LIMIT 3)`, cid, cid)
	return err
}

// ---- notes ----

const noteCols = `id, user_id, codebase_id, path, kind, text, scan_id, model, created_at, updated_at`

func scanNote(row interface{ Scan(...any) error }) (*Note, error) {
	var n Note
	var scan sql.NullInt64
	if err := row.Scan(&n.ID, &n.UserID, &n.CodebaseID, &n.Path, &n.Kind, &n.Text, &scan, &n.Model, &n.CreatedAt, &n.UpdatedAt); err != nil {
		return nil, err
	}
	if scan.Valid {
		n.ScanID = &scan.Int64
	}
	return &n, nil
}

// ListNotes returns a codebase's notes; path "" = all, else that path and
// the symbols in it ("file#Symbol").
func (s *Store) ListNotes(ctx context.Context, uid, cid int64, path string, all bool) ([]Note, error) {
	q := `SELECT ` + noteCols + ` FROM notes WHERE user_id = ? AND codebase_id = ?`
	args := []any{uid, cid}
	if !all {
		q += ` AND (path = ? OR path LIKE ? || '#%')`
		args = append(args, path, path)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY kind DESC, id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Note{}
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// SaveNote adds a note, or (kind ai) replaces the AI explanation of that path.
func (s *Store) SaveNote(ctx context.Context, uid int64, n *Note) error {
	n.Path = strings.Trim(n.Path, "/")
	if n.Kind == "ai" {
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM notes WHERE user_id = ? AND codebase_id = ? AND path = ? AND kind = 'ai'`, uid, n.CodebaseID, n.Path); err != nil {
			return err
		}
	}
	now := Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO notes (user_id, codebase_id, path, kind, text, scan_id, model, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uid, n.CodebaseID, n.Path, n.Kind, n.Text, nullI(n.ScanID), n.Model, now, now)
	if err != nil {
		return err
	}
	n.ID, _ = res.LastInsertId()
	n.UserID, n.CreatedAt, n.UpdatedAt = uid, now, now
	return nil
}

// UpdateNote changes a note's text.
func (s *Store) UpdateNote(ctx context.Context, uid, id int64, text string) (*Note, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE notes SET text = ?, updated_at = ? WHERE id = ? AND user_id = ?`, text, Now(), id, uid)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	n, err := scanNote(s.DB.QueryRowContext(ctx, `SELECT `+noteCols+` FROM notes WHERE id = ?`, id))
	return n, notFound(err)
}

// DeleteNote removes a note of the person's.
func (s *Store) DeleteNote(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM notes WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
