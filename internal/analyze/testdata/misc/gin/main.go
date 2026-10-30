package main

import "github.com/gin-gonic/gin"

func main() {
	r := gin.Default()
	r.GET("/ping", ping)
	v1 := r.Group("/api/v1")
	{
		v1.POST("/login", login)
		users := v1.Group("/users")
		users.GET("/:id", getUser)
		users.Any("", anyUser)
	}
	_ = r.Run()
}

func ping(c *gin.Context)    {}
func login(c *gin.Context)   {}
func getUser(c *gin.Context) {}
func anyUser(c *gin.Context) {}
