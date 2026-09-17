package postgres

import (
	"strings"
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/wb"
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

// prefixColumns extracts the flat comma-separated column list from an
// "INSERT INTO t ( ... ) VALUES" prefix.
func prefixColumns(t *testing.T, prefixSQL string) []string {
	t.Helper()
	start := strings.Index(prefixSQL, "(")
	end := strings.Index(prefixSQL, ")")
	if start < 0 || end < 0 || end < start {
		t.Fatalf("cannot locate column list parentheses in prefix SQL")
	}
	cols := strings.Split(prefixSQL[start+1:end], ",")
	for i := range cols {
		cols[i] = strings.TrimSpace(cols[i])
	}
	return cols
}

// TestOnConflictDoUpdate_CoversAllNonKeyColumns: after the 04.09.2026 incident
// (rewrite-INSERT без ON CONFLICT падал на sales_rrd_id_key), все insert-пути —
// upsert c DO UPDATE. Рукописный SET-список должен покрывать каждый столбец
// INSERT, кроме ключа конфликта rrd_id: пропущенный столбец молча перестанет
// обновляться при повторной выдаче строки finance-API.
func TestOnConflictDoUpdate_CoversAllNonKeyColumns(t *testing.T) {
	for _, tc := range []struct {
		name       string
		prefixSQL  string
		conflictSQL string
		colsTotal  int
	}{
		{"sales", insertSaleRowPrefixSQL, insertSaleRowOnConflictSQL, insertSaleRowCols},
		{"service_records", insertServiceRecPrefixSQL, insertServiceRecOnConflictSQL, insertServiceRecCols},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cols := prefixColumns(t, tc.prefixSQL)
			if len(cols) != tc.colsTotal {
				t.Fatalf("%s: colsTotal = %d, prefix has %d", tc.name, tc.colsTotal, len(cols))
			}
			assignments := 0
			for _, col := range cols {
				if col == "rrd_id" {
					continue
				}
				want := col + " = EXCLUDED." + col
				if !strings.Contains(tc.conflictSQL, want) {
					t.Errorf("%s: DO UPDATE не покрывает столбец %q (нет %q)", tc.name, col, want)
					continue
				}
				assignments++
			}
			// Каждое присваивание встречается ровно один раз.
			if n := strings.Count(tc.conflictSQL, "= EXCLUDED."); n != assignments {
				t.Errorf("%s: в DO UPDATE %d присваиваний, ожидалось %d (все, кроме rrd_id)", tc.name, n, assignments)
			}
		})
	}
}

// TestDedupByRrdID_KeepLastWins: дедуп перед multi-row INSERT с ON CONFLICT DO UPDATE
// обязателен — один и тот же rrd_id дважды в одном стейтменте даёт
// «cannot affect row a second time». Поздние вхождения (корректировки WB) важнее.
func TestDedupByRrdID_KeepLastWins(t *testing.T) {
	rows := []wb.RealizationReportRow{
		{RrdID: 1, Quantity: 1},
		{RrdID: 2, Quantity: 1},
		{RrdID: 1, Quantity: 5}, // дубль rrd_id=1: поздняя версия
		{RrdID: 3, Quantity: 1},
	}
	got := dedupByRrdID(rows)
	if len(got) != 3 {
		t.Fatalf("dedupByRrdID len = %d, want 3 (%v)", len(got), got)
	}
	byID := map[int]int{}
	for _, r := range got {
		byID[r.RrdID] = r.Quantity
	}
	if byID[1] != 5 {
		t.Errorf("rrd_id=1 Quantity = %d, want 5 (последнее вхождение)", byID[1])
	}
	if byID[2] != 1 || byID[3] != 1 {
		t.Errorf("уникальные строки потеряны: %v", got)
	}

	if got := dedupByRrdID(nil); len(got) != 0 {
		t.Errorf("dedupByRrdID(nil) = %v, want empty", got)
	}
	single := []wb.RealizationReportRow{{RrdID: 9, Quantity: 1}}
	if got := dedupByRrdID(single); len(got) != 1 || got[0].RrdID != 9 {
		t.Errorf("dedupByRrdID(single) = %v, want как есть", got)
	}
}
