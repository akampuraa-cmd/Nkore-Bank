-- Migration 006: Interest Accrual Schema
-- Nkore Bank Core Banking System
--
-- Banking principles enforced:
--   - DECIMAL(19,4) for monetary amounts (NEVER float)
--   - DECIMAL(10,8) for interest rates (8 decimal places of precision)
--   - Day-count convention support (ACTUAL_365, THIRTY_360)
--   - Accrual-to-posting lifecycle tracked

-- ============================================================
-- ENUM types for interest
-- ============================================================

CREATE TYPE day_count_convention AS ENUM (
    'ACTUAL_365',
    'THIRTY_360'
);

-- ============================================================
-- interest_rates — product-level rate definitions
-- ============================================================

CREATE TABLE interest_rates (
    id              UUID                PRIMARY KEY DEFAULT gen_random_uuid(),
    product_type    VARCHAR(50)         NOT NULL,
    rate            DECIMAL(10,8)       NOT NULL CHECK (rate >= 0),
    effective_from  DATE                NOT NULL,
    effective_to    DATE,
    created_at      TIMESTAMPTZ         NOT NULL DEFAULT now(),

    CONSTRAINT chk_rate_dates CHECK (
        effective_to IS NULL OR effective_to >= effective_from
    )
);

COMMENT ON COLUMN interest_rates.rate IS 'Annual interest rate as decimal, e.g. 0.05250000 = 5.25%';

-- ============================================================
-- interest_accrual — daily accrual records per account
-- ============================================================

CREATE TABLE interest_accrual (
    id                      UUID                    PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id              UUID                    NOT NULL REFERENCES accounts (id),
    accrual_date            DATE                    NOT NULL,
    principal               DECIMAL(19,4)           NOT NULL CHECK (principal >= 0),
    rate                    DECIMAL(10,8)           NOT NULL CHECK (rate >= 0),
    day_count_convention    day_count_convention    NOT NULL,
    accrued_amount          DECIMAL(19,4)           NOT NULL CHECK (accrued_amount >= 0),
    posted                  BOOLEAN                 NOT NULL DEFAULT FALSE,
    journal_entry_id        UUID                    REFERENCES journal_entries (id),
    created_at              TIMESTAMPTZ             NOT NULL DEFAULT now(),

    CONSTRAINT uq_accrual_account_date UNIQUE (account_id, accrual_date)
);

COMMENT ON COLUMN interest_accrual.posted IS 'FALSE = accrued only; TRUE = posted to general ledger';
COMMENT ON COLUMN interest_accrual.journal_entry_id IS 'Links to GL journal entry when posted';

-- ============================================================
-- Indexes
-- ============================================================

-- Interest rates
CREATE INDEX idx_interest_rates_product     ON interest_rates (product_type);
CREATE INDEX idx_interest_rates_effective   ON interest_rates (product_type, effective_from, effective_to);

-- Interest accrual
CREATE INDEX idx_accrual_account_id         ON interest_accrual (account_id);
CREATE INDEX idx_accrual_date               ON interest_accrual (accrual_date);
CREATE INDEX idx_accrual_unposted           ON interest_accrual (account_id, accrual_date)
    WHERE posted = FALSE;
CREATE INDEX idx_accrual_posted             ON interest_accrual (account_id, accrual_date)
    WHERE posted = TRUE;
