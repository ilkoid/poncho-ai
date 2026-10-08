package main

import (
	"strings"
	"testing"
)

// Guard-инварианты SQL снапшота: universe по складу конфига, мера free,
// пороги параметрами, пин дат среза параметрами ($1 стоки, $5 остатки —
// иначе SQL, пересчитав MAX сам, навесит свежие данные на чужую дату),
// дедуп-джойны, идемпотентный upsert, полный год по символам 2-3,
// связка article↔vendor_code, presented по сетке card_sizes.
func TestFillDailyGuards(t *testing.T) {
	checks := map[string]string{
		"universe: свободный остаток":        "WHERE COALESCE(r.free, 0) > 0",
		"склад 1С — параметр":                "storage_name = $3",
		"дата rests — пин среза $5":          "r.snapshot_date = $5",
		"дата стоков — пин среза $1":         "WHERE snapshot_date = $1",
		"порог шт — параметр":                "COALESCE(w.qty, 0) > $2",
		"порог размеров — параметр":          "COALESCE(p.presented_cnt, 0) < $4",
		"presented по сетке card_sizes":      "FROM public.card_sizes cs",
		"связка артикул-карточка":            "c.vendor_code = o.article",
		"дедуп карточек в MIN(nm_id)":        "MIN(nm_id)",
		"дедуп карточек по vendor_code":      "GROUP BY vendor_code",
		"дедуп справочника 1С по артикулу":   "DISTINCT ON (article)",
		"остатки 1С по всем guid артикула":   "GROUP BY g.article",
		"остаток размера = сумма складов":    "GROUP BY nm_id, chrt_id",
		"год по символам 2-3":                "substring(o.article FROM 2 FOR 2)",
		"год 20XX":                           "2000 + substring(o.article FROM 2 FOR 2)::int",
		"идемпотентный upsert":               "ON CONFLICT (snapshot_date, article) DO UPDATE SET",
		"категория L1 с fallback":            "COALESCE(NULLIF(o.category_level1_name, ''), o.category, '')",
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
		"дата стоков — пин среза $1": "WHERE snapshot_date = $1",
		"universe собранного дня":    "WHERE snapshot_date = $1 AND nm_id IS NOT NULL",
		"universe-джойн на месте":    "JOIN universe u",
		"идемпотентный upsert":       "ON CONFLICT (snapshot_date, nm_id, chrt_id) DO UPDATE SET",
		"chrt глобально уникален":    "ON w.chrt_id = cs.chrt_id",
	}
	for name, substr := range checks {
		if !strings.Contains(fillSizesSQL, substr) {
			t.Errorf("fillSizesSQL потерял %s: нет %q", name, substr)
		}
	}
}

// Пересборка дня: сначала DELETE обеих таблиц за дату (иначе «призраки»
// повторного прогона), потом вставка; всё одним fillSnapshot-транзакцией.
func TestSnapshotReplaceGuards(t *testing.T) {
	checks := []struct{ name, sql, substr string }{
		{"daily: снос даты перед вставкой", deleteDailySQL, "DELETE FROM analytical.size_availability_daily WHERE snapshot_date = $1"},
		{"sizes: снос даты перед вставкой", deleteSizesSQL, "DELETE FROM analytical.size_availability_sizes_daily WHERE snapshot_date = $1"},
	}
	for _, c := range checks {
		if !strings.Contains(c.sql, c.substr) {
			t.Errorf("%s: нет %q", c.name, c.substr)
		}
	}
}

// coverageSQL — единственное место, где легален MAX(snapshot_date): его
// результат пиннится в $1/$5 и печатается в шапке. Склад конфига проверяется
// счётчиком строк последнего среза (guard на переименование склада).
func TestCoverageGuards(t *testing.T) {
	for name, substr := range map[string]string{
		"последний срез стоков":        "MAX(snapshot_date)",
		"последний срез остатков 1С":   "FROM public.onec_rests",
		"склад конфига — параметр":     "storage_name = $1",
		"строки склада последнего сре": "snapshot_date = (SELECT MAX(snapshot_date) FROM public.onec_rests)",
		"размеры карточек считаются":   "COUNT(*) FROM public.card_sizes",
	} {
		if !strings.Contains(coverageSQL, substr) {
			t.Errorf("coverageSQL потерял %s: нет %q", name, substr)
		}
	}
}

// summarize считает KPI по колонкам куба: недоступные, из них без карточки и
// с нулём представленных, сумма свободного остатка 1С. Значение SizesPres,
// РАВНОЕ порогу, — доступно (граница «< порога»).
func TestSummarize(t *testing.T) {
	cube := &AvailabilityData{
		Meta: Meta{MinSizes: 4},
		Arts: ArticleCol{
			Article:   []string{"A1", "A2", "A3", "A4", "A5"},
			SizesPres: []int{5, 2, 0, 0, 4},
			HasCard:   []bool{true, true, true, false, true},
			OnecFree:  []int64{100, 50, 70, 10, 10},
		},
	}
	unavail, nocard, zero, stuck := summarize(cube)
	if unavail != 3 || nocard != 1 || zero != 1 || stuck != 130 {
		t.Fatalf("summarize: got (%d,%d,%d,%d), want (3,1,1,130)", unavail, nocard, zero, stuck)
	}
}

// html — три состояния: "auto" → reports/<имя>-<дата снапшота>.html,
// "" → отключено, явный путь → как есть.
func TestResolveHTMLPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"auto", "reports/size-availability-2026-10-08.html"},
		{"", ""},
		{"/tmp/out.html", "/tmp/out.html"},
	}
	for _, c := range cases {
		if got := resolveHTMLPath(c.in, "2026-10-08", "size-availability"); got != c.want {
			t.Errorf("resolveHTMLPath(%q) = %q, want %q", c.in, got, c.want)
		}
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
