package main

import (
	"context"
	"fmt"
)

// discoverTargets lists every base-table column in the configured schema whose
// data_type matches the effective type list (text-like, plus jsonb/json when
// IncludeJSONB). Views are excluded (table_type='BASE TABLE') because a substring
// scrub cannot UPDATE through a non-materialized view. The table filters
// (include/select/exclude) are applied client-side afterwards.
func (s *Scrubber) discoverTargets(ctx context.Context, q queryer) ([]Target, error) {
	const sql = `
		SELECT c.table_name, c.column_name, c.data_type
		FROM information_schema.columns c
		JOIN information_schema.tables t
		  ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = $1
		  AND t.table_type = 'BASE TABLE'
		  AND c.data_type = ANY($2::text[])
		ORDER BY c.table_name, c.column_name`

	rows, err := q.Query(ctx, sql, s.schema, s.types)
	if err != nil {
		return nil, fmt.Errorf("introspect columns: %w", err)
	}
	defer rows.Close()

	all := make([]Target, 0, 64)
	for rows.Next() {
		var t Target
		if err := rows.Scan(&t.Table, &t.Column, &t.Type); err != nil {
			return nil, fmt.Errorf("scan column row: %w", err)
		}
		all = append(all, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read column rows: %w", err)
	}

	return filterTables(all, s.include, s.selectTables, s.exclude), nil
}

// filterTables narrows a target list by include ∩ select_tables, minus exclude.
// include and select are intersected when both are set; either alone acts as the
// allow-list. If a filter is active but the intersection is empty, NOTHING passes
// (the user explicitly constrained to an empty set) — this is distinct from "no
// filter active", which keeps everything except excludes.
func filterTables(all []Target, include, selectTables, exclude []string) []Target {
	active := len(include) > 0 || len(selectTables) > 0
	allow := include
	if len(selectTables) > 0 {
		if len(allow) == 0 {
			allow = selectTables
		} else {
			allow = intersectStrings(allow, selectTables)
		}
	}
	excluded := stringSet(exclude)

	out := make([]Target, 0, len(all))
	for _, t := range all {
		if active && !containsString(allow, t.Table) {
			continue
		}
		if excluded[t.Table] {
			continue
		}
		out = append(out, t)
	}
	return out
}

// --- small string-slice helpers (pure, unit-tested) ---

func intersectStrings(a, b []string) []string {
	bs := stringSet(b)
	out := make([]string, 0, len(a))
	for _, s := range a {
		if bs[s] {
			out = append(out, s)
		}
	}
	return out
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func stringSet(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}
