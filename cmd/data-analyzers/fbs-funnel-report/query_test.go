package main

import (
	"strings"
	"testing"
	"time"
)

// SQL-инварианты: то, что однажды уже ломало аналитику на живых данных.
// Проверяем константы, а не поведение PG: если запрос потеряет MSK-каст,
// is_mp-фильтр или COALESCE у сумм — упадёт здесь, а не молча в отчёте.
func TestSQLInvariants(t *testing.T) {
	feedQueries := map[string]string{
		"funnelTotals":      funnelTotalsQuery,
		"funnelDaily":       funnelDailyQuery,
		"cohortsFeed":       cohortsFeedQuery,
		"cancelReasons":     cancelReasonsQuery,
		"lifecycleOverall":  lifecycleOverallQuery,
		"lifecycleByCohort": lifecycleByCohortQuery,
		"geo":               geoQuery,
		"topNm":             topNmQuery,
		"crossCheck":        crossCheckQuery,
	}
	for name, q := range feedQueries {
		// is_mp с алиасом таблицы (f.is_mp) — тот же фильтр.
		if !strings.Contains(q, "OR is_mp") && !strings.Contains(q, "OR f.is_mp") {
			t.Errorf("%s: нет is_mp-фильтра ($1 OR is_mp) — отчёт посчитает FBW", name)
		}
		if strings.Contains(q, "GROUP BY") && !strings.Contains(q, "AT TIME ZONE 'Europe/Moscow'") {
			t.Errorf("%s: группировка по дню без Europe/Moscow — границы дней съедут от МСК", name)
		}
		if got, want := strings.Count(q, "sum("), strings.Count(q, "COALESCE(sum("); got != want {
			t.Errorf("%s: не каждая sum() обёрнута COALESCE (sum=%d, coalesce=%d) — NULL сломает scan", name, got, want)
		}
	}

	v3Queries := map[string]string{
		"cohortsV3": cohortsV3Query,
		"v3Totals":  v3TotalsQuery,
	}
	for name, q := range v3Queries {
		if strings.Contains(q, "is_mp") {
			t.Errorf("%s: v3-таблицы не имеют is_mp — запрос упадёт", name)
		}
		if strings.Contains(q, "GROUP BY") && !strings.Contains(q, "AT TIME ZONE 'Europe/Moscow'") {
			t.Errorf("%s: группировка по дню без Europe/Moscow", name)
		}
	}

	// Статусные литералы — сваггер 11-analytics.yaml:6414-6439, 03-orders-fbs.yaml:664.
	if !strings.Contains(funnelTotalsQuery, "'returnDefective'") || !strings.Contains(funnelTotalsQuery, "'buyout'") {
		t.Error("funnelTotals: статусы ленты не соответствуют enum order-feed")
	}
	if !strings.Contains(cohortsV3Query, "'declined_by_client'") || !strings.Contains(cohortsV3Query, "'sold'") {
		t.Error("cohortsV3: статусы v3 не соответствуют enum wbStatus")
	}
}

// queryParams: since="" уходит как NULL ($2::date IS NULL → без ограничения).
func TestQueryParams(t *testing.T) {
	q := queryParams{allModels: false, since: ""}
	args := q.args()
	if len(args) != 2 {
		t.Fatalf("args = %d, want 2", len(args))
	}
	if args[1] != nil {
		t.Errorf("пустое since должно уходить nil (NULL), got %v", args[1])
	}
	q.since = "2026-08-18"
	if got := q.args()[1]; got != "2026-08-18" {
		t.Errorf("since = %v, want строку-дату", got)
	}
	if len(q.sinceOnly()) != 1 {
		t.Errorf("sinceOnly = %d аргумента, want 1", len(q.sinceOnly()))
	}
}

// Доля выкупа: -1 (нет завершившихся) вместо NaN от деления на ноль.
func TestBuyoutPctSentinel(t *testing.T) {
	if got := (DailyEvent{}).BuyoutPct(); got != -1 {
		t.Errorf("пустой день: BuyoutPct = %v, want -1", got)
	}
	if got := (FunnelTotals{Buyout: 25136, Cancel: 25981, Returns: 1229}).BuyoutPct(); got < 47 || got > 49 {
		t.Errorf("BuyoutPct = %.1f, want ~48", got)
	}
	if got := (CohortFeedRow{}).BuyoutPct(); got != -1 {
		t.Errorf("пустая когорта: BuyoutPct = %v, want -1", got)
	}
}

// Зрелость когорт: возраст (с конца дня создания) ≥ p90 цикла.
func TestMarkMaturity(t *testing.T) {
	p90 := 294.2 // ~12.3 сут — эмпирика wb_data_test на 25.08.2026
	lc := Lifecycle{P90H: &p90}
	cohorts := []CohortFeedRow{
		{Cohort: "2026-08-10"}, // возраст ~14.5 сут → зрелая
		{Cohort: "2026-08-24"}, // возраст ~1.5 сут → незрелая
		{Cohort: "не-дата"},    // парсинг падает → остаётся незрелой
	}
	markMaturity(cohorts, lc, time.Date(2026, 8, 25, 12, 0, 0, 0, mskLoc()))
	if !cohorts[0].Mature {
		t.Error("когорта 2026-08-10 при p90=294ч должна быть зрелой")
	}
	if cohorts[1].Mature {
		t.Error("когорта 2026-08-24 при p90=294ч не должна быть зрелой")
	}
	if cohorts[2].Mature {
		t.Error("непарсируемая дата не должна становиться зрелой")
	}

	// Нет p90 (выкупов не было) — никто не помечен зрелым.
	cohorts2 := []CohortFeedRow{{Cohort: "2026-08-01"}}
	markMaturity(cohorts2, Lifecycle{}, time.Date(2026, 8, 25, 12, 0, 0, 0, mskLoc()))
	if cohorts2[0].Mature {
		t.Error("без p90 зрелость неопределима — когорта не должна помечаться")
	}
}
