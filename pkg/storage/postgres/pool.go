// Package postgres provides PostgreSQL storage implementation.
//
// Architecture follows ISP: each domain gets its own focused repository
// (PgCardsRepo, PgSalesRepo, etc.) that implements exactly one Writer interface.
// No god-object — unlike the SQLite package's monolithic SQLiteSalesRepository.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool wraps pgxpool.Pool with lifecycle management.
type Pool struct {
	pool *pgxpool.Pool
}

// NewPool creates a connection pool from a DSN.
//
// Pool defaults:
//   - MaxConns: 10 (suitable for concurrent downloaders)
//   - MinConns: 2 (keeps warm connections)
//
// The pool pings the server on creation to verify connectivity.
func NewPool(ctx context.Context, dsn string) (*Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse DSN: %w", err)
	}

	config.MaxConns = 10
	config.MinConns = 2

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	return &Pool{pool: pool}, nil
}

// Close releases all pool connections.
func (p *Pool) Close() {
	p.pool.Close()
}

// DB returns the underlying pgxpool.Pool for direct access.
func (p *Pool) DB() *pgxpool.Pool {
	return p.pool
}
