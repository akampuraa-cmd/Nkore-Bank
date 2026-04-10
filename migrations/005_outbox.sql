-- Migration 005: Outbox Pattern & Audit Log Schema
-- Nkore Bank Core Banking System
--
-- Banking principles enforced:
--   - Transactional outbox for reliable event publishing
--   - Full audit trail with actor, IP, trace ID, and before/after state

-- ============================================================
-- outbox_events — transactional outbox for event-driven messaging
-- ============================================================

CREATE TABLE outbox_events (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_type  VARCHAR(100)    NOT NULL,
    aggregate_id    UUID            NOT NULL,
    event_type      VARCHAR(100)    NOT NULL,
    payload         JSONB           NOT NULL,
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT now(),
    published_at    TIMESTAMPTZ,                        -- NULL until published
    retry_count     INTEGER         NOT NULL DEFAULT 0
                        CHECK (retry_count >= 0)
);

COMMENT ON TABLE  outbox_events IS 'Transactional outbox — polled by event relay for at-least-once delivery';
COMMENT ON COLUMN outbox_events.published_at IS 'NULL = unpublished; set by event relay after successful delivery';

-- ============================================================
-- audit_log — immutable record of every state change
-- ============================================================

CREATE TABLE audit_log (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_type VARCHAR(100)    NOT NULL,
    entity_id   UUID            NOT NULL,
    action      VARCHAR(50)     NOT NULL,
    actor_id    UUID            NOT NULL,
    actor_ip    INET,
    trace_id    UUID,
    old_value   JSONB,
    new_value   JSONB,
    created_at  TIMESTAMPTZ     NOT NULL DEFAULT now()
);

COMMENT ON TABLE audit_log IS 'INSERT-ONLY — rows must never be updated or deleted';

-- Prevent UPDATE and DELETE on audit_log
CREATE OR REPLACE FUNCTION fn_audit_log_immutable()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is immutable. UPDATE and DELETE are prohibited.';
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_audit_log_no_update
    BEFORE UPDATE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION fn_audit_log_immutable();

CREATE TRIGGER trg_audit_log_no_delete
    BEFORE DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION fn_audit_log_immutable();

-- ============================================================
-- Indexes
-- ============================================================

-- Outbox: poll for unpublished events
CREATE INDEX idx_outbox_unpublished ON outbox_events (created_at)
    WHERE published_at IS NULL;

CREATE INDEX idx_outbox_aggregate   ON outbox_events (aggregate_type, aggregate_id);
CREATE INDEX idx_outbox_event_type  ON outbox_events (event_type);

-- Audit log: lookup by entity
CREATE INDEX idx_audit_entity       ON audit_log (entity_type, entity_id, created_at);
CREATE INDEX idx_audit_actor        ON audit_log (actor_id, created_at);
CREATE INDEX idx_audit_trace        ON audit_log (trace_id)
    WHERE trace_id IS NOT NULL;
CREATE INDEX idx_audit_created_at   ON audit_log (created_at);
