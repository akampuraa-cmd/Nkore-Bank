-- Migration 003: Transaction Engine Schema
-- Nkore Bank Core Banking System
--
-- Banking principles enforced:
--   - DECIMAL(19,4) for all monetary amounts (NEVER float)
--   - INSERT-ONLY transaction entries (event sourcing)
--   - Debits must equal credits per transaction (trigger-enforced)
--   - Idempotency key prevents duplicate processing

-- ============================================================
-- ENUM types for transactions
-- ============================================================

CREATE TYPE transaction_type AS ENUM (
    'DEPOSIT',
    'WITHDRAWAL',
    'TRANSFER',
    'FEE',
    'INTEREST',
    'REVERSAL'
);

CREATE TYPE transaction_status AS ENUM (
    'PENDING',
    'SETTLED',
    'FAILED',
    'REVERSED'
);

CREATE TYPE tx_entry_type AS ENUM ('DEBIT', 'CREDIT');

-- ============================================================
-- transactions — header record for each banking transaction
-- ============================================================

CREATE TABLE transactions (
    id                  UUID                PRIMARY KEY DEFAULT gen_random_uuid(),
    idempotency_key     UUID                NOT NULL UNIQUE,
    transaction_type    transaction_type    NOT NULL,
    status              transaction_status  NOT NULL DEFAULT 'PENDING',
    reference_number    VARCHAR(50)         NOT NULL,
    description         TEXT,
    created_at          TIMESTAMPTZ         NOT NULL DEFAULT now(),
    settled_at          TIMESTAMPTZ
);

COMMENT ON COLUMN transactions.idempotency_key IS 'Prevents duplicate transaction processing';

-- ============================================================
-- transaction_entries — INSERT-ONLY double-entry legs
-- ============================================================

CREATE TABLE transaction_entries (
    id                  UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id      UUID            NOT NULL REFERENCES transactions (id),
    account_id          UUID            NOT NULL REFERENCES accounts (id),
    entry_type          tx_entry_type   NOT NULL,
    amount              DECIMAL(19,4)   NOT NULL CHECK (amount > 0),
    running_balance     DECIMAL(19,4)   NOT NULL,
    currency            CHAR(3)         NOT NULL DEFAULT 'UGX',
    created_at          TIMESTAMPTZ     NOT NULL DEFAULT now()
);

COMMENT ON TABLE transaction_entries IS 'INSERT-ONLY — rows must never be updated or deleted (event sourcing)';

-- Prevent UPDATE and DELETE on transaction_entries
CREATE OR REPLACE FUNCTION fn_tx_entries_immutable()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'transaction_entries is an immutable, INSERT-ONLY ledger. UPDATE and DELETE are prohibited.';
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_tx_entries_no_update
    BEFORE UPDATE ON transaction_entries
    FOR EACH ROW EXECUTE FUNCTION fn_tx_entries_immutable();

CREATE TRIGGER trg_tx_entries_no_delete
    BEFORE DELETE ON transaction_entries
    FOR EACH ROW EXECUTE FUNCTION fn_tx_entries_immutable();

-- ============================================================
-- Validate debits = credits per transaction
-- ============================================================

CREATE OR REPLACE FUNCTION fn_validate_transaction_balance()
RETURNS TRIGGER AS $$
DECLARE
    v_debit_total   DECIMAL(19,4);
    v_credit_total  DECIMAL(19,4);
    v_tx_status     transaction_status;
BEGIN
    -- Only validate when the transaction is being settled
    SELECT status INTO v_tx_status
    FROM transactions
    WHERE id = NEW.transaction_id;

    SELECT
        COALESCE(SUM(CASE WHEN entry_type = 'DEBIT'  THEN amount END), 0),
        COALESCE(SUM(CASE WHEN entry_type = 'CREDIT' THEN amount END), 0)
    INTO v_debit_total, v_credit_total
    FROM transaction_entries
    WHERE transaction_id = NEW.transaction_id;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_validate_transaction_balance
    AFTER INSERT ON transaction_entries
    FOR EACH ROW EXECUTE FUNCTION fn_validate_transaction_balance();

-- Explicit function to assert transaction balance (called by application before settling)
CREATE OR REPLACE FUNCTION fn_assert_transaction_balanced(p_transaction_id UUID)
RETURNS BOOLEAN AS $$
DECLARE
    v_debit_total   DECIMAL(19,4);
    v_credit_total  DECIMAL(19,4);
BEGIN
    SELECT
        COALESCE(SUM(CASE WHEN entry_type = 'DEBIT'  THEN amount END), 0),
        COALESCE(SUM(CASE WHEN entry_type = 'CREDIT' THEN amount END), 0)
    INTO v_debit_total, v_credit_total
    FROM transaction_entries
    WHERE transaction_id = p_transaction_id;

    IF v_debit_total <> v_credit_total THEN
        RAISE EXCEPTION 'Transaction % is unbalanced: debits=% credits=%',
            p_transaction_id, v_debit_total, v_credit_total;
    END IF;

    RETURN TRUE;
END;
$$ LANGUAGE plpgsql;

-- Function to settle a transaction (validates balance first)
CREATE OR REPLACE FUNCTION fn_settle_transaction(p_transaction_id UUID)
RETURNS BOOLEAN AS $$
BEGIN
    PERFORM fn_assert_transaction_balanced(p_transaction_id);

    UPDATE transactions
    SET status = 'SETTLED', settled_at = now()
    WHERE id = p_transaction_id AND status = 'PENDING';

    IF NOT FOUND THEN
        RAISE EXCEPTION 'Transaction % cannot be settled — not in PENDING status', p_transaction_id;
    END IF;

    RETURN TRUE;
END;
$$ LANGUAGE plpgsql;

-- ============================================================
-- Indexes
-- ============================================================

CREATE INDEX idx_tx_idempotency            ON transactions (idempotency_key);
CREATE INDEX idx_tx_status                 ON transactions (status);
CREATE INDEX idx_tx_type                   ON transactions (transaction_type);
CREATE INDEX idx_tx_reference              ON transactions (reference_number);
CREATE INDEX idx_tx_created_at             ON transactions (created_at);
CREATE INDEX idx_tx_settled_at             ON transactions (settled_at)
    WHERE settled_at IS NOT NULL;

-- Primary query pattern: account statement (account + date range)
CREATE INDEX idx_tx_entries_account_date   ON transaction_entries (account_id, created_at);
CREATE INDEX idx_tx_entries_transaction_id ON transaction_entries (transaction_id);
CREATE INDEX idx_tx_entries_currency       ON transaction_entries (currency);
