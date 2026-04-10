package middleware

import (
	"net/http"
	"strings"
)

// CORSConfig holds the allowed origins for CORS. An empty slice means no
// origin is allowed (safe default for production).
type CORSConfig struct {
	AllowedOrigins []string
}

// CORS returns middleware that sets standard CORS headers and handles
// preflight OPTIONS requests.
func CORS(cfg CORSConfig) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		allowed[o] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			if origin != "" {
				if _, ok := allowed[origin]; ok {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Vary", "Origin")
				}
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-Request-ID")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Max-Age", "86400")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// CORSFromEnv is a helper that parses a comma-separated list of origins
// (e.g. "https://app.nkorebank.com,https://admin.nkorebank.com") into a
// CORSConfig.
func CORSFromEnv(origins string) CORSConfig {
	if origins == "" {
		return CORSConfig{}
	}
	parts := strings.Split(origins, ",")
	trimmed := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			trimmed = append(trimmed, s)
		}
	}
	return CORSConfig{AllowedOrigins: trimmed}
}
