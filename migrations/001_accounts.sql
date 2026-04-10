-- Migration 001: Account Management Schema
-- Nkore Bank Core Banking System
--
-- Banking principles enforced:
--   - DECIMAL(19,4) for all monetary amounts (NEVER float)
--   - All PII columns encrypted at rest (marked _enc / _hash)
--   - Parameterized queries only (no dynamic SQL)
--   - Full audit trail via version + timestamps

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ============================================================
-- ENUM types for account domain
-- ============================================================

CREATE TYPE account_type AS ENUM ('DDA', 'SAVINGS', 'LOAN');

CREATE TYPE account_status AS ENUM ('ACTIVE', 'FROZEN', 'CLOSED', 'DORMANT');

CREATE TYPE kyc_status AS ENUM (
    'PENDING',
    'VERIFIED',
    'FAILED',
    'EXPIRED',
    'REVIEW'
);

CREATE TYPE hold_type AS ENUM (
    'REGULATORY',
    'LEGAL',
    'ADMINISTRATIVE',
    'PENDING_TRANSACTION'
);

-- ============================================================
-- customers — personally identifiable information encrypted
-- ============================================================

CREATE TABLE customers (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    first_name_enc  BYTEA       NOT NULL,   -- AES-256-GCM encrypted PII
    last_name_enc   BYTEA       NOT NULL,   -- AES-256-GCM encrypted PII
    email_enc       BYTEA       NOT NULL,   -- AES-256-GCM encrypted PII
    phone_enc       BYTEA       NOT NULL,   -- AES-256-GCM encrypted PII
    ssn_hash        BYTEA       NOT NULL,   -- HMAC-SHA256 one-way hash (never decrypted)
    kyc_status      kyc_status  NOT NULL DEFAULT 'PENDING',
    risk_score      SMALLINT    NOT NULL DEFAULT 0
                        CHECK (risk_score BETWEEN 0 AND 100),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON COLUMN customers.first_name_enc IS 'PII — AES-256-GCM encrypted';
COMMENT ON COLUMN customers.last_name_enc  IS 'PII — AES-256-GCM encrypted';
COMMENT ON COLUMN customers.email_enc      IS 'PII — AES-256-GCM encrypted';
COMMENT ON COLUMN customers.phone_enc      IS 'PII — AES-256-GCM encrypted';
COMMENT ON COLUMN customers.ssn_hash       IS 'PII — HMAC-SHA256, non-reversible';

-- ============================================================
-- accounts
-- ============================================================

CREATE TABLE accounts (
    id              UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    account_number  VARCHAR(34)     NOT NULL UNIQUE, -- supports IBAN length
    customer_id     UUID            NOT NULL REFERENCES customers (id),
    account_type    account_type    NOT NULL,
    currency        CHAR(3)         NOT NULL DEFAULT 'UGX',
    status          account_status  NOT NULL DEFAULT 'ACTIVE',
    daily_limit     DECIMAL(19,4)   NOT NULL DEFAULT 0.0000
                        CHECK (daily_limit >= 0),
    version         INTEGER         NOT NULL DEFAULT 1, -- optimistic locking
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ     NOT NULL DEFAULT now()
);

COMMENT ON COLUMN accounts.version IS 'Optimistic concurrency control — increment on every update';

-- ============================================================
-- account_holds — encumbrances that reduce available balance
-- ============================================================

CREATE TABLE account_holds (
    id          UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id  UUID            NOT NULL REFERENCES accounts (id),
    hold_type   hold_type       NOT NULL,
    amount      DECIMAL(19,4)   NOT NULL CHECK (amount > 0),
    expires_at  TIMESTAMPTZ,
    released_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ     NOT NULL DEFAULT now()
);

-- ============================================================
-- Indexes
-- ============================================================

CREATE INDEX idx_customers_kyc_status   ON customers (kyc_status);
CREATE INDEX idx_customers_ssn_hash     ON customers (ssn_hash);
CREATE INDEX idx_customers_created_at   ON customers (created_at);

CREATE INDEX idx_accounts_customer_id   ON accounts (customer_id);
CREATE INDEX idx_accounts_account_type  ON accounts (account_type);
CREATE INDEX idx_accounts_status        ON accounts (status);
CREATE INDEX idx_accounts_currency      ON accounts (currency);

CREATE INDEX idx_holds_account_id       ON account_holds (account_id);
CREATE INDEX idx_holds_expires_at       ON account_holds (expires_at)
    WHERE released_at IS NULL;
CREATE INDEX idx_holds_active           ON account_holds (account_id)
    WHERE released_at IS NULL AND (expires_at IS NULL OR expires_at > now());
