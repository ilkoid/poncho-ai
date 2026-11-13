// detect_test.go — тесты чистых функций детекции и дерева гипотез (без БД).
package main

import "testing"

func TestParseDate(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"2026-10-28", "2026-10-28", true},
		{"2026-10-28T18:08:11+03:00", "2026-10-28", true},
		{"2026-10-28 18:08:11", "2026-10-28", true},
		{"0001-01-01T00:00:00", "", false}, // фантом 1С
		{"1999-12-31", "", false},          // до 2000 — мусор
		{"", "", false},                    // пусто
		{"2026-1-1", "", false},            // не канонический формат
	}
	for _, c := range cases {
		got, ok := parseDate(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parseDate(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestSalesStartCascade(t *testing.T) {
	// Из двух дат остатка берём САМУЮ РАННЮЮ (кейс 09.11: FBO позже портала —
	// приоритет источника не должен отодвигать начало продаж).
	got, src := SalesStartCascade("2026-10-20", "2026-10-15", "2026-10-10", "2026-09-01")
	if got != "2026-10-15" || src != "stock_products" {
		t.Errorf("портал раньше stocks — берём раннюю: %q/%q", got, src)
	}
	got, src = SalesStartCascade("2026-10-12", "2026-10-15", "2026-10-10", "2026-09-01")
	if got != "2026-10-12" || src != "stocks" {
		t.Errorf("stocks раньше портала — берём раннюю: %q/%q", got, src)
	}
	// Единственный источник остатка.
	got, src = SalesStartCascade("", "2026-10-15", "2026-10-10", "2026-09-01")
	if got != "2026-10-15" || src != "stock_products" {
		t.Errorf("portal один: %q/%q", got, src)
	}
	got, src = SalesStartCascade("2026-10-15", "", "2026-10-10", "2026-09-01")
	if got != "2026-10-15" || src != "stocks" {
		t.Errorf("stocks один: %q/%q", got, src)
	}
	// Остатков нет → деградация к первому показу, затем созданию.
	got, src = SalesStartCascade("0001-01-01", "", "2026-10-10", "2026-09-01")
	if got != "2026-10-10" || src != "funnel_open" {
		t.Errorf("фантом stocks должен быть пропущен: %q/%q", got, src)
	}
	// Показ раньше остатков НЕ перетягивает якорь (открытие ≠ доступность).
	got, src = SalesStartCascade("2026-10-20", "2026-10-15", "2026-10-01", "2026-09-01")
	if got != "2026-10-15" || src != "stock_products" {
		t.Errorf("funnel раньше остатков не участвует в выборе: %q/%q", got, src)
	}
	got, src = SalesStartCascade("", "", "", "2026-09-07T14:26:05Z")
	if got != "2026-09-07" || src != "cards_created" {
		t.Errorf("created_at не последний: %q/%q", got, src)
	}
	if got, _ := SalesStartCascade("", "", "", ""); got != "" {
		t.Errorf("пустой каскад должен давать пустой якорь, got %q", got)
	}
}

func TestFullDaysNoOrders(t *testing.T) {
	const today = "2026-11-09"
	cases := []struct {
		name       string
		lastOrder  string
		salesStart string
		want       int
	}{
		{"заказ сегодня", today, "2026-10-28", 0},
		{"заказ вчера", "2026-11-08", "2026-10-28", 0},
		{"заказ позавчера", "2026-11-07", "2026-10-28", 1},
		{"заказ 4 дня назад", "2026-11-05", "2026-10-28", 3},
		{"нет заказов, старт вчера", "", "2026-11-08", 0},
		{"нет заказов, старт позавчера", "", "2026-11-07", 1},
		{"нет заказов, старт 4 дня назад", "", "2026-11-05", 3},
		{"нет заказов и старта", "", "", 0},
		{"заказ позже старта — якорь заказ", "2026-11-01", "2026-10-01", 7},
	}
	for _, c := range cases {
		if got := FullDaysNoOrders(today, c.lastOrder, c.salesStart); got != c.want {
			t.Errorf("%s: FullDaysNoOrders(%q,%q) = %d, want %d", c.name, c.lastOrder, c.salesStart, got, c.want)
		}
	}
}

func testWatch() WatchConfig {
	return WatchConfig{
		NoOrdersDays:       3,
		SlowStartMaxOrders: 2,
		StallDays:          3,
		IncludePreSales:    false,
	}
}

func TestClassifySignal(t *testing.T) {
	w := testWatch()

	// Новая без заказов: старт 5 дней назад → 3 полных дня → сигнал.
	if got := ClassifySignal("2026-11-04", 3, 50, 0, &w); got != SignalSlowStart {
		t.Errorf("newcard_zero: got %q", got)
	}
	// Меньше порога — не флагуем.
	if got := ClassifySignal("2026-11-05", 2, 50, 0, &w); got != "" {
		t.Errorf("ниже порога: got %q", got)
	}
	// Стоп: 12 заказов за жизнь (разгон была), тишина 5 дней → stall.
	if got := ClassifySignal("2026-10-01", 5, 50, 12, &w); got != SignalStall {
		t.Errorf("stall: got %q", got)
	}
	// Товара нет в наличии — отсутствие заказов не контент-проблема.
	if got := ClassifySignal("2026-11-01", 5, 0, 0, &w); got != "" {
		t.Errorf("stock=0: got %q", got)
	}
	// 1-2 заказа за жизнь — тоже slow_start («продалось чуть-чуть и умерло»).
	if got := ClassifySignal("2026-11-01", 5, 50, 2, &w); got != SignalSlowStart {
		t.Errorf("лёгкий старт: got %q", got)
	}
	// Свежесть коллекции ограничивается allowed_years на уровне популяции (SQL),
	// не здесь: старый старт с живым остатком флагуется как есть.
	if got := ClassifySignal("2026-05-01", 100, 50, 0, &w); got != SignalSlowStart {
		t.Errorf("старый старт без возрастного фильтра: got %q, want newcard_zero", got)
	}
	// Якоря нет: pre_sales только при IncludePreSales.
	if got := ClassifySignal("", 0, 0, 0, &w); got != "" {
		t.Errorf("pre_sales выключен: got %q", got)
	}
	wPre := w
	wPre.IncludePreSales = true
	if got := ClassifySignal("", 0, 0, 0, &wPre); got != SignalPreSales {
		t.Errorf("pre_sales включён: got %q", got)
	}
}

func TestAllowedYearsNormalization(t *testing.T) {
	cfg := defaultConfig()
	cfg.Watch.AllowedYears = []int{2026, 25, 2027}
	cfg.applyDefaults()
	got := cfg.Watch.AllowedYears
	want := []int{26, 25, 27}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("нормализация годов: got %v, want %v", got, want)
		}
	}
}

func TestTitleChecks(t *testing.T) {
	th := ThresholdsConfig{TitleLenCritical: 20, TitleLenWarning: 25}

	// Кейс гендира: «Ботинки» == предмет, однословное, 7 символов.
	if !TitleBad("Ботинки", "Ботинки", &th) {
		t.Error("«Ботинки» должен быть критичным")
	}
	if !TitleBad("Кроссовки", "Кроссовки", &th) {
		t.Error("совпадение с предметом — критично независимо от длины")
	}
	if !TitleBad("Сапоги зимние", "Сапоги", &th) {
		t.Error("двухсловное короче 20 — критично")
	}
	if TitleBad("Ботинки зимние для мальчика Memo", "Ботинки", &th) {
		t.Error("нормальное наименование не должно быть критичным")
	}
	if !TitleShort("Ботинки зимние kid", &th) {
		t.Error("20-24 символа — short")
	}
	if TitleShort("Ботинки зимние для мальчика", &th) {
		t.Error(">=25 символов — не short")
	}
	if got := titleWordCount("  Ботинки  зимние "); got != 2 {
		t.Errorf("titleWordCount = %d, want 2", got)
	}
	if !titleEqSubject("Ботинки ", "Ботинки") {
		t.Error("eqSubject должен триммить пробелы")
	}
}

func TestTopHypothesis(t *testing.T) {
	yes, no := true, false
	low, ok := 0.3, 0.9
	cases := []struct {
		name string
		h    Hypothesis
		want string
	}{
		{"pre_sales", Hypothesis{Signal: SignalPreSales}, HypoPresales},
		{"скрыта", Hypothesis{Signal: SignalSlowStart, IsShadowed: &yes}, HypoShadowed},
		{"нет видимости", Hypothesis{Signal: SignalSlowStart, Opens7d: 0, Campaigns: 0}, HypoNoVisibility},
		{"кампания без показов", Hypothesis{Signal: SignalStall, Opens7d: 0, Campaigns: 2}, HypoPromoBroken},
		{"наименование", Hypothesis{Signal: SignalSlowStart, Opens7d: 98, Carts7d: 0, TitleBad: true}, HypoNaming},
		{"контент", Hypothesis{Signal: SignalSlowStart, Opens7d: 50, Carts7d: 0, TitleBad: false}, HypoContent},
		{"размерный ряд", Hypothesis{Signal: SignalStall, Opens7d: 30, Carts7d: 4, SizesRatioLow: true}, HypoSizes},
		{"конверсия", Hypothesis{Signal: SignalStall, Opens7d: 30, Carts7d: 4}, HypoConversion},
	}
	for _, c := range cases {
		if code, _ := TopHypothesis(c.h); code != c.want {
			t.Errorf("%s: got %q, want %q", c.name, code, c.want)
		}
	}
	// Скрытость приоритетнее видимости; nodata по тени не мешает остальным веткам.
	if code, _ := TopHypothesis(Hypothesis{Signal: SignalSlowStart, IsShadowed: &no, Opens7d: 0}); code != HypoNoVisibility {
		t.Errorf("is_shadowed=false не должен блокировать видимость: %q", code)
	}
	_ = low
	_ = ok
}

func TestEnrichRowGeneralDirectorCase(t *testing.T) {
	// Контрольный кейс гендира: карточка 1536932431 «Ботинки», показы с 28.10
	// (98 открытий), 0 корзин, 61 шт только в портале, 6 размеров на Внуково.
	cfg := defaultConfig()
	const today = "2026-11-09"
	start := "2026-10-28"
	raw := rawRow{
		NmID: 1536932431, Article: "32616702", Title: "Ботинки", Subject: "Ботинки",
		Category1C: "Обувь", Season1C: "Осень-Зима", NameIM: "Ботинки зимние детские",
		CardCreated: "2026-09-07T14:26:05Z",
		FirstPortal: &start, Opens7d: 98, Carts7d: 0, PhotoCount: 8, CharCount: 10,
		PortalQty: int64Ptr(61), PortalAvail: "nonLiquid",
		Sizes1C: intPtr(6), FboStorage: "Внуково", Campaigns: 0,
	}
	r := EnrichRow(raw, cfg, today)
	if r == nil {
		t.Fatal("кейс гендира обязан попадать в вотчлист")
	}
	if r.Signal != SignalSlowStart {
		t.Errorf("signal = %q, want slow_start", r.Signal)
	}
	if r.SalesStart != "2026-10-28" || r.AnchorSource != "stock_products" {
		t.Errorf("якорь = %q/%q, want 2026-10-28/stock_products", r.SalesStart, r.AnchorSource)
	}
	if r.WBStockTotal == nil || *r.WBStockTotal != 61 {
		t.Errorf("остаток должен прийти из портала (61), got %v", r.WBStockTotal)
	}
	if r.SizesWB != nil {
		t.Errorf("размерных данных WB нет — sizes_wb должен быть NULL, got %d", *r.SizesWB)
	}
	if r.SizesRatio != nil {
		t.Error("ratio не считается без размеров WB")
	}
	if r.HypoCode != HypoNaming {
		t.Errorf("гипотеза = %q (%s), want naming — цена не первая!", r.HypoCode, r.TopHypothesis)
	}
}

func TestEnrichRowStockCascade(t *testing.T) {
	cfg := defaultConfig()
	const today = "2026-11-09"
	start := "2026-11-05" // стартовый день 05 → тишина 06,07,08 = ровно 3 полных дня
	// 12 заказов за жизнь (разгон была) → stall, не slow_start.
	raw := rawRow{
		NmID: 1, Article: "32600001", Title: "Кроссовки детские PLAYToday", Subject: "Кроссовки",
		FirstStock: &start, StockQty: int64Ptr(120), StockSizes: intPtr(3), OrdersTotal: 12,
		Sizes1C: intPtr(6), FboStorage: "РВБ ФБО",
	}
	r := EnrichRow(raw, cfg, today)
	if r == nil {
		t.Fatal("3 полных дня без заказов при остатке — должен флаговаться")
	}
	if r.Signal != SignalStall {
		t.Errorf("signal = %q, want stall (12 заказов за жизнь)", r.Signal)
	}
	if r.AnchorSource != "stocks" || r.SalesStart != "2026-11-05" {
		t.Errorf("якорь = %q/%q, want stocks/2026-11-05", r.AnchorSource, r.SalesStart)
	}
	if r.FullDays != 3 {
		t.Errorf("fullDays = %d, want 3", r.FullDays)
	}
	if r.SizesRatio == nil || *r.SizesRatio < 0.49 || *r.SizesRatio > 0.51 {
		t.Errorf("ratio = %v, want 0.5", r.SizesRatio)
	}
	if r.HypoCode != HypoNoVisibility {
		t.Errorf("opens=0 и кампаний нет → no_visibility, got %q", r.HypoCode)
	}
}

func TestEnrichRowFBSPortalStock(t *testing.T) {
	// FBS-нюанс каскада: строки stocks есть, но FBO=0, а в портале остаток живой —
	// товар доступен к покупке, остаток берём из портала (не теряем карточку).
	cfg := defaultConfig()
	const today = "2026-11-09"
	start := "2026-11-02"
	raw := rawRow{
		NmID: 2, Article: "32600002", Title: "Сандалии детские", Subject: "Сандалии",
		FirstStock: &start, StockQty: int64Ptr(0), StockSizes: intPtr(0),
		PortalQty: int64Ptr(45), OrdersTotal: 1,
	}
	r := EnrichRow(raw, cfg, today)
	if r == nil {
		t.Fatal("портальный остаток 45 при нулевом FBO — карточка доступна, должна флаговаться")
	}
	if r.WBStockTotal == nil || *r.WBStockTotal != 45 {
		t.Errorf("остаток = %v, want 45 (из портала)", r.WBStockTotal)
	}
	if r.Signal != SignalSlowStart {
		t.Errorf("signal = %q, want slow_start (1 заказ за жизнь)", r.Signal)
	}
}
