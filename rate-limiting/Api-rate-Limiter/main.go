package main

import (
	"api-rate-limiter/middleware"
	"context"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func main() {
	ctx := context.Background()
	// create the redis connection
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Redis connection failed: %v", err)
	}

	router := gin.Default()
	router.GET(
		"/api/data",
		middleware.RateLimiter(rdb, 5, time.Minute),
		func(c *gin.Context) {
			c.JSON(200, gin.H{"message": "request allowed"})
		},
	)
	router.GET(
		"/api/sliding-data",
		middleware.SlidingWindowRateLimiter(rdb, 5, time.Minute),
		func(c *gin.Context) {
			c.JSON(200, gin.H{
				"message": "request allowed",
			})
		},
	)
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	log.Fatal(router.Run(":8080"))

}
