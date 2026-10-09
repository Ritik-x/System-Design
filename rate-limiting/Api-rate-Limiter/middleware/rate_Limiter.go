package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func RateLimiter(rdb *redis.Client, limit int64, window time.Duration) gin.HandlerFunc {
	script := redis.NewScript(`
		local count = redis.call("INCR", KEYS[1])

		if count == 1 then
			redis.call("PEXPIRE", KEYS[1], ARGV[1])
		end

		return count
	`)

	return func(c *gin.Context) {
		if limit <= 0 || window <= 0 {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "invalid rate limiter configuration",
			})
			c.Abort()
			return
		}

		clientID := c.ClientIP()
		now := time.Now()

		windowStart := now.Truncate(window)
		windowID := windowStart.UnixNano()

		key := fmt.Sprintf("rate:%s:%d", clientID, windowID)

		ttl := time.Until(windowStart.Add(window))
		ttlMillis := ttl.Milliseconds()
		if ttlMillis < 1 {
			ttlMillis = 1
		}

		ctx, cancel := context.WithTimeout(
			c.Request.Context(),
			100*time.Millisecond,
		)
		defer cancel()

		count, err := script.Run(
			ctx,
			rdb,
			[]string{key},
			ttlMillis,
		).Int64()

		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error": "rate limiter unavailable",
			})
			c.Abort()
			return
		}

		remaining := max(int64(0), limit-count)

		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))

		if count > limit {

			ttl, err := rdb.PTTL(ctx, key).Result()

			if err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{
					"error": "rate limiter unavailable",
				})
				c.Abort()
				return
			}

			retryAfter := int64(1)
			if ttl > 0 {
				retryAfter = int64((ttl + time.Second - 1) / time.Second)
			}

			c.Header("Retry-After", fmt.Sprintf("%d", retryAfter))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":       "rate limit exceeded",
				"retry_after": retryAfter,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

const slidingWindowScript = `
local key = KEYS[1]
local now = tonumber(ARGV[1])
local cutoff = now - tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local member = ARGV[4]
local window = tonumber(ARGV[2])

-- 1. Remove requests outside the rolling window.
redis.call("ZREMRANGEBYSCORE", key, "-inf", cutoff)

-- 2. Count requests still inside the window.
local count = redis.call("ZCARD", key)

-- 3. Reject if the limit has already been reached.
if count >= limit then
    local oldest = redis.call("ZRANGE", key, 0, 0, "WITHSCORES")
    local retry_ms = window

    if oldest[2] then
        retry_ms = tonumber(oldest[2]) + window - now
    end

    return cjson.encode({
        allowed = false,
        count = count,
        retry_ms = math.max(1, retry_ms)
    })
end

-- 4. Record this request with its timestamp.
redis.call("ZADD", key, now, member)

-- 5. Expire the key after inactivity.
redis.call("PEXPIRE", key, window)

return cjson.encode({
    allowed = true,
    count = count + 1,
    retry_ms = 0
})
`

type slidingWindowResult struct {
	Allowed bool  `json:"allowed"`
	Count   int64 `json:"count"`
	RetryMS int64 `json:"retry_ms"`
}
