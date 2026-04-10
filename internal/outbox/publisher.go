package outbox

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/akampuraa-cmd/Nkore-Bank/internal/platform/database"
)

const (
	pollInterval   = 5 * time.Second
	maxRetryCount  = 5
	publishBatch   = 50
)

// Publisher polls the outbox_events table and "publishes" unpublished events.
type Publisher struct {
	db   *database.DB
	done chan struct{}
}

// NewPublisher creates a new outbox publisher.
func NewPublisher(db *database.DB) *Publisher {
	return &Publisher{
		db:   db,
		done: make(chan struct{}),
	}
}

// Start begins the polling loop. It blocks until Stop is called or the context
// is cancelled.
func (p *Publisher) Start(ctx context.Context) error {
	slog.Info("outbox publisher started", "poll_interval", pollInterval.String())

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("outbox publisher stopping: context cancelled")
			return ctx.Err()
		case <-p.done:
			slog.Info("outbox publisher stopping: stop signal received")
			return nil
		case <-ticker.C:
			if err := p.poll(ctx); err != nil {
				slog.Error("outbox publisher poll error", "error", err)
			}
		}
	}
}

// Stop signals the publisher to shut down gracefully.
func (p *Publisher) Stop() {
	close(p.done)
}

// poll fetches unpublished events and processes them one at a time.
func (p *Publisher) poll(ctx context.Context) error {
	query := `SELECT id, aggregate_type, aggregate_id, event_type, payload, created_at, retry_count
		FROM outbox_events
		WHERE published_at IS NULL AND retry_count <= $1
		ORDER BY created_at ASC
		LIMIT $2`

	rows, err := p.db.Pool.QueryContext(ctx, query, maxRetryCount, publishBatch)
	if err != nil {
		return fmt.Errorf("poll: query: %w", err)
	}
	defer rows.Close()

	var events []OutboxEvent
	for rows.Next() {
		var e OutboxEvent
		if err := rows.Scan(
			&e.ID, &e.AggregateType, &e.AggregateID,
			&e.EventType, &e.Payload, &e.CreatedAt, &e.RetryCount,
		); err != nil {
			return fmt.Errorf("poll: scan: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("poll: iterate: %w", err)
	}

	for i := range events {
		if err := p.publish(ctx, &events[i]); err != nil {
			slog.Error("outbox publish failed",
				"event_id", events[i].ID,
				"event_type", events[i].EventType,
				"error", err,
			)
			// Increment retry count on failure.
			if retryErr := p.incrementRetry(ctx, events[i].ID); retryErr != nil {
				slog.Error("outbox increment retry failed", "event_id", events[i].ID, "error", retryErr)
			}
			continue
		}
	}

	return nil
}

// publish simulates sending the event to a message broker (e.g. Kafka) and
// marks it as published.
func (p *Publisher) publish(ctx context.Context, event *OutboxEvent) error {
	// Simulate publishing (log the event as if it were sent to Kafka).
	slog.Info("outbox event published",
		"event_id", event.ID,
		"aggregate_type", event.AggregateType,
		"aggregate_id", event.AggregateID,
		"event_type", event.EventType,
	)

	// Mark as published.
	now := time.Now().UTC()
	query := `UPDATE outbox_events SET published_at = $1 WHERE id = $2`
	_, err := p.db.Pool.ExecContext(ctx, query, now, event.ID)
	if err != nil {
		return fmt.Errorf("publish: mark published: %w", err)
	}

	return nil
}

// incrementRetry bumps the retry_count for a failed event.
func (p *Publisher) incrementRetry(ctx context.Context, eventID string) error {
	query := `UPDATE outbox_events SET retry_count = retry_count + 1 WHERE id = $1`
	_, err := p.db.Pool.ExecContext(ctx, query, eventID)
	if err != nil {
		return fmt.Errorf("increment retry: %w", err)
	}
	return nil
}
