// report_test.go — регрессии рендера xlsx (без БД; временный файл в t.TempDir).
package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// TestEscalationSheetColumnAlignment — заголовок и значение каждой колонки листа
// «7+ дней» обязаны стоять в одной колонке. Регрессия аудита 09.11 (P1): шапка
// писалась по исходному индексу reportColumns (дырка на пропущенной «Фото»),
// данные — по плотному счётчику, значения уезжали на колонку влево.
func TestEscalationSheetColumnAlignment(t *testing.T) {
	cfg := defaultConfig()
	const today = "2026-11-09"
	rows := mockRows(cfg, today)
	sortRows(rows)

	path := filepath.Join(t.TempDir(), "escalation.xlsx")
	if err := exportXLSX(rows, path, cfg, nil, false, today, "testrev"); err != nil {
		t.Fatalf("exportXLSX: %v", err)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	const sheet = "7+ дней"
	// Ожидаемые заголовки — плотный список без фото-колонок.
	want := make([]string, 0, len(reportColumns))
	for _, c := range reportColumns {
		if c.header == "Фото" || c.header == "Ссылка на фото" {
			continue
		}
		want = append(want, c.header)
	}
	esc := make([]Row, 0)
	for _, r := range rows {
		if r.FullDays >= 7 {
			esc = append(esc, r)
		}
	}
	if len(esc) == 0 {
		t.Fatal("моки должны давать строки 7+ дней")
	}

	for j, h := range want {
		col := j + 1
		gotHeader, _ := f.GetCellValue(sheet, cellName(col, 4))
		if gotHeader != h {
			t.Errorf("col %d: заголовок = %q, want %q", col, gotHeader, h)
			continue
		}
		// Значение первой строки листа — из value() колонки с этим заголовком
		// (№ — порядковый номер, для первой строки 1).
		var exp any
		for _, c := range reportColumns {
			if c.header == h {
				exp = c.value(&esc[0])
				if h == "№" {
					exp = 1
				}
			}
		}
		gotVal, _ := f.GetCellValue(sheet, cellName(col, 5))
		if gotVal != anyToString(exp) {
			t.Errorf("col %d (%s): значение = %q, want %q", col, h, gotVal, anyToString(exp))
		}
	}
}

// TestGlossaryCoversAllColumns — семантическое ядро: каждая колонка отчёта
// обязана иметь глоссарий (смысл + сырьё). Новая колонка без описания роняет
// тест раньше, чем уедет в рассылку.
func TestGlossaryCoversAllColumns(t *testing.T) {
	if len(glossary) == 0 {
		t.Fatal("глоссарий пуст")
	}
	for _, c := range reportColumns {
		g, ok := glossary[c.header]
		if !ok {
			t.Errorf("колонка %q: нет записи в glossary.go", c.header)
			continue
		}
		if g.meaning == "" || g.source == "" {
			t.Errorf("колонка %q: смысл/сырьё пустые", c.header)
		}
	}
	// Обратное направление: глоссарий без мёртвых записей.
	for h := range glossary {
		found := false
		for _, c := range reportColumns {
			if c.header == h {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("глоссарий %q: колонки в отчёте нет (мёртвая запись)", h)
		}
	}
}

func cellName(col, row int) string {
	name, _ := excelize.CoordinatesToCellName(col, row)
	return name
}

// anyToString — как excelize хранит значение ячейки (nil → пусто).
func anyToString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

// TestAvailabilityRu — русские ярлыки «Доступности» (классификация WB,
// сваггер 11-analytics.yaml availabilityFilters); неизвестный код не маскируется.
func TestAvailabilityRu(t *testing.T) {
	cases := map[string]string{
		"nonLiquid":    "залежался (неликвид)",
		"balanced":     "баланс (норма)",
		"deficient":    "дефицит (мало товара)",
		"nonActual":    "неактуальный (не сезон)",
		"actual":       "актуальный (продаётся)",
		"invalidData":  "не рассчитано",
		"":             "",
		"somethingNew": "somethingNew",
	}
	for in, want := range cases {
		if got := availabilityRu(in); got != want {
			t.Errorf("availabilityRu(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestAvailabilitySheet — лист «Оценка WB» существует и несёт официальные
// названия категорий WB дословно (не наши пересказы).
func TestAvailabilitySheet(t *testing.T) {
	cfg := defaultConfig()
	rows := mockRows(cfg, "2026-11-09")
	path := filepath.Join(t.TempDir(), "avail.xlsx")
	if err := exportXLSX(rows, path, cfg, nil, false, "2026-11-09", "testrev"); err != nil {
		t.Fatalf("exportXLSX: %v", err)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	const sheet = "Оценка WB"
	if _, err := f.GetSheetIndex(sheet); err != nil {
		t.Fatalf("листа «%s» нет в файле", sheet)
	}
	// Официальные ярлыки WB — по одному на строку данных.
	want := map[string]bool{"Неликвид": false, "Баланс": false, "Дефицит": false,
		"Актуальный": false, "Неактуальный": false, "Не рассчитано": false}
	for row := 7; row < 7+len(availabilityCategories); row++ {
		v, _ := f.GetCellValue(sheet, cellName(2, row))
		if _, ok := want[v]; ok {
			want[v] = true
		}
	}
	for label, seen := range want {
		if !seen {
			t.Errorf("официальный ярлык WB %q отсутствует на листе «Оценка WB»", label)
		}
	}
	// Политика подачи: заголовок листа обязан называть источник — WB, не агент.
	a2, _ := f.GetCellValue(sheet, "A2")
	if !strings.Contains(a2, "Wildberries") {
		t.Errorf("A2 должен указывать источник (Wildberries): %q", a2)
	}
}

// TestGlossaryRelatedTerms — блок «Смежные термины» присутствует и несёт
// атрибуцию «чей термин» (политика подачи: гендир видит происхождение).
func TestGlossaryRelatedTerms(t *testing.T) {
	cfg := defaultConfig()
	rows := mockRows(cfg, "2026-11-10")
	path := filepath.Join(t.TempDir(), "terms.xlsx")
	if err := exportXLSX(rows, path, cfg, nil, false, "2026-11-10", "testrev"); err != nil {
		t.Fatalf("exportXLSX: %v", err)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	const sheet = "Глоссарий"
	found := false
	for row := 1; row < 200; row++ {
		a, _ := f.GetCellValue(sheet, cellName(1, row))
		if strings.Contains(a, "Slow movers") {
			b, _ := f.GetCellValue(sheet, cellName(2, row))
			if !strings.Contains(b, "не WB и не наш") {
				t.Errorf("атрибуция термина Slow movers потерялась: %q", b)
			}
			found = true
		}
	}
	if !found {
		t.Error("термин Slow movers отсутствует в блоке смежных терминов «Глоссария»")
	}
}
