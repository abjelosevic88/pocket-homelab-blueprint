// Package server serves the shop API.
package server

import (
	"net/http"
	"os"

	"example.com/shop/internal/store"
	"github.com/go-chi/chi/v5"
)

// Version is the API version.
const Version = "1.0"

const pingPath = "/ping"

// Server holds the dependencies.
type Server struct {
	store *store.Store
}

// New builds a Server. It opens the store.
func New(path string) *Server {
	return &Server{store: store.Open(path)}
}

// Router returns all routes.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", s.handleHealth)
	r.Route("/api/v1", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Get("/users/{id:[0-9]+}", s.handleUser)
			r.Post("/users", s.handleCreate)
		})
		r.Route("/orders", func(r chi.Router) {
			r.Get("/", s.listOrders)
			r.Delete("/{id}", s.deleteOrder)
		})
		s.adminRoutes(r)
	})
	r.Mount("/static", http.FileServer(http.Dir("web")))
	api := chi.NewRouter()
	api.Get(pingPath, s.ping)
	r.Mount("/v2", api)
	http.HandleFunc("GET /legacy/{name}", legacy)
	return r
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("API_KEY") == "" {
		w.WriteHeader(500)
	}
	_, debug := os.LookupEnv("DEBUG")
	_ = debug
}

func (s *Server) handleUser(w http.ResponseWriter, r *http.Request) {
	_ = s.store.GetUser(1)
	helper()
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) { helper() }
func (s *Server) listOrders(w http.ResponseWriter, r *http.Request)   {}
func (s *Server) deleteOrder(w http.ResponseWriter, r *http.Request)  {}
func (s *Server) ping(w http.ResponseWriter, r *http.Request)         {}

func legacy(w http.ResponseWriter, r *http.Request) {}

func helper() {}
