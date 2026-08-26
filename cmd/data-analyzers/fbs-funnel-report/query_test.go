package main

import (
	"strings"
	"testing"
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

// Зрелость когорт: по факту данных — «в пути» ≤ 10% заказов когорты.
// Возрастная метрика (≥ p90 выкупов) занижала срок: 13.08.2026 в wb_data_prod
// в 12–13 сут имела 54% «в пути», но помечалась зрелой.
func TestMarkMaturity(t *testing.T) {
	cohorts := []CohortFeedRow{
		{Cohort: "2026-08-05", Orders: 1000, InFlight: 29},  // 2.9% в пути → зрелая
		{Cohort: "2026-08-13", Orders: 1000, InFlight: 536}, // 53.6% в пути → незрелая (возраст 13 сут!)
		{Cohort: "2026-08-24", Orders: 1000, InFlight: 998}, // молодая → незрелая
		{Cohort: "2026-08-25"},                              // нет заказов → незрелая
	}
	markMaturity(cohorts)
	if !cohorts[0].Mature {
		t.Error("когорта с 2.9% «в пути» должна быть зрелой")
	}
	if cohorts[1].Mature {
		t.Error("когорта 13.08 с 53.6% «в пути» не должна быть зрелой независимо от возраста")
	}
	if cohorts[2].Mature {
		t.Error("молодая когорта с 99.8% «в пути» не должна быть зрелой")
	}
	if cohorts[3].Mature {
		t.Error("когорта без заказов не должна помечаться зрелой")
	}

	// Граница правила — ровно 10%: зрелая (≤, не <).
	edge := []CohortFeedRow{{Cohort: "2026-08-06", Orders: 100, InFlight: 10}}
	markMaturity(edge)
	if !edge[0].Mature {
		t.Error("ровно 10% «в пути» — ещё зрелая (граница включается)")
	}
}
