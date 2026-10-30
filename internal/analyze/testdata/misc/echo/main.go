package main

import "github.com/labstack/echo/v4"

func main() {
	e := echo.New()
	g := e.Group("/admin")
	g.DELETE("/users/:id", del)
	e.GET("/", home)
}

func del(c echo.Context) error  { return nil }
func home(c echo.Context) error { return nil }
