// Package db provides database connection and query utilities.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deicod/gotodon/internal/config"
)

// DBTX is the interface for database operations.
// It's compatible with both *pgxpool.Pool and pgx.Tx.
type DBTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// DB wraps the pgx connection pool with generated queries.
type DB struct {
	Pool    *pgxpool.Pool
	Queries *Queries
}

// New creates a new database connection pool.
func New(ctx context.Context, cfg *config.DatabaseConfig) (*DB, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("parsing database config: %w", err)
	}

	poolConfig.MaxConns = int32(cfg.MaxOpenConns)
	poolConfig.MinConns = int32(cfg.MaxIdleConns)

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}

	// Test the connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return &DB{
		Pool:    pool,
		Queries: NewQueries(pool),
	}, nil
}

// Queries wraps all database queries.
// NOTE: This is a placeholder struct. Run `sqlc generate` to create
// the actual implementation with type-safe query methods.
type Queries struct {
	db DBTX
}

// New creates a new Queries instance.
func NewQueries(db DBTX) *Queries {
	return &Queries{db: db}
}

// WithTx creates a new Queries instance with the given transaction.
func (q *Queries) WithTx(tx DBTX) *Queries {
	return &Queries{db: tx}
}

// Close closes the database connection pool.
func (d *DB) Close() {
	d.Pool.Close()
}
