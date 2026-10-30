package main

import (
	"net/http"

	"github.com/gorilla/mux"
)

func main() {
	r := mux.NewRouter()
	r.HandleFunc("/articles/{id:[0-9]+}", article).Methods("GET", "PUT")
	api := r.PathPrefix("/api").Subrouter()
	api.HandleFunc("/items", items).Methods(http.MethodPost, "POST")
	_ = http.ListenAndServe(":8080", r)
}

func article(w http.ResponseWriter, r *http.Request) {}
func items(w http.ResponseWriter, r *http.Request)   {}
