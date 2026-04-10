package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/akampuraa-cmd/Nkore-Bank/internal/platform/database"
)

// auditEntry holds the information needed to write an audit row.
type auditEntry struct {
	EntityType string
	Action     string
	ActorID    string
	ActorIP    string
	TraceID    string
}

// AuditLogger processes audit entries asynchronously via a buffered channel.
type AuditLogger struct {
	db   *database.DB
	ch   chan auditEntry
	done chan struct{}
}

// NewAuditLogger creates an AuditLogger with the given buffer size and starts
// a background goroutine that drains the channel into PostgreSQL.
func NewAuditLogger(db *database.DB, bufSize int) *AuditLogger {
	if bufSize <= 0 {
		bufSize = 256
	}
	al := &AuditLogger{
		db:   db,
		ch:   make(chan auditEntry, bufSize),
		done: make(chan struct{}),
	}
	go al.drain()
	return al
}

// Close signals the drain goroutine to stop and waits for it to finish.
func (al *AuditLogger) Close() {
	close(al.ch)
	<-al.done
}

func (al *AuditLogger) drain() {
	defer close(al.done)
	for entry := range al.ch {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := al.db.Pool.ExecContext(ctx,
			`INSERT INTO audit_log (entity_type, action, actor_id, actor_ip, trace_id) VALUES ($1, $2, $3, $4, $5)`,
			entry.EntityType, entry.Action, entry.ActorID, entry.ActorIP, entry.TraceID,
		)
		if err != nil {
			slog.Error("audit: failed to write audit log", "error", err, "entry", entry)
		}
		cancel()
	}
}

// Middleware returns an HTTP middleware that logs every request using slog and,
// for state-mutating methods, asynchronously writes to the audit_log table.
func (al *AuditLogger) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap the writer to capture the status code.
			sw := &statusWriter{ResponseWriter: w, statusCode: http.StatusOK}
			next.ServeHTTP(sw, r)

			latency := time.Since(start)

			userID := UserIDFromContext(r.Context())
			traceID := traceIDFromContext(r.Context())
			sourceIP := sourceIPFromRequest(r)

			slog.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.statusCode,
				"latency_ms", latency.Milliseconds(),
				"user_id", userID,
				"source_ip", sourceIP,
				"trace_id", traceID,
			)

			if isStateMutating(r.Method) {
				entry := auditEntry{
					EntityType: entityTypeFromPath(r.URL.Path),
					Action:     r.Method,
					ActorID:    userID,
					ActorIP:    sourceIP,
					TraceID:    traceID,
				}
				// Non-blocking send; drop the entry if the channel is full
				// rather than blocking the response.
				select {
				case al.ch <- entry:
				default:
					slog.Warn("audit: channel full, dropping audit entry",
						"path", r.URL.Path, "method", r.Method)
				}
			}
		})
	}
}

// statusWriter wraps http.ResponseWriter to capture the response status code.
type statusWriter struct {
	http.ResponseWriter
	statusCode int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.statusCode = code
	sw.ResponseWriter.WriteHeader(code)
}

func traceIDFromContext(ctx context.Context) string {
	sc := trace.SpanFromContext(ctx).SpanContext()
	if sc.HasTraceID() {
		return sc.TraceID().String()
	}
	return ""
}

func sourceIPFromRequest(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if parts := strings.SplitN(xff, ",", 2); len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	// Fall back to remote address, stripping the port.
	if idx := strings.LastIndex(r.RemoteAddr, ":"); idx != -1 {
		return r.RemoteAddr[:idx]
	}
	return r.RemoteAddr
}

func isStateMutating(method string) bool {
	return method == http.MethodPost || method == http.MethodPut ||
		method == http.MethodPatch || method == http.MethodDelete
}

// entityTypeFromPath extracts the first non-version path segment as the entity type.
// For example, "/v1/accounts/123" returns "accounts".
func entityTypeFromPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for _, p := range parts {
		if p == "" || strings.HasPrefix(p, "v") && len(p) <= 3 {
			continue
		}
		return p
	}
	return "unknown"
}
