package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCubeSQLInvariants — guard-инварианты куба (как TestSQLInvariants для XLSX):
// МСК-границы дней, is_mp-фильтр с параметром, COALESCE вокруг агрегатов,
// ограничение топ-городов, джойн 1С-словаря в onec-варианте.
func TestCubeSQLInvariants(t *testing.T) {
	checks := []struct{ name, sql, sub string }{
		{"куб: МСК-дни", cubeQuery, "AT TIME ZONE 'Europe/Moscow'"},
		{"куб: is_mp-параметр", cubeQuery, "($1 OR is_mp)"},
		{"куб: COALESCE суммы", cubeQuery, "COALESCE(sum(seller_price), 0)"},
		{"куб: окно по дате создания", cubeQuery, "created_at AT TIME ZONE 'Europe/Moscow')::date >= $2::date"},
		{"куб: топ-городов ограничен", cubeQuery, "LIMIT 300"},
		{"куб: группировка по бакету города", cubeQuery, "IN (SELECT destination_city FROM top_cities)"},
		{"словарь 1С: джойн cards", cubeNmQueryOneC, "LEFT JOIN public.cards"},
		{"словарь 1С: джойн onec_goods", cubeNmQueryOneC, "LEFT JOIN public.onec_goods"},
		{"словарь 1С: фолбэк на WB-предмет", cubeNmQueryOneC, "'WB · ' || c.subject_name"},
		{"словарь без 1С: джойн cards", cubeNmQueryPlain, "LEFT JOIN public.cards"},
		{"словарь без 1С: фолбэк WB", cubeNmQueryPlain, "'WB · ' || c.subject_name"},
	}
	for _, c := range checks {
		if !strings.Contains(c.sql, c.sub) {
			t.Errorf("%s: подстрока не найдена: %s", c.name, c.sub)
		}
	}
	if strings.Contains(cubeNmQueryPlain, "onec_goods") {
		t.Error("словарь без 1С не должен джойнить onec_goods")
	}
}

// TestExportHTMLSmoke — сборка дашборда на синтетике: токены заменены,
// echarts заинлайнен, payload на месте, файл цел.
func TestExportHTMLSmoke(t *testing.T) {
	cube := &CubeData{
		Meta: DashMeta{
			GeneratedAt: "25.08.2026 03:00", Db: "wb_data_test",
			FeedFrom: "2026-08-18", FeedTo: "2026-08-25",
			TotalOrders: 2, MatureAfterDays: 13, LifecycleMedianD: 3.1,
			OnecCategories: true, OnecCoveragePct: 99.8,
		},
		Dims: CubeDims{
			Cohort: []string{"2026-08-20"},
			Event:  []string{"2026-08-24"},
			Nm:     [][]string{{"123", "VC-1", "Футболка", "Футболки", "Одежда", "Футболки, майки", "Футболки"}},
			City:   []string{"Москва"},
			Status: []string{"buyout"},
			Ctype:  []string{""},
		},
		Facts: CubeFacts{
			Cohort: []int32{0}, Event: []int32{0}, Nm: []int32{0}, City: []int32{0},
			Status: []int32{0}, Ctype: []int32{0}, Cnt: []int32{2}, Kop: []int64{199900},
		},
	}
	path := filepath.Join(t.TempDir(), "dash.html")
	size, err := exportHTML(cube, path)
	if err != nil {
		t.Fatalf("exportHTML: %v", err)
	}
	if size < 500_000 {
		t.Errorf("файл подозрительно мал: %d байт (echarts не заинлайнен?)", size)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	html := string(b)
	for _, tok := range []string{tokEcharts, tokPayload, tokApp, tokStyle, tokGlossary} {
		if strings.Contains(html, tok) {
			t.Errorf("токен %s не заменён", tok)
		}
	}
	for _, marker := range []string{
		"echarts",          // библиотека заинлайнена
		`"total_orders":2`, // payload
		"chart-money",      // каркас виджетов
		"initChrome",       // приложение
		"--violet:#7758B3", // стили
		`class="g-sec"`,    // глоссарий (один источник с XLSX)
		"Когорта",          // термин глоссария
	} {
		if !strings.Contains(html, marker) {
			t.Errorf("маркер %q не найден", marker)
		}
	}
}

// TestPayloadScriptEscape — «</» в значениях куба не ломает разметку:
// в payload попадает экранированное «<\/».
func TestPayloadScriptEscape(t *testing.T) {
	cube := &CubeData{
		Dims: CubeDims{
			Event:  []string{"2026-08-24"},
			Status: []string{"buyout"},
			City:   []string{"</script><b>evil"},
		},
		Facts: CubeFacts{
			Event: []int32{0}, Status: []int32{0}, City: []int32{0},
			Cnt: []int32{1}, Kop: []int64{100},
		},
	}
	path := filepath.Join(t.TempDir(), "esc.html")
	if _, err := exportHTML(cube, path); err != nil {
		t.Fatalf("exportHTML: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(b), "</script><b>evil") {
		t.Error("неэкранированный </script> попал в payload")
	}
	// json.Marshal по умолчанию HTML-экранирует (< → \u003c); слэш-экранирование
	// в exportHTML — страховка. Хоть что-то из этого должно присутствовать.
	if !strings.Contains(string(b), `\u003c/script`) && !strings.Contains(string(b), `<\/script>`) {
		t.Error("payload не экранирован")
	}
}
