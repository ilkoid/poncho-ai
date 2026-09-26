// chars_test.go — тесты чистых функций колонок «Характеристики WB» и «Сертификат/декларация»
// (парсинг json_value, рендер строк/размеров, сертификатная ячейка, просрочка).
package main

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseJSONValues(t *testing.T) {
	cases := []struct {
		name, in string
		want     []string
		wantErr  bool
	}{
		{"строки", `["хлопок 95%","эластан 5%"]`, []string{"хлопок 95%", "эластан 5%"}, false},
		{"числа", `[150]`, []string{"150"}, false},
		{"смешанный", `["тёмно-синий","красный","белый"]`, []string{"тёмно-синий", "красный", "белый"}, false},
		{"пустые значения отфильтрованы", `["","80х150 см",""]`, []string{"80х150 см"}, false},
		{"пустой массив", `[]`, []string{}, false},
		{"повреждённый JSON", `не json`, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseJSONValues(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("ожидали ошибку, получили %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ошибка: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("получили %v, ожидали %v", got, c.want)
			}
		})
	}
}

func TestRenderCardChars(t *testing.T) {
	chars := []rawChar{
		{charID: 12, name: "Рисунок", values: []string{"полоска"}},
		{charID: 14177449, name: "Цвет", values: []string{"тёмно-синий", "красный", "белый"}},
		{charID: 15001136, name: "Номер сертификата соответствия", values: []string{"ЕАЭС RU С-CN.НВ18.В.02416/23"}},
		{charID: 15001137, name: "Дата регистрации сертификата/декларации", values: []string{"2023-05-31T00:00:00"}},
		{charID: 15001138, name: "Дата окончания действия сертификата/декларации", values: []string{"2026-05-30T00:00:00"}},
		{charID: 15000001, name: "ТНВЭД", values: []string{"6110201000"}},
		{charID: 999, name: "", values: []string{"x"}}, // имя пустое → char_<id>
		{charID: 998, name: "Пустая", values: nil},     // нет значений → строка пропускается
	}
	cc := renderCardChars(chars, []string{"146", "62", "128", "74"})

	// Строки отсортированы лексикографически по код-поинтам: латиница < кириллицы
	// (char_999 первым), далее Р < Ц. ВЭД-поля (сертификат, даты, ТНВЭД) в Lines НЕ попадают.
	wantLines := []string{
		"char_999: x",
		"Рисунок: полоска",
		"Цвет: тёмно-синий, красный, белый",
	}
	if !reflect.DeepEqual(cc.Lines, wantLines) {
		t.Fatalf("Lines: получили %v, ожидали %v", cc.Lines, wantLines)
	}
	if cc.CertNum != "ЕАЭС RU С-CN.НВ18.В.02416/23" || cc.DeclNum != "" {
		t.Fatalf("слоты номеров: ожидали cert=№02416/23, decl пустой; получили cert=%q decl=%q", cc.CertNum, cc.DeclNum)
	}
	if cc.Tnved != "6110201000" {
		t.Fatalf("Tnved: получили %q, ожидали 6110201000", cc.Tnved)
	}
	if want := []string{"62", "74", "128", "146"}; !reflect.DeepEqual(cc.Sizes, want) {
		t.Fatalf("Sizes: получили %v, ожидали %v (числовая сортировка)", cc.Sizes, want)
	}
}

func TestSortSizesNonNumericFallback(t *testing.T) {
	got := sortSizes([]string{"one_size", "128", "62"})
	// Есть нечисловой размер → лексикографическая сортировка.
	want := []string{"128", "62", "one_size"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("получили %v, ожидали %v", got, want)
	}
}

func TestCharsCell(t *testing.T) {
	r := Row{
		CharLines: []string{"Состав: хлопок 95%, эластан 5%", "Цвет: белый"},
		WBSizes:   "62, 68, 74",
	}
	got := charsCell(r)
	want := "Состав: хлопок 95%, эластан 5%\nЦвет: белый\nРазмеры (WB): 62, 68, 74"
	if got != want {
		t.Fatalf("получили %q, ожидали %q", got, want)
	}
	if strings.HasSuffix(got, "\n") || strings.HasPrefix(got, "\n") {
		t.Fatal("лишний \\n на границе ячейки")
	}

	// Только размеры, без характеристик.
	if got, want := charsCell(Row{WBSizes: "0"}), "Размеры (WB): 0"; got != want {
		t.Fatalf("только размеры: получили %q, ожидали %q", got, want)
	}
	// Пустая ячейка.
	if got := charsCell(Row{}); got != "" {
		t.Fatalf("пустая: получили %q, ожидали \"\"", got)
	}
}

