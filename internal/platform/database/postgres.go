// Package database provides a PostgreSQL connection pool with helpers for
// transactional execution and pessimistic locking used throughout Nkore Bank.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver
)

// DB wraps a *sql.DB with banking-specific helpers.
type DB struct {
	Pool *sql.DB
}

// New opens a PostgreSQL connection pool and configures its limits.
func New(databaseURL string) (*DB, error) {
	pool, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("database: open: %w", err)
	}

	pool.SetMaxOpenConns(25)
	pool.SetMaxIdleConns(10)
	pool.SetConnMaxLifetime(5 * time.Minute)
	pool.SetConnMaxIdleTime(1 * time.Minute)

	if err := pool.PingContext(context.Background()); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: ping: %w", err)
	}

	return &DB{Pool: pool}, nil
}

// Close gracefully closes the connection pool.
func (db *DB) Close() error {
	return db.Pool.Close()
}

// HealthCheck verifies the database connection is alive.
func (db *DB) HealthCheck(ctx context.Context) error {
	if err := db.Pool.PingContext(ctx); err != nil {
		return fmt.Errorf("database: health check: %w", err)
	}
	return nil
}

// TxFunc is a function executed within a database transaction.
type TxFunc func(tx *sql.Tx) error

// RunInTx executes fn inside a transaction. If fn returns an error or panics,
// the transaction is rolled back; otherwise it is committed.
func (db *DB) RunInTx(ctx context.Context, opts *sql.TxOptions, fn TxFunc) (err error) {
	tx, err := db.Pool.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("database: begin tx: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			err = fmt.Errorf("database: tx panic: %v", p)
		} else if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				err = fmt.Errorf("database: rollback failed: %w (original: %v)", rbErr, err)
			}
		}
	}()

	if err = fn(tx); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("database: commit: %w", err)
	}
	return nil
}

// SelectForUpdate appends "FOR UPDATE" to a query and executes it within the
// given transaction, scanning the result into dest. This is the standard
// pessimistic locking pattern for banking operations such as balance checks.
func SelectForUpdate(ctx context.Context, tx *sql.Tx, query string, dest []any, args ...any) error {
	locked := query + " FOR UPDATE"
	row := tx.QueryRowContext(ctx, locked, args...)
	if err := row.Scan(dest...); err != nil {
		return fmt.Errorf("database: select for update: %w", err)
	}
	return nil
}
