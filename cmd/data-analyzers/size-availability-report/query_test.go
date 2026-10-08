package main

import (
	"strings"
	"testing"
)

// Guard-инварианты SQL снапшота: universe по складу конфига, мера free,
// пороги параметрами, идемпотентный upsert, полный год по символам 2-3,
// связка article↔vendor_code.
func TestFillDailyGuards(t *testing.T) {
	checks := map[string]string{
		"universe: свободный остаток":     "WHERE COALESCE(r.free, 0) > 0",
		"склад 1С — параметр":             "storage_name = $3",
		"порог шт — параметр":             "WHERE qty > $2",
		"порог размеров — параметр":       "COALESCE(p.presented_cnt, 0) < $4",
		"связка артикул-карточка":         "c.vendor_code = o.article",
		"остаток размера = сумма складов": "GROUP BY nm_id, chrt_id",
		"год по символам 2-3":             "substring(o.article FROM 2 FOR 2)",
		"год 20XX":                        "2000 + substring(o.article FROM 2 FOR 2)::int",
		"идемпотентный upsert":            "ON CONFLICT (snapshot_date, article) DO UPDATE SET",
		"категория L1 с fallback":         "COALESCE(NULLIF(o.category_level1_name, ''), o.category, '')",
		"снапшот последнего дня стоков":   "SELECT MAX(snapshot_date) AS d FROM public.stocks_daily_warehouses",
	}
	for name, substr := range checks {
		if !strings.Contains(fillDailySQL, substr) {
			t.Errorf("fillDailySQL потерял %s: нет %q", name, substr)
		}
	}
}

func TestFillSizesGuards(t *testing.T) {
	checks := map[string]string{
		"полная сетка из card_sizes": "FROM public.card_sizes cs",
		"нулевые размеры остаются":   "COALESCE(w.qty, 0)",
		"порог — параметр":           "COALESCE(w.qty, 0) > $2",
		"universe собранного дня":    "WHERE snapshot_date = $1 AND nm_id IS NOT NULL",
		"идемпотентный upsert":       "ON CONFLICT (snapshot_date, nm_id, chrt_id) DO UPDATE SET",
		"chrt глобально уникален":    "ON w.chrt_id = cs.chrt_id",
	}
	for name, substr := range checks {
		if !strings.Contains(fillSizesSQL, substr) {
			t.Errorf("fillSizesSQL потерял %s: нет %q", name, substr)
		}
	}
}

// summarize считает KPI по колонкам куба: недоступные, из них без карточки и
// с нулём представленных, сумма свободного остатка 1С.
func TestSummarize(t *testing.T) {
	cube := &AvailabilityData{
		Meta: Meta{MinSizes: 4},
		Arts: ArticleCol{
			Article:   []string{"A1", "A2", "A3", "A4"},
			SizesPres: []int{5, 2, 0, 0},
			HasCard:   []bool{true, true, true, false},
			OnecFree:  []int64{100, 50, 70, 10},
		},
	}
	unavail, nocard, zero, stuck := summarize(cube)
	if unavail != 3 || nocard != 1 || zero != 1 || stuck != 130 {
		t.Fatalf("summarize: got (%d,%d,%d,%d), want (3,1,1,130)", unavail, nocard, zero, stuck)
	}
}

// fmtInt — тысячи с пробелом.
func TestFmtInt(t *testing.T) {
	cases := map[int64]string{0: "0", 999: "999", 1000: "1 000", 1234567: "1 234 567"}
	for in, want := range cases {
		if got := fmtInt(in); got != want {
			t.Errorf("fmtInt(%d) = %q, want %q", in, got, want)
		}
	}
}
