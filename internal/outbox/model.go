package outbox

import (
	"encoding/json"
	"time"
)

type OutboxEvent struct {
	ID            string
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       json.RawMessage
	CreatedAt     time.Time
	PublishedAt   *time.Time
	RetryCount    int
}

type AuditLog struct {
	ID         string
	EntityType string
	EntityID   string
	Action     string
	ActorID    string
	ActorIP    string
	TraceID    string
	OldValue   json.RawMessage
	NewValue   json.RawMessage
	CreatedAt  time.Time
}
