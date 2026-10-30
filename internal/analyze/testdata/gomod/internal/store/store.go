package store

import "example.com/shop/internal/server"

// Store keeps users.
type Store struct{ path string }

// Open opens the database file.
func Open(path string) *Store { return &Store{path: path} }

// GetUser finds one user.
func (s *Store) GetUser(id int) string { return server.Version }
