package main

import (
	"strings"
	"testing"
	"time"
)

// Guard-инварианты fillSQL: окно параметрами (не в SQL), МСК-день текущего
// статуса, полный набор статусов ленты, деньги по buyout/'S*'/'R*'-дискриминатору,
// srid-сшивка трёх RAW-источников, регион с fallback, GROUP BY весь grain.
func TestFillSQLGuards(t *testing.T) {
	checks := map[string]string{
		"окно ленты — параметр $1":        "f.updated_at >= $1::timestamptz",
		"окно ленты — параметр $2":        "f.updated_at <  $2::timestamptz",
		"день — МСК-дата статуса":         "AT TIME ZONE 'Europe/Moscow'",
		"статус created":                  "feed.status = 'created'",
		"статус buyout (выкупили)":        "feed.status = 'buyout'",
		"статус cancel":                   "feed.status = 'cancel'",
		"возвраты = return + брак":        "feed.status IN ('return', 'returnDefective')",
		"валовая цена — только выкупы":    "SUM(feed.seller_price) FILTER (WHERE feed.status = 'buyout')",
		"продажи по sale_id 'S*'":         "FILTER (WHERE os.sale_id LIKE 'S%')",
		"возвраты по sale_id 'R*'":        "FILTER (WHERE os.sale_id LIKE 'R%')",
		"регион: orders по srid":          "LEFT JOIN public.orders o ON o.srid = feed.srid",
		"деньги: opsales по srid":         "JOIN feed ON feed.srid = os.srid",
		"регион fallback на opsales":      "COALESCE(NULLIF(o.region_name, ''),       NULLIF(a.region_name, ''),       '')",
		"контроль join-покрытия деньгами": "COUNT(a.srid)                   AS os_rows",
		"GROUP BY весь grain":             "GROUP BY 1, 2, 3, 4, 5, 6, 7, 8",
		"город доставки из ленты":         "COALESCE(f.destination_city, '')",
		"ФО доставки из ленты":            "COALESCE(f.destination_district, '')",
	}
	for name, substr := range checks {
		if !strings.Contains(fillSQL, substr) {
			t.Errorf("fillSQL потерял %s: нет %q", name, substr)
		}
	}
}

// Регрессия смоука 11.10: окно считалось от «сейчас−Nд» без усечения до
// полуночи МСК — первый день окна был обрезан временем запуска (за 04.10 в
// агрегат попали только строки с 23:20), а строки «сегодня» вставлялись за
// пределами DELETE-окна и роняли повторный прогон по PK.
func TestDayFloor_MidnightMSK(t *testing.T) {
	loc := moscowLoc()
	run := time.Date(2026, 10, 11, 23, 20, 56, 0, loc) // момент запуска смоука
	got := dayFloor(run.AddDate(0, 0, -7), loc)
	want := time.Date(2026, 10, 4, 0, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("from = %s, хочу полночь МСК %s", got, want)
	}
}
