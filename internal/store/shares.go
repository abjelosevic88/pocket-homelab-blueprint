package store

import (
	"context"
	"strings"
)

// ListShares returns who a codebase is shared with.
func (s *Store) ListShares(ctx context.Context, cid int64) ([]Share, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT u.id, u.name, u.email, sh.role, sh.created_at FROM shares sh JOIN users u ON u.id = sh.user_id
		WHERE sh.codebase_id = ? ORDER BY lower(COALESCE(NULLIF(u.name, ''), u.email))`, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Share{}
	for rows.Next() {
		var sh Share
		if err := rows.Scan(&sh.UserID, &sh.Name, &sh.Email, &sh.Role, &sh.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// SetShare shares a codebase with a person (or changes their role); new
// tells whether it was not shared with them before.
func (s *Store) SetShare(ctx context.Context, cid, uid int64, role string) (bool, error) {
	var had int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM shares WHERE codebase_id = ? AND user_id = ?`, cid, uid).Scan(&had)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO shares (codebase_id, user_id, role) VALUES (?, ?, ?)
		ON CONFLICT(codebase_id, user_id) DO UPDATE SET role = excluded.role`, cid, uid, role)
	return had == 0, err
}

// RemoveShare stops sharing a codebase with a person.
func (s *Store) RemoveShare(ctx context.Context, cid, uid int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM shares WHERE codebase_id = ? AND user_id = ?`, cid, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountShares returns with how many people a codebase is shared.
func (s *Store) CountShares(ctx context.Context, cid int64) int {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM shares WHERE codebase_id = ?`, cid).Scan(&n)
	return n
}

// People lists everyone on this box but one person, for sharing with.
func (s *Store) People(ctx context.Context, except int64) ([]Person, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, email FROM users WHERE id != ? ORDER BY lower(COALESCE(NULLIF(name, ''), email))`, except)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Person{}
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.ID, &p.Name, &p.Email); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DisplayName is how a person is shown to others.
func DisplayName(name, email string) string {
	if n := strings.TrimSpace(name); n != "" {
		return n
	}
	return email
}
