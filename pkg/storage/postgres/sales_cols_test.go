package postgres

import (
	"strings"
	"testing"
)

// TestInsertSaleRowCols_MatchesColumnCount guards against the most likely Stage-3 bug:
// a mismatch between insertSaleRowCols (which feeds BuildMultiRowInsert's $1..$N placeholder
// generation) and the actual column list in insertSaleRowPrefixSQL. Such a mismatch is a
// runtime PG error ("INSERT has more/fewer expressions than target columns") that only
// surfaces at first download — this test catches it at compile-test time.
func TestInsertSaleRowCols_MatchesColumnCount(t *testing.T) {
	// Extract the column list: text between the first '(' and the ')' before 'VALUES'.
	start := strings.Index(insertSaleRowPrefixSQL, "(")
	end := strings.Index(insertSaleRowPrefixSQL, ")")
	if start < 0 || end < 0 || end < start {
		t.Fatalf("cannot locate column list parentheses in insertSaleRowPrefixSQL")
	}
	colList := insertSaleRowPrefixSQL[start+1 : end]
	// Column names are comma-separated; count top-level commas + 1.
	// (No nested parens in the sales column list, so a flat comma split is exact.)
	parts := strings.Split(colList, ",")
	got := len(parts)

	if got != insertSaleRowCols {
		t.Errorf("insertSaleRowCols = %d, but insertSaleRowPrefixSQL has %d columns — "+
			"BuildMultiRowInsert will generate wrong $N placeholders → runtime SQL error",
			insertSaleRowCols, got)
	}
}
