package main

import (
	"net/http"
	"os"

	"example.com/shop/internal/server"
)

func main() {
	s := server.New(env("DB_PATH", "shop.db"))
	_ = http.ListenAndServe(env("ADDR", ":8080"), s.Router())
}

// env reads a variable with a default.
func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
