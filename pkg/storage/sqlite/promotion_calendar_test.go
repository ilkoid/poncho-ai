package sqlite

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"
)

// TestGetCalendarPromotionIDsByType_EndDateCut — guard отсечки оконченных акций
// в фазе Calendar Nomenclatures (2026-10-29). Ручка /api/v1/calendar/promotions/
// nomenclatures отвечает 422 «неприменим» на закончившиеся промо, а
// wb_calendar_promotions append-only (upsert без удаления) — без date-фильтра
// фаза жгёт по 2 пустых запроса на каждую закрытую акцию ежедневно
// (VPS2: 286 запросов/день, ~3 мин фазы при лимите 600 мс/запрос).
//
// Границы: день окончания ещё проходит (финальный снапшот participating),
// NULL/пустой end_date остаются в выборке защитно (аномалия ≠ причина терять
// акцию), фильтр по type ('auto') работает как и раньше.
func TestGetCalendarPromotionIDsByType_EndDateCut(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open mem db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(PromotionV2SchemaSQL); err != nil {
		t.Fatalf("apply PromotionV2SchemaSQL: %v", err)
	}
	repo := &SQLiteSalesRepository{db: db}

	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	future := time.Now().AddDate(0, 0, 30).Format("2006-01-02")

	// (id, end_date, type, want) — end_date в ISO-формате API (с T…Z),
	// NULL и '' — защитные аномалии.
	cases := []struct {
		id       int
		end      sql.NullString
		typ      string
		included bool
	}{
		{9201, sql.NullString{String: yesterday + "T20:59:59Z", Valid: true}, "regular", false}, // окончена вчера
		{9202, sql.NullString{String: today + "T20:59:59Z", Valid: true}, "regular", true},     // день окончания — ещё проходит
		{9203, sql.NullString{String: future + "T20:59:59Z", Valid: true}, "regular", true},    // будущая
		{9204, sql.NullString{Valid: false}, "regular", true},                                   // NULL end — защитно включаем
		{9205, sql.NullString{String: "", Valid: true}, "regular", true},                        // пустой end — защитно включаем
		{9206, sql.NullString{String: future + "T20:59:59Z", Valid: true}, "auto", false},      // автоакция — как и раньше
	}
	for _, c := range cases {
		var end any
		if c.end.Valid {
			end = c.end.String
		}
		if _, err := db.ExecContext(ctx,
			"INSERT INTO wb_calendar_promotions (promotion_id, name, start_date, end_date, type) VALUES (?, 'test', ?, ?, ?)",
			c.id, yesterday+"T00:00:00Z", end, c.typ); err != nil {
			t.Fatalf("insert promo %d: %v", c.id, err)
		}
	}

	ids, err := repo.GetCalendarPromotionIDsByType(ctx, "auto", today)
	if err != nil {
		t.Fatalf("GetCalendarPromotionIDsByType: %v", err)
	}

	var want []int
	for _, c := range cases {
		if c.included {
			want = append(want, c.id)
		}
	}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
}
