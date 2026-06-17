package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Target is a discovered (table, column) text column eligible for scrubbing.
type Target struct {
	Table  string
	Column string
	Type   string // information_schema.data_type
}

// TableGroup is every scrub-eligible column of a single table, in information_schema order.
// Counting and updating operate per-group so the table's heap is scanned once, not once per column.
type TableGroup struct {
	Table string
	Cols  []Target
}

// Sample is one before→after pair for --dry-run display.
type Sample struct {
	Before string
	After  string
}

// Update is a planned single-column scrub with its impact metrics. The --show/--dry-run
// output is still per-column (familiar shape); only the underlying scans are consolidated.
type Update struct {
	Target  Target
	Matches int      // rows containing >=1 case-insensitive match
	Samples []Sample // up to sampleRows before→after pairs
}

// Scrubber holds the validated plan parameters and the DB pool. It is constructed
// ONLY in non-mock branches (mock mode never opens a pool), mirroring the
// DiscardWriter philosophy from pkg/stocks/mock.go.
type Scrubber struct {
	pool *pgxpool.Pool

	schema      string
	types       []string // effective data_type list (jsonb/json appended when IncludeJSONB)
	pattern     string   // escapeRegex(Read) — the regex pattern, always passed to regexp_replace
	replacement string   // escapeReplacement(Write) — passed to regexp_replace
	ilike       string   // "%" + escapeILIKE(Read) + "%" — faster filter predicate for plain literals
	plain       bool     // Read has no regex metacharacters → use ILIKE instead of ~* in predicates
	sampleRows  int
	stmtTimeoutMs int // SET LOCAL statement_timeout at tx start (0 = no limit)

	// Table filters, applied uniformly in --show/--dry-run/--apply.
	include      []string
	selectTables []string
	exclude      []string
}

// newScrubber builds a Scrubber from a validated Config. The match mode (plain vs
// regex) and all escaped literals are pre-computed once and reused for every SQL
// statement, so the only per-statement work is the query itself.
func newScrubber(pool *pgxpool.Pool, cfg *Config, selectTables []string) *Scrubber {
	types := append([]string{}, cfg.ColumnTypes...)
	if cfg.IncludeJSONB {
		types = append(types, "jsonb", "json")
	}
	sr := cfg.SampleRows
	if sr <= 0 {
		sr = defaultSampleRows
	}
	return &Scrubber{
		pool:           pool,
		schema:         cfg.Schema,
		types:          types,
		pattern:        escapeRegex(cfg.Read),
		replacement:    escapeReplacement(cfg.Write),
		ilike:          "%" + escapeILIKE(cfg.Read) + "%",
		plain:          isPlainLiteral(cfg.Read),
		sampleRows:     sr,
		stmtTimeoutMs:  cfg.StatementTimeoutSeconds * 1000,
		include:        cfg.IncludeTables,
		selectTables:   selectTables,
		exclude:        cfg.ExcludeTables,
	}
}

// --- identifier + literal escaping (client-side, pure, unit-tested) ---

