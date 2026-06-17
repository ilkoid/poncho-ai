package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// beginScrubTx opens a transaction and immediately raises the statement_timeout
// above the pool's 5-minute default. A consolidated UPDATE on tens of millions of
// rows legitimately runs longer than 5 minutes; a timeout firing mid-transaction
// would roll back the ENTIRE scrub. SET LOCAL is scoped to this transaction, so the
// pool's connections are unaffected once the tx ends. ms=0 disables the timeout.
func (s *Scrubber) beginScrubTx(ctx context.Context, readOnly bool) (pgx.Tx, error) {
	opts := pgx.TxOptions{}
	if readOnly {
		opts.AccessMode = pgx.ReadOnly
	}
	tx, err := s.pool.BeginTx(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", s.stmtTimeoutMs)); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("set statement_timeout: %w", err)
	}
	return tx, nil
}

// apply executes one consolidated UPDATE per table group inside a single transaction.
// The scrub is logically atomic: either every occurrence of the read substring is
// replaced in every column, or none — a partial scrub would leave the database
// half-leaking the sensitive value, which is exactly the failure this tool prevents.
// Any per-table error rolls back the whole transaction.
//
// There is deliberately NO pre-count pass: the UPDATE predicate is the same one
// counting would use, so RowsAffected is the source of truth and there is no
// read/write race that could skip a table. With consolidation, RowsAffected counts
// updated ROWS (a row matching in several columns is one new MVCC version), not
// per-column matches.
//
// Returns total rows updated and how many tables actually had matches.
func (s *Scrubber) apply(ctx context.Context, groups []TableGroup) (totalRows, tablesTouched int, err error) {
	tx, err := s.beginScrubTx(ctx, false)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx) // no-op after Commit

	start := time.Now()
	for i := range groups {
		g := groups[i]
		tag, err := tx.Exec(ctx, s.updateTableSQL(g), s.updateArgs()...)
		if err != nil {
			return totalRows, tablesTouched, fmt.Errorf("update %s: %w", g.Table, err)
		}
		n := int(tag.RowsAffected())
		totalRows += n
		if n > 0 {
			tablesTouched++
		}
		printApplyProgress(i+1, len(groups), g, n, time.Since(start))
	}

	if err := tx.Commit(ctx); err != nil {
		return totalRows, tablesTouched, fmt.Errorf("commit: %w", err)
	}
	return totalRows, tablesTouched, nil
}

// withReadOnlyTx runs fn inside a READ ONLY transaction for --show/--dry-run.
// The transaction is always rolled back (never committed), so the safety of the
// read-only access mode is reinforced: there is no code path that commits writes.
func (s *Scrubber) withReadOnlyTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.beginScrubTx(ctx, true)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	return fn(tx)
}
