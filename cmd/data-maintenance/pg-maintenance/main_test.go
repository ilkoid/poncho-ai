package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMaintenanceStmt — one statement per table, one scan per statement in every
// mode combination (the pre-refactor code ran ANALYZE and VACUUM as two separate
// scans; these expectations pin the combined forms).
func TestMaintenanceStmt(t *testing.T) {
	assert.Equal(t, "VACUUM (ANALYZE) cards", maintenanceStmt(false, false, "cards"))
	assert.Equal(t, "VACUUM (FULL, ANALYZE) cards", maintenanceStmt(false, true, "cards"))
	assert.Equal(t, "ANALYZE cards", maintenanceStmt(true, false, "cards"))
}

// TestMaintenanceMode — analyze-only wins over full in the label (the flag
// combination itself is rejected in main, the label just documents precedence).
func TestMaintenanceMode(t *testing.T) {
	assert.Equal(t, "VACUUM (ANALYZE)", maintenanceMode(false, false))
	assert.Equal(t, "VACUUM (FULL, ANALYZE)", maintenanceMode(false, true))
	assert.Equal(t, "ANALYZE", maintenanceMode(true, false))
	assert.Equal(t, "ANALYZE", maintenanceMode(true, true))
}

// TestResolveGroup — group resolution: nil without --group, tables from the
// config, depth "analyze" lifts the flag, unknown group/depth/table-list fail.
func TestResolveGroup(t *testing.T) {
	groups := map[string]GroupConfig{
		"cards":       {Depth: "vacuum", Tables: []string{"cards", "card_sizes"}},
		"onec-prices": {Depth: "analyze", Tables: []string{"onec_prices"}},
	}

	tables, err := resolveGroup("", groups, new(bool))
	assert.NoError(t, err)
	assert.Nil(t, tables, "no --group → nil → caller falls back to hardcoded lists")

	analyzeOnly := false
	tables, err = resolveGroup("cards", groups, &analyzeOnly)
	assert.NoError(t, err)
	assert.Equal(t, []string{"cards", "card_sizes"}, tables)
	assert.False(t, analyzeOnly, "vacuum depth leaves the flag alone")

	analyzeOnly = false
	tables, err = resolveGroup("onec-prices", groups, &analyzeOnly)
	assert.NoError(t, err)
	assert.Equal(t, []string{"onec_prices"}, tables)
	assert.True(t, analyzeOnly, "analyze depth lifts analyzeOnly")

	_, err = resolveGroup("nope", groups, new(bool))
	assert.ErrorContains(t, err, `--group "nope" is not defined`)
	assert.ErrorContains(t, err, "cards, onec-prices", "error lists available groups")

	bad := map[string]GroupConfig{"weird": {Depth: "extreme", Tables: []string{"cards"}}}
	_, err = resolveGroup("weird", bad, new(bool))
	assert.ErrorContains(t, err, `unknown depth "extreme"`)

	empty := map[string]GroupConfig{"hollow": {Depth: "vacuum"}}
	_, err = resolveGroup("hollow", empty, new(bool))
	assert.ErrorContains(t, err, "empty tables list")
}

// TestSortedGroupNames — deterministic (sorted) group listing in error messages.
func TestSortedGroupNames(t *testing.T) {
	assert.Equal(t,
		[]string{"beta", "alpha", "gamma"},
		[]string{"beta", "alpha", "gamma"}) // sanity: literal order
	names := sortedGroupNames(map[string]GroupConfig{
		"gamma": {}, "alpha": {}, "beta": {},
	})
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, names)
}

// TestFilterExistingTables_KeepsCanonicalOrder — missing names are dropped, the
// survivors keep the order of `want` (so the maintenance phase sequence is stable).
func TestFilterExistingTables_KeepsCanonicalOrder(t *testing.T) {
	want := []string{"cards", "sales", "products"}
	have := map[string]struct{}{"cards": {}, "products": {}}

	kept, missing := filterExistingTables(want, have)

	assert.Equal(t, []string{"cards", "products"}, kept, "kept preserves want order")
	assert.Equal(t, []string{"sales"}, missing, "missing preserves want order")
}

// TestFilterExistingTables_AllPresent — nothing is dropped when the catalog is a
// superset of want.
func TestFilterExistingTables_AllPresent(t *testing.T) {
	want := []string{"cards", "sales"}
	have := map[string]struct{}{"cards": {}, "sales": {}, "extra": {}}

	kept, missing := filterExistingTables(want, have)

	assert.Equal(t, want, kept)
	assert.Empty(t, missing)
}

// TestFilterExistingTables_AllMissing — empty catalog (fresh DB) drops everything;
// missing mirrors want so the operator sees exactly what was skipped.
func TestFilterExistingTables_AllMissing(t *testing.T) {
	want := []string{"cards", "sales"}
	have := map[string]struct{}{}

	kept, missing := filterExistingTables(want, have)

	assert.Empty(t, kept)
	assert.Equal(t, want, missing)
}

// TestFilterExistingTables_EmptyWant — degenerate input, no panic, no allocations
// of meaning.
func TestFilterExistingTables_EmptyWant(t *testing.T) {
	kept, missing := filterExistingTables(nil, map[string]struct{}{"cards": {}})

	assert.Empty(t, kept)
	assert.Empty(t, missing)
}

// wbscraperTables are the test-only snapshot tables from pkg/wbscraper. Per
// AGENTS.md their schema lives ONLY in wb_data_test and is never created in prod
// (wb_data_prod). The refactor commit 16f9561 hand-added them to the maintenance
// lists, which made every prod run fail on SQLSTATE 42P01. This test guards against
// that regression: none of these names may appear in any maintenance slice.
var wbscraperTables = []string{
	"search_queries",
	"search_positions",
	"vitrine_ads",
	"competitor_cards",
	"competitor_card_prices",
	"competitor_card_details",
	"competitor_card_stocks",
	"competitor_card_meta",
	"competitor_card_options",
	"competitor_card_compositions",
	"competitor_card_sizes",
	"competitor_card_colors",
}

func TestMaintenanceList_HasNoWbscraperTables(t *testing.T) {
	all := append(append(append([]string{}, HeavyUpdateTables...), AppendOnlyTables...), PromotionTables...)
	seen := make(map[string]struct{}, len(all))
	for _, name := range all {
		seen[name] = struct{}{}
	}
	for _, name := range wbscraperTables {
		_, present := seen[name]
		assert.Falsef(t, present, "wbscraper test-only table %q must not be in the maintenance list", name)
	}
}

// TestMaintenanceList_NoDuplicates — a name appearing in two slices (e.g. the same
// table misclassified into two phases) would VACUUM it twice for no benefit and
// would also double-count in the progress denominator. Guard the invariant.
func TestMaintenanceList_NoDuplicates(t *testing.T) {
	all := append(append(append([]string{}, HeavyUpdateTables...), AppendOnlyTables...), PromotionTables...)
	seen := make(map[string]int, len(all))
	for _, name := range all {
		seen[name]++
	}
	for name, n := range seen {
		assert.Equalf(t, 1, n, "table %q appears %d times across the maintenance slices", name, n)
	}
	// Fixed expectation after dropping 12 wbscraper test-only tables: the prod
	// schema has 62 tables (61 raw/loader tables + stock_products). If this
	// number drifts, update it deliberately and explain why in the commit.
	assert.Equal(t, 62, len(all), "expected 62 prod tables in the maintenance list")
}