// quoteIdent wraps a SQL identifier in double quotes and escapes internal " by
// doubling. Identifiers come from information_schema; we quote defensively.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// escapeRegex turns s into a POSIX ERE that matches s literally, so '.', '+', '(',
// etc. inside a brand name are not treated as regex operators. Always needed for
// regexp_replace (case-insensitive global replace has no non-regex equivalent).
func escapeRegex(s string) string {
	const special = `\.[](){}*+?^$|`
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(special, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// escapeReplacement escapes s for use as a regexp_replace replacement literal.
// PostgreSQL treats \1..\9 as backreferences and \ as an escape; '&' is not special.
// We double every backslash so write_substring is inserted verbatim.
func escapeReplacement(s string) string {
	return strings.ReplaceAll(s, `\`, `\\`)
}

// escapeILIKE escapes s for use inside an ILIKE pattern: %, _ and \ are special in
// LIKE/ILIKE and must be backslash-escaped so a brand containing them matches literally.
// The caller wraps the result with surrounding % wildcards.
func escapeILIKE(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '%', '_', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isPlainLiteral reports whether read contains no POSIX regex metacharacters. When
// true, the scan predicate can use the faster ILIKE matcher instead of ~*.
func isPlainLiteral(read string) bool {
	const special = `\.[](){}*+?^$|`
	for _, r := range read {
		if strings.ContainsRune(special, r) {
			return false
		}
	}
	return true
}

// --- SQL building ---

// queryer is satisfied by both *pgxpool.Pool and pgx.Tx, so the same read methods
// run against the pool or inside a READ ONLY transaction.
type queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// isJSON reports whether the column type needs ::text / ::jsonb casting.
func isJSON(dataType string) bool {
	return dataType == "jsonb" || dataType == "json"
}

// fqName returns "schema"."table".
func (s *Scrubber) fqName(table string) string {
	return quoteIdent(s.schema) + "." + quoteIdent(table)
}

// fqTable returns "schema"."table" for a target (table name only; column ignored).
func (s *Scrubber) fqTable(t Target) string { return s.fqName(t.Table) }

// matchExpr returns the column expression used in predicates and regexp_replace
// input: "col" for text-like, "col"::text for jsonb/json.
func (s *Scrubber) matchExpr(t Target) string {
	expr := quoteIdent(t.Column)
	if isJSON(t.Type) {
		return expr + "::text"
	}
	return expr
}

// groupTargetsByTable groups targets by table, preserving first-appearance order
// (discoverTargets already returns them sorted table,column — this is stable regardless).
func groupTargetsByTable(targets []Target) []TableGroup {
	var groups []TableGroup
	idx := map[string]int{}
	for _, t := range targets {
		if gi, ok := idx[t.Table]; ok {
			groups[gi].Cols = append(groups[gi].Cols, t)
			continue
		}
		idx[t.Table] = len(groups)
		groups = append(groups, TableGroup{Table: t.Table, Cols: []Target{t}})
	}
	return groups
}

// countPredicate is the boolean test used in a count statement's FILTER clause.
// Both modes reference $1; countArgs supplies the ilike or regex pattern accordingly.
func (s *Scrubber) countPredicate(expr string) string {
	if s.plain {
		return expr + " ILIKE $1"
	}
	return expr + " ~* $1"
}

// updatePredicate is the boolean test used in an update statement's CASE WHEN and
// WHERE clauses. Regex mode reuses $1 (the same regex pattern regexp_replace uses);
// plain mode uses $3, leaving $1/$2 for regexp_replace which must stay regex-based.
func (s *Scrubber) updatePredicate(expr string) string {
	if s.plain {
		return expr + " ILIKE $3"
	}
	return expr + " ~* $1"
}

// countTableSQL builds a single-pass count over one table. Every column's match
// count is returned as c0..c{N-1} in group.Cols order, so one heap scan yields all
// per-column counts (vs. one scan per column previously).
func (s *Scrubber) countTableSQL(g TableGroup) string {
	var b strings.Builder
	b.WriteString("SELECT ")
	for i, t := range g.Cols {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "count(*) FILTER (WHERE %s) AS c%d", s.countPredicate(s.matchExpr(t)), i)
	}
	b.WriteString(" FROM ")
	b.WriteString(s.fqName(g.Table))
	return b.String()
}

// updateTableSQL builds a single-pass UPDATE over one table. Each column is scrubbed
// via regexp_replace only when it matches (CASE … ELSE col), and only rows with at
// least one match are visited (WHERE OR-chain). Net effect: one heap scan and one
// MVCC version per matching row (vs. one scan + up to K versions per column previously).
// jsonb columns are cast ::text for the predicate/replace and back ::jsonb on assignment.
func (s *Scrubber) updateTableSQL(g TableGroup) string {
	sets := make([]string, 0, len(g.Cols))
	wheres := make([]string, 0, len(g.Cols))
	for _, t := range g.Cols {
		col := quoteIdent(t.Column)
		expr := s.matchExpr(t)
		replace := fmt.Sprintf("regexp_replace(%s, $1, $2, 'gi')", expr)
		if isJSON(t.Type) {
			replace += "::jsonb"
		}
		pred := s.updatePredicate(expr)
		sets = append(sets, fmt.Sprintf("%s = CASE WHEN %s THEN %s ELSE %s END", col, pred, replace, col))
		wheres = append(wheres, pred)
	}
	return fmt.Sprintf("UPDATE %s SET %s WHERE %s",
		s.fqName(g.Table), strings.Join(sets, ", "), strings.Join(wheres, " OR "))
}

// countArgs returns the single argument for a count statement: the ilike pattern in
// plain mode, the regex pattern otherwise.
func (s *Scrubber) countArgs() []any {
	if s.plain {
		return []any{s.ilike}
	}
	return []any{s.pattern}
}

// updateArgs returns the arguments for an update statement: [regex pattern,
// replacement] always (regexp_replace is regex-based), plus the ilike pattern in
// plain mode (the $3 predicate).
func (s *Scrubber) updateArgs() []any {
	if s.plain {
		return []any{s.pattern, s.replacement, s.ilike}
	}
	return []any{s.pattern, s.replacement}
}

// sampleSQL builds the per-column before→after preview (LIMIT $3). Samples run only
// for columns already known to have matches and are bounded by sampleRows, so the
// scan cost is negligible and we keep the simpler regex predicate here regardless of mode.
func (s *Scrubber) sampleSQL(t Target) string {
	expr := s.matchExpr(t)
	return fmt.Sprintf(
		"SELECT %s AS before_val, regexp_replace(%s, $1, $2, 'gi') AS after_val FROM %s WHERE %s ~* $1 LIMIT $3",
		expr, expr, s.fqTable(t), expr,
	)
}

// --- DB read operations ---

// countTable runs the consolidated count for one table and returns per-column match
// counts in group.Cols order.
func (s *Scrubber) countTable(ctx context.Context, q queryer, g TableGroup) ([]int, error) {
	rows, err := q.Query(ctx, s.countTableSQL(g), s.countArgs()...)
	if err != nil {
		return nil, fmt.Errorf("count table %s: %w", g.Table, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, fmt.Errorf("count table %s: empty result", g.Table)
	}
	vals, err := rows.Values()
	if err != nil {
		return nil, fmt.Errorf("count table %s: read values: %w", g.Table, err)
	}
	out := make([]int, len(g.Cols))
	for i, v := range vals {
		n, ok := v.(int64)
		if !ok {
			return nil, fmt.Errorf("count table %s: column %d has unexpected type %T", g.Table, i, v)
		}
		out[i] = int(n)
	}
	return out, rows.Err()
}

// samples returns up to sampleRows before→after pairs for t.
func (s *Scrubber) samples(ctx context.Context, q queryer, t Target) ([]Sample, error) {
	rows, err := q.Query(ctx, s.sampleSQL(t), s.pattern, s.replacement, s.sampleRows)
	if err != nil {
		return nil, fmt.Errorf("sample %s.%s: %w", t.Table, t.Column, err)
	}
	defer rows.Close()

	var out []Sample
	for rows.Next() {
		var sm Sample
		if err := rows.Scan(&sm.Before, &sm.After); err != nil {
			return nil, fmt.Errorf("scan sample %s.%s: %w", t.Table, t.Column, err)
		}
		out = append(out, sm)
	}
	return out, rows.Err()
}
