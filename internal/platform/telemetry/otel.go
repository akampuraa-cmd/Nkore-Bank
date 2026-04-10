// Package telemetry provides OpenTelemetry tracing, Prometheus metrics, and
// structured logging for Nkore Bank services.
package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/prometheus"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// BankingMetrics holds Prometheus counters and histograms for core banking operations.
type BankingMetrics struct {
	TransactionCount      otelmetric.Int64Counter
	TransactionAmountTotal otelmetric.Float64Counter
	AccountOperations     otelmetric.Int64Counter
	ErrorCount            otelmetric.Int64Counter
}

// Telemetry bundles the tracer, metrics, and shutdown function.
type Telemetry struct {
	TracerProvider *trace.TracerProvider
	Metrics        *BankingMetrics
	Logger         *slog.Logger
	shutdown       func(context.Context) error
}

// Init initializes OpenTelemetry tracing (OTLP/gRPC), Prometheus metrics,
// and structured logging. Call Shutdown when the application exits.
func Init(ctx context.Context, serviceName string) (*Telemetry, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: resource: %w", err)
	}

	// --- Tracer (OTLP/gRPC) ---
	traceExporter, err := otlptracegrpc.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("telemetry: trace exporter: %w", err)
	}

	tp := trace.NewTracerProvider(
		trace.WithBatcher(traceExporter),
		trace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	// --- Metrics (Prometheus) ---
	promExporter, err := prometheus.New()
	if err != nil {
		return nil, fmt.Errorf("telemetry: prometheus exporter: %w", err)
	}

	mp := metric.NewMeterProvider(
		metric.WithReader(promExporter),
		metric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	meter := mp.Meter(serviceName)
	metrics, err := registerBankingMetrics(meter)
	if err != nil {
		return nil, fmt.Errorf("telemetry: register metrics: %w", err)
	}

	// --- Structured logging ---
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	shutdown := func(ctx context.Context) error {
		if err := tp.Shutdown(ctx); err != nil {
			return fmt.Errorf("telemetry: tracer shutdown: %w", err)
		}
		if err := mp.Shutdown(ctx); err != nil {
			return fmt.Errorf("telemetry: meter shutdown: %w", err)
		}
		return nil
	}

	return &Telemetry{
		TracerProvider: tp,
		Metrics:        metrics,
		Logger:         logger,
		shutdown:       shutdown,
	}, nil
}

// Shutdown flushes telemetry data and releases resources.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	return t.shutdown(ctx)
}

// StartSpan creates a new span with standard banking attributes.
func StartSpan(ctx context.Context, tracer oteltrace.Tracer, name string, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	ctx, span := tracer.Start(ctx, name, oteltrace.WithAttributes(attrs...))
	return ctx, span
}

func registerBankingMetrics(meter otelmetric.Meter) (*BankingMetrics, error) {
	txCount, err := meter.Int64Counter("transaction_count",
		otelmetric.WithDescription("Total number of banking transactions processed"),
	)
	if err != nil {
		return nil, err
	}

	txAmount, err := meter.Float64Counter("transaction_amount_total",
		otelmetric.WithDescription("Cumulative monetary amount of transactions"),
	)
	if err != nil {
		return nil, err
	}

	acctOps, err := meter.Int64Counter("account_operations",
		otelmetric.WithDescription("Total number of account operations (create, update, close)"),
	)
	if err != nil {
		return nil, err
	}

	errCount, err := meter.Int64Counter("error_count",
		otelmetric.WithDescription("Total number of errors"),
	)
	if err != nil {
		return nil, err
	}

	return &BankingMetrics{
		TransactionCount:       txCount,
		TransactionAmountTotal: txAmount,
		AccountOperations:      acctOps,
		ErrorCount:             errCount,
	}, nil
}
