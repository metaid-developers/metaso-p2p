package api

import (
	"time"

	"github.com/gin-gonic/gin"
)

const requestStartTimeKey = "metaso.requestStartTime"

func RequestTimingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(requestStartTimeKey, time.Now())
		c.Next()
	}
}

func processingTimeMillis(c *gin.Context) int64 {
	if c != nil {
		if started, ok := c.Get(requestStartTimeKey); ok {
			if startTime, ok := started.(time.Time); ok && !startTime.IsZero() {
				elapsed := time.Since(startTime).Milliseconds()
				if elapsed > 0 {
					return elapsed
				}
			}
		}
	}

	return 1
}
