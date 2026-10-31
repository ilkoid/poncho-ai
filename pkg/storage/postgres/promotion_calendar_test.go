package postgres

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

// ============================================================================
// Integration test (real PostgreSQL). Gated on WB_TEST_PG — same convention
// as fsa_repo_test.go / wbscraper_repo_test.go, so `go test ./...` skips it:
//
//	WB_TEST_PG=1 PG_PWD=... go test ./pkg/storage/postgres/ -run TestPgGetCalendarPromotionIDsByType -v
//
// Runs against wb_data_test (NEVER prod). Rows use a dedicated 9750xxx ID
// range and are deleted on completion, so repeats never collide with real data.
// ============================================================================

// TestPgGetCalendarPromotionIDsByType_EndDateCut — PG-близнец
// sqlite.TestGetCalendarPromotionIDsByType_EndDateCut: отсечка оконченных
// акций (422 «неприменим» от /nomenclatures, append-only таблица промо).
func TestPgGetCalendarPromotionIDsByType_EndDateCut(t *testing.T) {
	if !pgEnabled() {
		t.Skipf("skipping PostgreSQL integration test: set WB_TEST_PG=1 (and PG_PWD) to run")
	}
	ctx := context.Background()

	// Тот же путь построения DSN, что у ночных утилит (V2StorageConfig →
	// GetEffectiveDSN); база запинена на wb_data_test.
	dsn, err := config.V2StorageConfig{Backend: "postgres", PgDatabase: "wb_data_test"}.GetEffectiveDSN()
	if err != nil {
		t.Skipf("skipping: cannot build PG DSN (is PG_PWD set?): %v", err)
	}
	pool, err := NewPool(ctx, dsn)
	if err != nil {
		t.Skipf("skipping: cannot reach wb_data_test (%v) — is the PG test DB up?", err)
	}
	defer pool.Close()

	repo := NewPgPromotionRepo(pool.DB())
	if err := repo.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	future := time.Now().AddDate(0, 0, 30).Format("2006-01-02")

	// Тестовый диапазон 9750001..9750006 — вне реальных ID акций WB.
	cases := []struct {
		id       int
		end      *string
		typ      string
		included bool
	}{
		{9750001, strPtr(yesterday + "T20:59:59Z"), "regular", false}, // окончена вчера
		{9750002, strPtr(today + "T20:59:59Z"), "regular", true},      // день окончания — ещё проходит
		{9750003, strPtr(future + "T20:59:59Z"), "regular", true},     // будущая
		{9750004, nil, "regular", true},                               // NULL end — защитно включаем
		{9750005, strPtr(""), "regular", true},                        // пустой end — защитно включаем
		{9750006, strPtr(future + "T20:59:59Z"), "auto", false},       // автоакция — как и раньше
	}

	const lo, hi = 9750001, 9750006
	_, err = pool.DB().Exec(ctx, "DELETE FROM wb_calendar_promotions WHERE promotion_id BETWEEN $1 AND $2", lo, hi)
	if err != nil {
		t.Fatalf("cleanup before: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.DB().Exec(context.Background(),
			"DELETE FROM wb_calendar_promotions WHERE promotion_id BETWEEN $1 AND $2", lo, hi)
	})

	for _, c := range cases {
		if _, err := pool.DB().Exec(ctx,
			"INSERT INTO wb_calendar_promotions (promotion_id, name, start_date, end_date, type) VALUES ($1, 'pg-calendar-cut-test', $2, $3, $4)",
			c.id, yesterday+"T00:00:00Z", c.end, c.typ); err != nil {
			t.Fatalf("insert promo %d: %v", c.id, err)
		}
	}

	ids, err := repo.GetCalendarPromotionIDsByType(ctx, "auto", today)
	if err != nil {
		t.Fatalf("GetCalendarPromotionIDsByType: %v", err)
	}

	var got []int
	for _, id := range ids {
		if id >= lo && id <= hi {
			got = append(got, id)
		}
	}
	var want []int
	for _, c := range cases {
		if c.included {
			want = append(want, c.id)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ids (test range) = %v, want %v", got, want)
	}
}

func strPtr(s string) *string { return &s }
