package postgres

import (
	"strings"
	"testing"
)

// Guard-тесты против самого вероятного бага PG-адаптера: рассинхрон
// <name>Cols (который кормит BuildMultiRowInsert генерацией $1..$N) и
// фактического списка колонок в PrefixSQL. Такой рассинхрон — рантайм-ошибка
// PG ("INSERT has more/fewer expressions than target columns"), которая
// всплывает только на первой загрузке.
func fbsCountColumns(t *testing.T, prefixSQL, name string) int {
	t.Helper()
	start := strings.Index(prefixSQL, "(")
	end := strings.Index(prefixSQL, ")")
	if start < 0 || end < 0 || end < start {
		t.Fatalf("cannot locate column list parentheses in %s", name)
	}
	return len(strings.Split(prefixSQL[start+1:end], ","))
}

func TestFBSInsertCols_MatchColumnCounts(t *testing.T) {
	cases := []struct {
		name      string
		prefixSQL string
		cols      int
	}{
		{"insertFBSOrderPrefixSQL", insertFBSOrderPrefixSQL, insertFBSOrderCols},
		{"insertFBSStatusPrefixSQL", insertFBSStatusPrefixSQL, insertFBSStatusCols},
		{"insertFBSStatusLogPrefixSQL", insertFBSStatusLogPrefixSQL, insertFBSStatusCols},
		{"insertOrderFeedPrefixSQL", insertOrderFeedPrefixSQL, insertOrderFeedCols},
	}
	for _, tc := range cases {
		if got := fbsCountColumns(t, tc.prefixSQL, tc.name); got != tc.cols {
			t.Errorf("%s: cols const = %d, but SQL has %d columns — wrong $N placeholders",
				tc.name, tc.cols, got)
		}
	}
}

// On-conflict ключи: журнал статусов обязан дедуплиться по тройке
// (order_id, supplier_status, wb_status) — иначе история распухнет.
func TestFBSOnConflictClauses(t *testing.T) {
	if !strings.Contains(insertFBSOrderOnConflictSQL, "ON CONFLICT (id)") {
		t.Error("orders upsert must conflict on (id)")
	}
	if !strings.Contains(insertFBSStatusOnConflictSQL, "ON CONFLICT (order_id)") {
		t.Error("statuses upsert must conflict on (order_id)")
	}
	if !strings.Contains(insertFBSStatusLogOnConflictSQL, "ON CONFLICT (order_id, supplier_status, wb_status)") {
		t.Error("status log must dedup on (order_id, supplier_status, wb_status)")
	}
	if !strings.Contains(insertOrderFeedOnConflictSQL, "ON CONFLICT (srid)") {
		t.Error("order feed upsert must conflict on (srid)")
	}
}

// Full-chunk SQL не должен превышать лимит параметров PG (65535).
func TestFBSFullChunkSQLParamLimit(t *testing.T) {
	limits := []struct {
		name   string
		params int
	}{
		{"orders", pgFBSChunkSize * insertFBSOrderCols},
		{"statuses", pgFBSChunkSize * insertFBSStatusCols},
		{"status log", pgFBSChunkSize * insertFBSStatusCols},
		{"order feed", pgFBSChunkSize * insertOrderFeedCols},
	}
	for _, l := range limits {
		if l.params > 65535 {
			t.Errorf("%s: %d params exceeds PG limit 65535", l.name, l.params)
		}
	}
}

// Кандидаты статусов: SQL обязан покрывать три источника полноты —
// свежее окно, задания без статуса, незакрытые задания.
func TestLoadFBSStatusCandidatesSQL_CoversAllGaps(t *testing.T) {
	for _, want := range []string{
		"o.created_at >= $1",
		"s.order_id IS NULL",
		"s.wb_status NOT IN",
	} {
		if !strings.Contains(loadFBSStatusCandidatesSQL, want) {
			t.Errorf("candidates SQL missing gap guard %q:\n%s", want, loadFBSStatusCandidatesSQL)
		}
	}
	// Все терминальные wb-статусы из сваггера (03-orders-fbs.yaml:589-607).
	for _, terminal := range []string{"sold", "canceled", "canceled_by_client", "declined_by_client", "defect", "canceled_by_carrier"} {
		if !strings.Contains(fbsTerminalWbStatuses, terminal) {
			t.Errorf("terminal wb status %q missing from fbsTerminalWbStatuses", terminal)
		}
	}
}
