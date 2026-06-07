// Package postgres provides PostgreSQL storage implementation.
//
// Architecture follows ISP: each domain gets its own focused repository
// (PgCardsRepo, PgSalesRepo, etc.) that implements exactly one Writer interface.
// No god-object — unlike the SQLite package's monolithic SQLiteSalesRepository.
package postgres

import (
	"context"
	"fmt"
	"time"

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
//   - MaxConnLifetime: 30m (rotate connections to prevent stale state)
//   - MaxConnIdleTime: 5m (reclaim idle conns quickly for short-lived CLIs)
//   - HealthCheckPeriod: 30s (detect dead connections proactively)
//
// Session params (applied at connection time, no extra round-trip):
//   - work_mem: 64MB (prevents disk spill for large multi-row INSERTs)
//   - synchronous_commit: off (2-5x faster COMMIT for re-downloadable data)
//   - statement_timeout: 5min (safety net against stuck queries)
//   - effective_cache_size: 2GB (planner hint, favors index scans)
//
// The pool pings the server on creation to verify connectivity.
func NewPool(ctx context.Context, dsn string) (*Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse DSN: %w", err)
	}

	// Pool sizing
	config.MaxConns = 10
	config.MinConns = 2

	// Connection lifecycle tuning for short-lived CLI utilities
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute
	config.HealthCheckPeriod = 30 * time.Second

	// Session-level optimizations via RuntimeParams (sent at connection time)
	for k, v := range BulkLoadSessionParams() {
		config.ConnConfig.RuntimeParams[k] = v
	}

	// AfterConnect hook for params that need explicit SET
	config.AfterConnect = AfterConnectBulkLoad

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
