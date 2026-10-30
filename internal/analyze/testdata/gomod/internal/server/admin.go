package server

import "github.com/go-chi/chi/v5"

func (s *Server) adminRoutes(r chi.Router) {
	r.Get("/admin/stats", s.stats)
}

func (s *Server) stats() {} // TODO: real numbers
