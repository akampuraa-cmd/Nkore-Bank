// Package middleware provides HTTP middleware for Nkore Bank.
package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"github.com/akampuraa-cmd/Nkore-Bank/internal/config"
)

type contextKey int

const (
	ctxKeyUserID contextKey = iota
	ctxKeyRoles
)

// Claims represents the JWT claims used by Nkore Bank.
type Claims struct {
	jwt.RegisteredClaims
	Roles []string `json:"roles"`
}

// Auth returns middleware that validates a Bearer JWT token on every request.
// It extracts the user_id (sub) and roles claims and stores them in the
// request context.
func Auth(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if header == "" || !strings.HasPrefix(header, "Bearer ") {
				http.Error(w, `{"error":"missing or malformed authorization header"}`, http.StatusUnauthorized)
				return
			}

			tokenStr := strings.TrimPrefix(header, "Bearer ")

			claims := &Claims{}
			token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, jwt.ErrSignatureInvalid
				}
				return []byte(cfg.JWTSecret), nil
			},
				jwt.WithIssuer(cfg.JWTIssuer),
				jwt.WithExpirationRequired(),
				jwt.WithValidMethods([]string{"HS256"}),
			)
			if err != nil || !token.Valid {
				http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
				return
			}

			sub, err := claims.GetSubject()
			if err != nil || sub == "" {
				http.Error(w, `{"error":"token missing subject"}`, http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), ctxKeyUserID, sub)
			ctx = context.WithValue(ctx, ctxKeyRoles, claims.Roles)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserIDFromContext returns the authenticated user ID, or empty string if absent.
func UserIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyUserID).(string)
	return v
}

// RolesFromContext returns the authenticated user's roles, or nil if absent.
func RolesFromContext(ctx context.Context) []string {
	v, _ := ctx.Value(ctxKeyRoles).([]string)
	return v
}