func TestRuCertType(t *testing.T) {
	cases := map[string]string{
		"Certificate":  "Сертификат",
		"certificate":  "Сертификат",
		" Declaration": "Декларация",
		"":             "",
		"Прочее":       "Прочее",
	}
	for in, want := range cases {
		if got := ruCertType(in); got != want {
			t.Fatalf("ruCertType(%q): получили %q, ожидали %q", in, got, want)
		}
	}
}

func TestParseCertDate(t *testing.T) {
	cases := map[string]string{ // вход → ожидаемая дата (для проверки формата)
		"2027-03-15T00:00:00": "15.03.2027",
		"2027-03-15":          "15.03.2027",
		"15.03.2027":          "15.03.2027",
	}
	for in, want := range cases {
		got, ok := parseCertDate(in)
		if !ok {
			t.Fatalf("parseCertDate(%q): не разобралась", in)
		}
		if s := got.Format("02.01.2006"); s != want {
			t.Fatalf("parseCertDate(%q): получили %s, ожидали %s", in, s, want)
		}
	}
	if _, ok := parseCertDate(""); ok {
		t.Fatal("пустая дата не должна парситься")
	}
	if _, ok := parseCertDate("мусор"); ok {
		t.Fatal("мусор не должен парситься")
	}
	// Фантомная дата 1С у товаров без документа — не парсится (иначе раздувает метрику просрочки).
	if _, ok := parseCertDate("0001-01-01T00:00:00"); ok {
		t.Fatal("фантомная дата 0001-01-01 не должна парситься")
	}
}

func TestCertExpiryDayBoundary(t *testing.T) {
	now := time.Date(2026, 9, 24, 15, 30, 0, 0, time.UTC)
	nm := int64(7)
	base := Row{NmID: &nm, WBCertNum: "ЕАЭС RU С-CN.НВ18.В.02416/23", HasOneCCert: true,
		CertType: "Сертификат", CertNumber: "ЕАЭС RU С-CN.НВ18.В.02416/23"}

	// Срок «до сегодня» — валиден весь последний день: не просрочен, не подсвечивается.
	today := base
	today.CertEnd = now.Format("2006-01-02")
	if got := today.certCell(now); strings.Contains(got, "просрочен") {
		t.Fatalf("срок сегодня не просрочен: %q", got)
	}
	if today.CertProblem(now) {
		t.Fatal("CertProblem: срок сегодня не должен подсвечиваться")
	}

	// Срок «до вчера» — просрочен и подсвечивается.
	yesterday := base
	yesterday.CertEnd = now.AddDate(0, 0, -1).Format("2006-01-02")
	if got := yesterday.certCell(now); !strings.Contains(got, "просрочен") {
		t.Fatalf("срок вчера должен быть просрочен: %q", got)
	}
	if !yesterday.CertProblem(now) {
		t.Fatal("CertProblem: срок вчера должен подсвечиваться")
	}
}

func TestRenderCardCharsEmptyCertValue(t *testing.T) {
	// Строка cert-характеристики с пустым значением: номер НЕ считается перенесённым.
	cc := renderCardChars([]rawChar{{charID: charCertNumberID, name: "Номер сертификата соответствия", values: nil}}, nil)
	if cc.CertNum != "" || cc.DeclNum != "" {
		t.Fatalf("пустое значение не должно заполнять слоты: cert=%q decl=%q", cc.CertNum, cc.DeclNum)
	}
	if len(cc.Lines) != 0 {
		t.Fatalf("пустая характеристика не должна попадать в Lines: %v", cc.Lines)
	}
}

