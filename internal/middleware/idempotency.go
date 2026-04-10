package middleware

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

	"github.com/akampuraa-cmd/Nkore-Bank/internal/platform/cache"
)

const idempotencyTTL = 24 * time.Hour

// cachedResponse is the structure stored in Redis for an idempotent response.
type cachedResponse struct {
	StatusCode int    `json:"status_code"`
	Body       string `json:"body"`
}

// responseRecorder captures the response so it can be cached.
type responseRecorder struct {
	http.ResponseWriter
	statusCode int
	body       bytes.Buffer
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.statusCode = code
	rr.ResponseWriter.WriteHeader(code)
}

func (rr *responseRecorder) Write(b []byte) (int, error) {
	rr.body.Write(b)
	return rr.ResponseWriter.Write(b)
}

// Idempotency returns middleware that enforces idempotency for mutating
// HTTP methods (POST, PUT, PATCH). It requires an Idempotency-Key header,
// checks Redis for a cached response, and caches new responses for 24 hours.
func Idempotency(c *cache.Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isMutating(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				http.Error(w, `{"error":"Idempotency-Key header is required for mutating requests"}`, http.StatusBadRequest)
				return
			}

			cacheKey := "idempotency:" + key

			// Check for a previously cached response.
			val, err := c.Get(r.Context(), cacheKey)
			if err != nil {
				http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
				return
			}
			if val != "" {
				var cr cachedResponse
				if err := json.Unmarshal([]byte(val), &cr); err == nil {
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("X-Idempotent-Replay", "true")
					w.WriteHeader(cr.StatusCode)
					w.Write([]byte(cr.Body))
					return
				}
			}

			rec := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
			next.ServeHTTP(rec, r)

			cr := cachedResponse{
				StatusCode: rec.statusCode,
				Body:       rec.body.String(),
			}
			encoded, err := json.Marshal(cr)
			if err == nil {
				// Best-effort cache; failure is not fatal.
				_ = c.Set(r.Context(), cacheKey, string(encoded), idempotencyTTL)
			}
		})
	}
}

func isMutating(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch
}
