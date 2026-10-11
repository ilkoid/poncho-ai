package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/fsa"
)

// Compile-time assertion: PgFSARepo implements fsa.Writer.
var _ fsa.Writer = (*PgFSARepo)(nil)

// pgFSAColumns — порядок колонок CopyFrom; должен совпадать с appendChunk.
var pgFSAColumns = []string{
	"certificate_number", "doc_kind", "fsa_id", "reg_date", "end_date",
	"id_status", "cert_type", "object_type", "found", "exact_match",
	"skipped_reason", "error", "checked_at",
}

const pgFSAChunkSize = 500

// PgFSARepo implements fsa.Writer for PostgreSQL (append-only RAW).
type PgFSARepo struct {
	pool *pgxpool.Pool
}

// NewPgFSARepo creates a new FSA check results repository.
func NewPgFSARepo(pool *pgxpool.Pool) *PgFSARepo {
	return &PgFSARepo{pool: pool}
}

// InitSchema creates fsa_certificates table, analytical schema and the
// certificate_validity view if they don't exist.
func (r *PgFSARepo) InitSchema(ctx context.Context) error {
	return initFSASchema(ctx, r.pool)
}

// AppendChecks appends check results via COPY in chunked transactions.
func (r *PgFSARepo) AppendChecks(ctx context.Context, checks []fsa.CheckResult) (int, error) {
	if len(checks) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(checks); i += pgFSAChunkSize {
		end := min(i+pgFSAChunkSize, len(checks))
		n, err := r.appendChunk(ctx, checks[i:end])
		if err != nil {
			return total, fmt.Errorf("append checks chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

func (r *PgFSARepo) appendChunk(ctx context.Context, chunk []fsa.CheckResult) (int, error) {
	rows := make([][]any, len(chunk))
	for i, c := range chunk {
		rows[i] = []any{
			c.CertificateNumber, c.DocKind, c.FsaID, c.RegDate, c.EndDate,
			c.IDStatus, c.CertType, c.ObjectType, c.Found, c.ExactMatch,
			c.SkippedReason, c.Error, c.CheckedAt,
		}
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	ct, err := tx.CopyFrom(ctx,
		pgx.Identifier{"fsa_certificates"},
		pgFSAColumns,
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return 0, fmt.Errorf("copy from: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return int(ct), nil
}
