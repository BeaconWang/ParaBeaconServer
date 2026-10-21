package middleware

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type clientWindow struct {
	startedAt time.Time
	count     int
}

type FixedWindowLimiter struct {
	mu         sync.Mutex
	clients    map[string]clientWindow
	limit      int
	window     time.Duration
	maxClients int
	now        func() time.Time
}

func NewFixedWindowLimiter(limit int, window time.Duration, maxClients int) *FixedWindowLimiter {
	return &FixedWindowLimiter{
		clients:    make(map[string]clientWindow),
		limit:      limit,
		window:     window,
		maxClients: maxClients,
		now:        time.Now,
	}
}

func (l *FixedWindowLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		allowed, retryAfter := l.allow(c.ClientIP())
		if !allowed {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "too many requests"})
			return
		}
		c.Next()
	}
}

func (l *FixedWindowLimiter) allow(clientIP string) (bool, int) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, exists := l.clients[clientIP]
	if !exists {
		if len(l.clients) >= l.maxClients {
			for ip, candidate := range l.clients {
				if !now.Before(candidate.startedAt.Add(l.window)) {
					delete(l.clients, ip)
				}
			}
			if len(l.clients) >= l.maxClients {
				return false, int(l.window.Seconds())
			}
		}
		l.clients[clientIP] = clientWindow{startedAt: now, count: 1}
		return true, 0
	}

	resetAt := entry.startedAt.Add(l.window)
	if !now.Before(resetAt) {
		l.clients[clientIP] = clientWindow{startedAt: now, count: 1}
		return true, 0
	}
	if entry.count >= l.limit {
		retryAfter := int(resetAt.Sub(now).Seconds())
		if retryAfter < 1 {
			retryAfter = 1
		}
		return false, retryAfter
	}
	entry.count++
	l.clients[clientIP] = entry
	return true, 0
}
