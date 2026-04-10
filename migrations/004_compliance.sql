-- Migration 004: Compliance / AML Schema
-- Nkore Bank Core Banking System
--
-- Banking principles enforced:
--   - DECIMAL(19,4) for all monetary amounts (NEVER float)
--   - Full audit trail for regulatory compliance
--   - CTR/SAR/velocity/structuring alert tracking
--   - OFAC / sanctions watchlist screening

-- pg_trgm required for fuzzy name matching on watchlists
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- ============================================================
-- ENUM types for compliance
-- ============================================================

CREATE TYPE aml_alert_type AS ENUM (
    'CTR',            -- Currency Transaction Report
    'SAR',            -- Suspicious Activity Report
    'VELOCITY',       -- Unusual transaction velocity
    'STRUCTURING'     -- Deliberate structuring to avoid reporting
);

CREATE TYPE aml_severity AS ENUM (
    'LOW',
    'MEDIUM',
    'HIGH',
    'CRITICAL'
);

CREATE TYPE aml_alert_status AS ENUM (
    'OPEN',
    'INVESTIGATING',
    'ESCALATED',
    'CLOSED',
    'FILED'
);

CREATE TYPE watchlist_source AS ENUM (
    'OFAC',
    'UN_SANCTIONS',
    'EU_SANCTIONS',
    'PEP',
    'INTERNAL'
);

CREATE TYPE watchlist_entry_status AS ENUM (
    'ACTIVE',
    'INACTIVE',
    'EXPIRED'
);

-- ============================================================
-- aml_alerts — anti-money-laundering alerts
-- ============================================================

CREATE TABLE aml_alerts (
    id                  UUID                PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id          UUID                NOT NULL REFERENCES accounts (id),
    alert_type          aml_alert_type      NOT NULL,
    severity            aml_severity        NOT NULL DEFAULT 'MEDIUM',
    transaction_ids     UUID[]              NOT NULL DEFAULT '{}',
    total_amount        DECIMAL(19,4)       NOT NULL CHECK (total_amount >= 0),
    status              aml_alert_status    NOT NULL DEFAULT 'OPEN',
    assigned_to         UUID,
    notes               TEXT,
    created_at          TIMESTAMPTZ         NOT NULL DEFAULT now(),
    resolved_at         TIMESTAMPTZ,
    updated_at          TIMESTAMPTZ         NOT NULL DEFAULT now()
);

COMMENT ON COLUMN aml_alerts.transaction_ids IS 'Array of transaction UUIDs that triggered this alert';

-- ============================================================
-- transaction_velocity — sliding-window velocity tracking
-- ============================================================

CREATE TABLE transaction_velocity (
    id              UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id      UUID            NOT NULL REFERENCES accounts (id),
    window_start    TIMESTAMPTZ     NOT NULL,
    window_end      TIMESTAMPTZ     NOT NULL,
    tx_count        INTEGER         NOT NULL CHECK (tx_count >= 0),
    total_amount    DECIMAL(19,4)   NOT NULL CHECK (total_amount >= 0),
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT now(),

    CONSTRAINT chk_velocity_window CHECK (window_end > window_start)
);

-- ============================================================
-- watchlist_entries — OFAC / sanctions / PEP screening
-- ============================================================

CREATE TABLE watchlist_entries (
    id                  UUID                    PRIMARY KEY DEFAULT gen_random_uuid(),
    source              watchlist_source        NOT NULL,
    status              watchlist_entry_status  NOT NULL DEFAULT 'ACTIVE',
    entity_name         VARCHAR(500)            NOT NULL,
    entity_type         VARCHAR(50)             NOT NULL DEFAULT 'INDIVIDUAL',
    country_code        CHAR(2),
    identifiers         JSONB                   NOT NULL DEFAULT '{}',
    aliases             TEXT[]                  NOT NULL DEFAULT '{}',
    notes               TEXT,
    effective_from      DATE                    NOT NULL DEFAULT CURRENT_DATE,
    effective_to        DATE,
    created_at          TIMESTAMPTZ             NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ             NOT NULL DEFAULT now()
);

COMMENT ON COLUMN watchlist_entries.identifiers IS 'JSON object with ID type keys, e.g. {"passport":"X12345","national_id":"N67890"}';

-- ============================================================
-- watchlist_hits — matches found during screening
-- ============================================================

CREATE TABLE watchlist_hits (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    watchlist_entry_id  UUID        NOT NULL REFERENCES watchlist_entries (id),
    customer_id         UUID        NOT NULL REFERENCES customers (id),
    match_score         DECIMAL(5,4)    NOT NULL CHECK (match_score BETWEEN 0 AND 1),
    match_details       JSONB       NOT NULL DEFAULT '{}',
    reviewed            BOOLEAN     NOT NULL DEFAULT FALSE,
    reviewed_by         UUID,
    reviewed_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- Indexes for compliance reporting
-- ============================================================

-- AML alerts
CREATE INDEX idx_aml_alerts_account_id      ON aml_alerts (account_id);
CREATE INDEX idx_aml_alerts_status          ON aml_alerts (status);
CREATE INDEX idx_aml_alerts_type            ON aml_alerts (alert_type);
CREATE INDEX idx_aml_alerts_severity        ON aml_alerts (severity);
CREATE INDEX idx_aml_alerts_created_at      ON aml_alerts (created_at);
CREATE INDEX idx_aml_alerts_assigned_to     ON aml_alerts (assigned_to)
    WHERE assigned_to IS NOT NULL;
CREATE INDEX idx_aml_alerts_open            ON aml_alerts (status, severity)
    WHERE status IN ('OPEN', 'INVESTIGATING', 'ESCALATED');

-- Transaction velocity
CREATE INDEX idx_velocity_account_id        ON transaction_velocity (account_id);
CREATE INDEX idx_velocity_window            ON transaction_velocity (account_id, window_start, window_end);

-- Watchlist
CREATE INDEX idx_watchlist_source           ON watchlist_entries (source);
CREATE INDEX idx_watchlist_status           ON watchlist_entries (status);
CREATE INDEX idx_watchlist_entity_name      ON watchlist_entries USING gin (entity_name gin_trgm_ops);
CREATE INDEX idx_watchlist_country          ON watchlist_entries (country_code)
    WHERE country_code IS NOT NULL;

-- Watchlist hits
CREATE INDEX idx_watchlist_hits_entry       ON watchlist_hits (watchlist_entry_id);
CREATE INDEX idx_watchlist_hits_customer    ON watchlist_hits (customer_id);
CREATE INDEX idx_watchlist_hits_unreviewed  ON watchlist_hits (created_at)
    WHERE reviewed = FALSE;
