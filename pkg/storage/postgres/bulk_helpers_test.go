package postgres

import (
	"strconv"
	"strings"
	"testing"
)

func TestBuildMultiRowInsert_SingleRowSingleCol(t *testing.T) {
	got := BuildMultiRowInsert("INSERT INTO t (a) VALUES ", "ON CONFLICT DO NOTHING", 1, 1)
	want := "INSERT INTO t (a) VALUES ($1) ON CONFLICT DO NOTHING"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestBuildMultiRowInsert_SingleRowMultipleCols(t *testing.T) {
	got := BuildMultiRowInsert("INSERT INTO t (a,b,c) VALUES ", "ON CONFLICT DO NOTHING", 1, 3)
	want := "INSERT INTO t (a,b,c) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestBuildMultiRowInsert_MultipleRowsMultipleCols(t *testing.T) {
	got := BuildMultiRowInsert("INSERT INTO t (a,b) VALUES ", "ON CONFLICT (a) DO UPDATE SET b=EXCLUDED.b", 3, 2)
	want := "INSERT INTO t (a,b) VALUES ($1, $2), ($3, $4), ($5, $6) ON CONFLICT (a) DO UPDATE SET b=EXCLUDED.b"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// TestBuildMultiRowInsert_EmptyOnConflict покрывает plain-INSERT путь
// (rewrite-mode в PgSalesRepo.SavePlain): ON CONFLICT не нужен, т.к.
// диапазон уже удалён и конфликты по rrd_id невозможны. Проверяем, что
// пустой onConflict не оставляет мусора в запросе.
func TestBuildMultiRowInsert_EmptyOnConflict(t *testing.T) {
	got := BuildMultiRowInsert("INSERT INTO t (a,b) VALUES ", "", 2, 2)
	want := "INSERT INTO t (a,b) VALUES ($1, $2), ($3, $4) "
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
	// Явно: никакого ON CONFLICT в plain-варианте быть не должно.
	if strings.Contains(got, "ON CONFLICT") {
		t.Errorf("plain INSERT must NOT contain ON CONFLICT, got: %s", got)
	}
}

func TestBuildMultiRowInsert_PlaceholderNumbering(t *testing.T) {
	// Verify that placeholders are numbered sequentially across rows.
	const rows, cols = 5, 4
	got := BuildMultiRowInsert("INSERT INTO t VALUES ", "", rows, cols)

	// Count $N occurrences and verify sequential numbering.
	for i := 1; i <= rows*cols; i++ {
		needle := "$" + strconv.Itoa(i)
		if !strings.Contains(got, needle) {
			t.Errorf("placeholder %s not found in output: %s", needle, got)
		}
	}
}

func TestBuildMultiRowInsert_ExceedsLimit(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for exceeding PG param limit")
		}
		if !strings.Contains(r.(string), "65535") {
			t.Errorf("panic message should mention 65535, got: %v", r)
		}
	}()
	BuildMultiRowInsert("INSERT INTO t VALUES ", "", 1000, 100) // 100,000 > 65,535
}

func TestBuildMultiRowInsert_ZeroRowCount(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for zero rowCount")
		}
	}()
	BuildMultiRowInsert("INSERT INTO t VALUES ", "", 0, 5)
}

func TestBuildMultiRowInsert_ZeroColCount(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for zero colCount")
		}
	}()
	BuildMultiRowInsert("INSERT INTO t VALUES ", "", 5, 0)
}

func TestBuildMultiRowInsert_AtLimit(t *testing.T) {
	// Exactly at the limit should not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("should not panic at exact limit: %v", r)
		}
	}()
	// 65535 / 3 = 21845 rows exactly at limit
	got := BuildMultiRowInsert("INS VALUES ", "CONFLICT", 21845, 3)
	if !strings.Contains(got, "$65535") {
		t.Error("last placeholder $65535 should be present")
	}
	if strings.Contains(got, "$65536") {
		t.Error("placeholder $65536 should NOT be present (exceeds limit)")
	}
}