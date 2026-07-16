package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
