package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/akampuraa-cmd/Nkore-Bank/internal/config"
	"github.com/akampuraa-cmd/Nkore-Bank/internal/platform/cache"
)

// RateLimit returns middleware that enforces a sliding-window rate limit using
// Redis. Authenticated requests are keyed by user_id; unauthenticated
// requests are keyed by source IP.
func RateLimit(cfg *config.Config, c *cache.Client) func(http.Handler) http.Handler {
	limit := cfg.RateLimitRequests
	window := time.Duration(cfg.RateLimitWindowSec) * time.Second

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var key string
			if uid := UserIDFromContext(r.Context()); uid != "" {
				key = "rl:user:" + uid
			} else {
				key = "rl:ip:" + sourceIPFromRequest(r)
			}

			allowed, err := c.AllowRequest(r.Context(), key, limit, window)
			if err != nil {
				// On Redis failure, allow the request rather than blocking traffic.
				next.ServeHTTP(w, r)
				return
			}

			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(cfg.RateLimitWindowSec))
				http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