func TestRenderCardCharsDeclSlot(t *testing.T) {
	// Мультизначный json_value: слот берёт первый элемент.
	cc := renderCardChars([]rawChar{
		{charID: charDeclNumberID, name: "Номер декларации соответствия", values: []string{"ЕАЭС N RU Д-CN.РА01.В.11349/23", "лишнее"}},
	}, nil)
	if cc.DeclNum != "ЕАЭС N RU Д-CN.РА01.В.11349/23" {
		t.Fatalf("DeclNum: получили %q", cc.DeclNum)
	}
	if cc.CertNum != "" {
		t.Fatalf("CertNum должен быть пуст: %q", cc.CertNum)
	}
}

func TestCertWBMarker(t *testing.T) {
	cases := []struct {
		name string
		row  Row
		want string
	}{
		{"совпадает (сертификат)", Row{CertType: "Сертификат", CertNumber: "N1", WBCertNum: "N1"}, "на WB: да"},
		{"совпадает (декларация)", Row{CertType: "Декларация", CertNumber: "N1", WBDeclNum: "N1"}, "на WB: да"},
		{"номера нет", Row{CertType: "Сертификат", CertNumber: "N1"}, "на WB: НЕТ"},
		{"другой номер", Row{CertType: "Сертификат", CertNumber: "N1", WBCertNum: "N2"}, "на WB: другой номер — N2"},
		{"другой номер (декларация)", Row{CertType: "Декларация", CertNumber: "N1", WBDeclNum: "N2"}, "на WB: другой номер — N2"},
		{"не тот тип: ждём декларацию", Row{CertType: "Декларация", CertNumber: "N1", WBCertNum: "N1"}, "на WB: не тот тип — заполнен сертификат"},
		{"не тот тип: ждём сертификат", Row{CertType: "Сертификат", CertNumber: "N1", WBDeclNum: "N1"}, "на WB: не тот тип — заполнена декларация"},
		{"фолбэк без типа: совпадение", Row{CertNumber: "N1", WBDeclNum: "N1"}, "на WB: да"},
		{"фолбэк без типа: расхождение", Row{CertNumber: "N1", WBDeclNum: "N2"}, "на WB: другой номер — N2"},
		{"триммирование номера", Row{CertType: "Сертификат", CertNumber: " N1 ", WBCertNum: "N1"}, "на WB: да"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.row.certWBMarker(); got != c.want {
				t.Fatalf("получили %q, ожидали %q", got, c.want)
			}
		})
	}
}

func TestCertCellTnvedLine(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	nm := int64(9)

	// Полный набор: документ 1С + маркер + ТНВЭД отдельной строкой.
	r := Row{NmID: &nm, HasOneCCert: true, CertType: "Сертификат",
		CertNumber: "ЕАЭС KG417/042.CN.02.08196", CertEnd: "2029-09-06T00:00:00",
		WBCertNum: "ЕАЭС KG417/042.CN.02.08196", WBTnved: "6109902000"}
	want := "Сертификат №ЕАЭС KG417/042.CN.02.08196, до 06.09.2029\nна WB: да\nТНВЭД: 6109902000"
	if got := r.certCell(now); got != want {
		t.Fatalf("получили %q, ожидали %q", got, want)
	}

	// Только ТНВЭД (карточка живая, документа в 1С нет) — ячейка не пустая.
	r2 := Row{NmID: &nm, WBTnved: "6109902000"}
	if got, want := r2.certCell(now), "ТНВЭД: 6109902000"; got != want {
		t.Fatalf("только ТНВЭД: получили %q, ожидали %q", got, want)
	}

	// Без ТНВЭД ячейка не меняется.
	r3 := r
	r3.WBTnved = ""
	if got := r3.certCell(now); strings.Contains(got, "ТНВЭД") {
		t.Fatalf("без ТНВЭД: не должно быть строки ТНВЭД: %q", got)
	}
}

func TestJoinYearsNormalization(t *testing.T) {
	// 2- и 4-значные годы дают одинаковый вывод; applyDefaults нормализует 4-значные.
	if got, want := joinYears([]int{24, 25, 26}), "2024, 2025, 2026"; got != want {
		t.Fatalf("2-значные: получили %q, ожидали %q", got, want)
	}
	if got, want := joinYears([]int{2024, 2026}), "2024, 2026"; got != want {
		t.Fatalf("4-значные: получили %q, ожидали %q", got, want)
	}
	cfg := &Config{AllowedYears: []int{2024, 25, 2026}}
	cfg.applyDefaults()
	if !reflect.DeepEqual(cfg.AllowedYears, []int{24, 25, 26}) {
		t.Fatalf("applyDefaults: получили %v, ожидали [24 25 26]", cfg.AllowedYears)
	}
}

