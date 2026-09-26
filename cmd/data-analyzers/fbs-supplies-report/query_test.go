package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Guard-инварианты куба поставок: границы МСК, исключение кроссбордера,
// окно-параметр, полнота бакетов и правило до-приёмочных отмен.
func TestSuppliesQueryGuards(t *testing.T) {
	q := suppliesQuery
	checks := map[string]string{
		"кроссбордер исключён":            "cross_border_type = 0",
		"день формирования в МСК":         "AT TIME ZONE 'Europe/Moscow'",
		"окно по дню формирования":        "($1::date IS NULL OR (sp.created_at AT TIME ZONE 'Europe/Moscow')::date >= $1::date)",
		"отмена до приёмки":               "c.first_cancel_seen < sp.scan_dt",
		"статусы отмен":                   "'canceled', 'canceled_by_client', 'declined_by_client'",
		"бакет 6ч":                        "/ 3600.0 <= 6",
		"бакет 12ч":                       "/ 3600.0 <= 12",
		"бакет 18ч":                       "/ 3600.0 <= 18",
		"бакет 24ч":                       "/ 3600.0 <= 24",
		"бакет 36ч":                       "/ 3600.0 <= 36",
		"бакет 48ч":                       "/ 3600.0 <= 48",
		"бакет 72ч":                       "/ 3600.0 <= 72",
		"бакет >72ч":                      "/ 3600.0 > 72",
		"лаг от первого задания":          "EXTRACT(EPOCH FROM (sp.scan_dt - min(o.created_at)))",
		"SLA-фильтр: не отменено до scan": "(c.first_cancel_seen IS NULL OR c.first_cancel_seen >= sp.scan_dt)",
	}
	for name, substr := range checks {
		if !strings.Contains(q, substr) {
			t.Errorf("suppliesQuery потерял %s: нет %q", name, substr)
		}
	}
	if got := strings.Count(q, "FILTER (WHERE sp.scan_dt IS NOT NULL AND"); got != 8 {
		t.Errorf("бакетов ожидания должно быть 8, найдено %d", got)
	}
	// бакеты должны считать только задания, не отменённые до приёмки
	if got := strings.Count(q, "AND (c.first_cancel_seen IS NULL OR c.first_cancel_seen >= sp.scan_dt)"); got != 8 {
		t.Errorf("SLA-условие в бакетах должно повторяться 8 раз, найдено %d", got)
	}
}

// Подписи и границы бакетов согласованы: 8 бакетов, последний — бесконечный.
func TestHistBucketsConsistent(t *testing.T) {
	if len(histBuckets) != 8 || len(histUpper) != 8 {
		t.Fatalf("бакетов должно быть 8: %d подписей, %d границ", len(histBuckets), len(histUpper))
	}
	if histUpper[7] != 1<<30 {
		t.Errorf("последний бакет должен быть открытым, got %d", histUpper[7])
	}
	for i := 1; i < 7; i++ {
		if histUpper[i] <= histUpper[i-1] {
			t.Errorf("границы бакетов не возрастают на %d: %d <= %d", i, histUpper[i], histUpper[i-1])
		}
	}
}

// P8-guard: константы бакетов синхронны на всех слоях — SQL (число FILTER),
// Go (histUpper), JS (BUCKET_UP в web/app.js), HTML (кнопки data-h порогов).
// Правка любого слоя без остальных роняет этот тест.
func TestBucketConstantsSyncAcrossLayers(t *testing.T) {
	m := regexp.MustCompile(`BUCKET_UP\s*=\s*\[([^\]]+)\]`).FindStringSubmatch(string(appJS))
	if m == nil {
		t.Fatal("web/app.js не содержит BUCKET_UP — guard не может проверить JS-константы")
	}
	parts := strings.Split(m[1], ",")
	if len(parts) != len(histUpper) {
		t.Fatalf("JS-бакетов %d, Go %d", len(parts), len(histUpper))
	}
	for i, p := range parts {
		want := "Infinity"
		if histUpper[i] != 1<<30 {
			want = fmt.Sprint(histUpper[i])
		}
		if got := strings.TrimSpace(p); got != want {
			t.Errorf("BUCKET_UP[%d]=%s, histUpper[%d]=%v — слои разошлись", i, got, i, histUpper[i])
		}
	}

	thrSeen := 0
	for _, hm := range regexp.MustCompile(`data-h="(\d+)"`).FindAllStringSubmatch(string(indexTmpl), -1) {
		v, err := strconv.Atoi(hm[1])
		if err != nil {
			t.Fatalf("data-h=%q не число", hm[1])
		}
		thrSeen++
		ok := false
		for _, u := range histUpper {
			if u == v {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("порог кнопки %dч не совпадает ни с одной границей бакета %v", v, histUpper)
		}
	}
	if thrSeen == 0 {
		t.Error("в index.tmpl.html нет кнопок порога data-h — guard слеп к HTML-слою")
	}

	if got := strings.Count(suppliesQuery, "FILTER (WHERE sp.scan_dt IS NOT NULL AND"); got != len(histUpper) {
		t.Errorf("SQL-бакетов %d, Go-границ %d", got, len(histUpper))
	}
}

// Golden-регрессия (срез 26.09.2026, прод): WB-GI-283173064 — лаг 32.0 ч,
// 2222 задания, бакеты [0,0,315,672,1235,0,0,0]. Воспроизводит математику
// браузера (okCount = сумма бакетов с верхней границей ≤ порога; вердикт —
// лаг ≤ порога) без обращения к БД. Сверено с ручным SQL в аудите 26.09.
func TestGoldenSupplyBucketMath(t *testing.T) {
	hist := [8]int{0, 0, 315, 672, 1235, 0, 0, 0}
	den := 0
	for _, v := range hist {
		den += v
	}
	okN := func(thr int) int {
		n := 0
		for k, v := range hist {
			if histUpper[k] <= thr {
				n += v
			}
		}
		return n
	}
	if den != 2222 {
		t.Fatalf("знаменатель %d, want 2222", den)
	}
	if okN(12) != 0 || okN(24) != 987 || okN(36) != 2222 {
		t.Errorf("ok12/24/36 = %d/%d/%d, want 0/987/2222", okN(12), okN(24), okN(36))
	}
	const lag = 32.0
	for _, tc := range []struct {
		thr  int
		want string
	}{{12, "bad"}, {24, "bad"}, {36, "ok"}} {
		got := "ok"
		if lag > float64(tc.thr) {
			got = "bad"
		}
		if got != tc.want {
			t.Errorf("вердикт thr=%dч: %s, want %s", tc.thr, got, tc.want)
		}
	}
}
