// Command nkorebank is the entry point for the Nkore Bank Core Banking System.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/akampuraa-cmd/Nkore-Bank/internal/config"
	"github.com/akampuraa-cmd/Nkore-Bank/internal/domain/account"
	"github.com/akampuraa-cmd/Nkore-Bank/internal/domain/compliance"
	"github.com/akampuraa-cmd/Nkore-Bank/internal/domain/interest"
	"github.com/akampuraa-cmd/Nkore-Bank/internal/domain/ledger"
	"github.com/akampuraa-cmd/Nkore-Bank/internal/domain/transaction"
	"github.com/akampuraa-cmd/Nkore-Bank/internal/middleware"
	"github.com/akampuraa-cmd/Nkore-Bank/internal/outbox"
	"github.com/akampuraa-cmd/Nkore-Bank/internal/platform/cache"
	"github.com/akampuraa-cmd/Nkore-Bank/internal/platform/database"
	"github.com/akampuraa-cmd/Nkore-Bank/internal/platform/telemetry"
)

const version = "1.0.0"

func main() {
	if err := run(); err != nil {
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ── Configuration ──────────────────────────────────────────────────
	cfg := config.MustLoad()

	// ── Telemetry (tracing + metrics + logging) ────────────────────────
	tel, err := telemetry.Init(ctx, "nkore-bank")
	if err != nil {
		return fmt.Errorf("telemetry init: %w", err)
	}
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tel.Shutdown(shutCtx); err != nil {
			slog.Error("telemetry shutdown", "error", err)
		}
	}()

	// ── PostgreSQL ─────────────────────────────────────────────────────
	db, err := database.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("database connect: %w", err)
	}
	defer func() { _ = db.Close() }()

	// ── Redis ──────────────────────────────────────────────────────────
	redisClient, err := cache.New(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("redis connect: %w", err)
	}
	defer func() { _ = redisClient.Close() }()

	// ── Repositories ───────────────────────────────────────────────────
	acctRepo := account.NewRepository(db)
	txnRepo := transaction.NewRepository(db)
	ledgerRepo := ledger.NewRepository(db)

	// ── Services ───────────────────────────────────────────────────────
	acctSvc := account.NewService(acctRepo, db, redisClient)
	txnSvc := transaction.NewService(txnRepo, acctRepo, db, tel.Metrics)
	ledgerSvc := ledger.NewService(ledgerRepo, db)
	compSvc := compliance.NewService(db)
	intSvc := interest.NewService(db)

	// ── Seed GL chart of accounts ──────────────────────────────────────
	if err := ledgerSvc.SeedChartOfAccounts(ctx); err != nil {
		return fmt.Errorf("seed chart of accounts: %w", err)
	}

	// ── Handlers ───────────────────────────────────────────────────────
	acctHandler := account.NewHandler(acctSvc, tel.Metrics)
	txnHandler := transaction.NewHandler(txnSvc)
	ledgerHandler := ledger.NewHandler(ledgerSvc)
	compHandler := compliance.NewHandler(compSvc)
	intHandler := interest.NewHandler(intSvc)

	// ── HTTP Mux ───────────────────────────────────────────────────────
	apiMux := http.NewServeMux()
	acctHandler.RegisterRoutes(apiMux)
	txnHandler.RegisterRoutes(apiMux)
	ledgerHandler.RegisterRoutes(apiMux)
	compHandler.RegisterRoutes(apiMux)
	intHandler.RegisterRoutes(apiMux)

	// Auth + idempotency only on API routes.
	authedAPI := middleware.Auth(cfg)(
		middleware.Idempotency(redisClient)(apiMux),
	)

	// ── Top-level mux with health checks ───────────────────────────────
	root := http.NewServeMux()
	root.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	})
	root.HandleFunc("GET /ready", readinessHandler(db, redisClient))
	root.Handle("/", authedAPI)

	// Outer middleware chain: recovery → cors → ratelimit → audit → (handler)
	auditLogger := middleware.NewAuditLogger(db, 256)
	defer auditLogger.Close()

	handler := middleware.Recovery()(
		middleware.CORS(middleware.CORSConfig{AllowedOrigins: []string{"*"}})(
			middleware.RateLimit(cfg, redisClient)(
				auditLogger.Middleware()(root),
			),
		),
	)

	// ── Outbox publisher ───────────────────────────────────────────────
	pub := outbox.NewPublisher(db)
	go func() {
		if err := pub.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("outbox publisher", "error", err)
		}
	}()
	defer pub.Stop()

	// ── HTTP Server ────────────────────────────────────────────────────
	addr := ":" + cfg.ServerPort
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	slog.Info(fmt.Sprintf("Nkore Bank Core Banking System v%s started on port %s", version, addr))

	// Start server in a goroutine.
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	// Wait for shutdown signal or server error.
	select {
	case <-ctx.Done():
		slog.Info("shutdown signal received")
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		return fmt.Errorf("server shutdown: %w", err)
	}
	slog.Info("server stopped gracefully")
	return nil
}

func readinessHandler(db *database.DB, rc *cache.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		if err := db.HealthCheck(ctx); err != nil {
			http.Error(w, "database not ready", http.StatusServiceUnavailable)
			return
		}
		if err := rc.HealthCheck(ctx); err != nil {
			http.Error(w, "cache not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	}
}