func TestCertStale(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	nm := int64(2)
	base := Row{NmID: &nm, HasOneCCert: true, CertType: "Сертификат",
		CertNumber: "N1", WBCertNum: "N2", CertEnd: "2028-01-01T00:00:00"}

	if !base.CertStale(now) {
		t.Fatal("валидный 1С + другой номер → янтарная")
	}
	if base.CertProblem(now) {
		t.Fatal("другой номер при валидном 1С — не красный")
	}

	// Просрочка 1С сильнее: красный гасит янтарную.
	expired := base
	expired.CertEnd = "2020-01-01T00:00:00"
	if expired.CertStale(now) {
		t.Fatal("просрочка 1С гасит янтарную")
	}
	if !expired.CertProblem(now) {
		t.Fatal("просрочка → красный")
	}

	// Совпадающий номер — без подсветки.
	match := base
	match.WBCertNum = "N1"
	if match.CertStale(now) {
		t.Fatal("совпадение → без подсветки")
	}

	// «НЕТ» — красный, не янтарный.
	absent := base
	absent.WBCertNum = ""
	if absent.CertStale(now) {
		t.Fatal("НЕТ → не янтарный")
	}
	if !absent.CertProblem(now) {
		t.Fatal("НЕТ → красный")
	}

	// Без документа 1С / без карточки — не подсвечивается.
	if (Row{WBCertNum: "N2"}).CertStale(now) {
		t.Fatal("нет документа 1С → без подсветки")
	}
	noCard := base
	noCard.NmID = nil
	if noCard.CertStale(now) {
		t.Fatal("нет карточки → без подсветки")
	}
}

func TestCertCellAndProblem(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	nm := int64(1)

	// Полный набор: тип + номер + будущий срок + есть на WB.
	r := Row{
		NmID: &nm, WBCertNum: "ЕАЭС RU С-CN.НВ18.В.02416/23", HasOneCCert: true,
		CertType: "Сертификат", CertNumber: "ЕАЭС RU С-CN.НВ18.В.02416/23", CertEnd: "2027-05-30T00:00:00",
	}
	if got, want := r.certCell(now), "Сертификат №ЕАЭС RU С-CN.НВ18.В.02416/23, до 30.05.2027\nна WB: да"; got != want {
		t.Fatalf("полный: получили %q, ожидали %q", got, want)
	}
	if r.CertProblem(now) {
		t.Fatal("живой сертификат не должен подсвечиваться")
	}

	// Рассинхрон: в 1С есть, на карточке WB нет.
	r.WBCertNum = ""
	if got, want := r.certCell(now), "Сертификат №ЕАЭС RU С-CN.НВ18.В.02416/23, до 30.05.2027\nна WB: НЕТ"; got != want {
		t.Fatalf("рассинхрон: получили %q, ожидали %q", got, want)
	}
	if !r.CertProblem(now) {
		t.Fatal("рассинхрон должен подсвечиваться")
	}

	// Просроченный.
	r.CertEnd = "2021-06-30T00:00:00"
	if got := r.certCell(now); !strings.Contains(got, "просрочен") {
		t.Fatalf("просрочка: ожидали «(просрочен)» в %q", got)
	}
	if !r.CertProblem(now) {
		t.Fatal("просрочка должна подсвечиваться")
	}

	// Флаг есть, номера нет.
	r2 := Row{NmID: &nm, HasOneCCert: true}
	if got, want := r2.certCell(now), "да (без номера)\nна WB: НЕТ"; got != want {
		t.Fatalf("без номера: получили %q, ожидали %q", got, want)
	}

	// Нет ничего — пустая ячейка.
	if got := (Row{}).certCell(now); got != "" {
		t.Fatalf("пустая: получили %q", got)
	}
	// В 1С нет документа, но карточка живая — маркер «на WB» не показываем.
	if got := (Row{NmID: &nm, WBCertNum: "N1"}).certCell(now); got != "" {
		t.Fatalf("нет 1С-документа: получили %q, ожидали пусто", got)
	}
}
