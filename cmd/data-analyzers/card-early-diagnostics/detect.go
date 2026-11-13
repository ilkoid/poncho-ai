// detect.go — чистые функции детекции: даты 1С, якорь начала продаж, полные дни
// без заказов, классификация сигнала. Без БД — покрыты unit-тестами.
package main

import "time"

// dateLayout — канонический формат дат в срезе ('YYYY-MM-DD').
const dateLayout = "2006-01-02"

// parseDate — best-effort разбор даты из TEXT-полей WB/1С ('2026-10-28',
// '2026-10-28T18:08:11+03:00', '2026-10-28 18:08:11'). Возвращает канон 'YYYY-MM-DD'.
// Даты с годом < 2000 бракуются: 1С пишет фантом «0001-01-01» товарам без даты
// (тот же фильтр, что parseCertDate в collection-readiness).
func parseDate(s string) (string, bool) {
	if len(s) < 10 {
		return "", false
	}
	t, err := time.Parse(dateLayout, s[:10])
	if err != nil || t.Year() < 2000 {
		return "", false
	}
	return t.Format(dateLayout), true
}

// daysBetween — разница дней to−from ('YYYY-MM-DD'); ok=false, если что-то не парсится.
func daysBetween(from, to string) (int, bool) {
	fs, ok1 := parseDate(from)
	ts, ok2 := parseDate(to)
	if !ok1 || !ok2 {
		return 0, false
	}
	ft, err1 := time.Parse(dateLayout, fs)
	tt, err2 := time.Parse(dateLayout, ts)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return int(tt.Sub(ft).Hours() / 24), true
}

// SalesStartCascade — якорь «фактического начала продаж» (первый день, когда товар
// можно было купить) с фиксацией источника:
//
//  1. самая РАННЯЯ из дат остатка>0: stocks_daily_warehouses (FBO) или stock_products
//     (портал; карточки-«невидимки» — кейс 1536932431). Приоритет источника не должен
//     отодвигать начало продаж: кейс 09.11 — FBO-остаток появился 11-08 при живом
//     портале с 10-31, и карточка выпадала из мониторинга на неделю;
//  2. funnel_metrics_daily — первый день открытой карточки (когда остатков
//     в окне истории уже нет);
//  3. cards.created_at — дата создания карточки (самый грубый якорь: карточка
//     могла существовать до завоза товара).
//
// Первый показ и создание — только fallback: карточки вне наличия WB всё равно
// показывает в поиске, открытие ≠ доступность.
//
// Для карточек старше 27-дневного окна остатков якорь деградирует вниз по каскаду —
// источник честно пишется в anchor_source.
func SalesStartCascade(firstStock, firstPortal, firstOpen, cardCreatedAt string) (string, string) {
	s, sOK := parseDate(firstStock)
	p, pOK := parseDate(firstPortal)
	switch {
	case sOK && pOK:
		if s <= p {
			return s, "stocks"
		}
		return p, "stock_products"
	case sOK:
		return s, "stocks"
	case pOK:
		return p, "stock_products"
	}
	if d, ok := parseDate(firstOpen); ok {
		return d, "funnel_open"
	}
	if d, ok := parseDate(cardCreatedAt); ok {
		return d, "cards_created"
	}
	return "", ""
}

// maxDate — поздняя из двух дат ('YYYY-MM-DD'); пустая/битая игнорируется.
func maxDate(a, b string) string {
	da, oka := parseDate(a)
	db, okb := parseDate(b)
	switch {
	case oka && okb:
		if da >= db {
			return da
		}
		return db
	case oka:
		return da
	case okb:
		return db
	default:
		return ""
	}
}

// FullDaysNoOrders — число ПОЛНЫХ дней без заказов (сегодня не считается: оно
// ещё не закончилось). Якорь отсчёта — поздняя из дат последнего заказа и начала
// продаж; сам день якоря — торговый день, из счёта исключён:
//
//	заказ сегодня        → 0 (сегодня не считаем, минус уходит в clamp)
//	заказ вчера          → 0 полных дней тишины
//	заказ позавчера      → 1
//	нет заказов, старт
//	вчера                → 0 (первый торговый день только прошёл)
//	нет заказов, старт
//	позавчера            → 1
//
// Формула: max(0, days(today − GREATEST(last_order, sales_start)) − 1).
func FullDaysNoOrders(today, lastOrder, salesStart string) int {
	anchor := maxDate(lastOrder, salesStart)
	if anchor == "" {
		return 0
	}
	d, ok := daysBetween(anchor, today)
	if !ok {
		return 0
	}
	if d <= 1 {
		return 0
	}
	return d - 1
}

// Сигналы детекции (колонка signal). Разрез — что было ДО тишины: карточка
// «не разогналась с начала продаж» (slow_start) или «разгонялась и остановилась» (stall).
const (
	// SignalSlowStart — выведено, но не разогналась: заказов за жизнь ≤ SlowStartMaxOrders
	// (включая ноль) и тишина ≥ NoOrdersDays полных дней. Сигнал ранней диагностики
	// гендира — «выведено только что и не продаётся».
	SignalSlowStart = "slow_start"
	// SignalStall — разгонялась (заказов за жизнь больше порога) и замолчала
	// на ≥ StallDays полных дней. Длинный хвост одежды продаётся не каждый день,
	// поэтому stall — категория наблюдения, а не безусловная аномалия.
	SignalStall    = "stall"
	SignalPreSales = "pre_sales" // одобрено в 1С, карточка есть, но продажы не начались
)

// ClassifySignal — классификация карточки: slow_start / stall / pre_sales.
//
// wbStock>0 — гард наличия: отсутствие товара объясняет отсутствие заказов,
// это не контент-проблема. Свежесть ассортимента ограничивается не здесь, а
// фильтром года коллекции в популяции (watch.allowed_years, SQL-side).
// Возвращает "" — карточка критериям не соответствует (в срез не попадает).
func ClassifySignal(salesStart string, fullDays int, wbStock int64, ordersTotal int64, cfg *WatchConfig) string {
	if salesStart == "" {
		// Якоря нет — товара никогда не было в наличии.
		if cfg.IncludePreSales {
			return SignalPreSales
		}
		return ""
	}
	if wbStock <= 0 {
		// Сейчас товара нет: продажа «не началась/остановилась» по причине наличия.
		return ""
	}
	if ordersTotal <= int64(cfg.SlowStartMaxOrders) {
		if fullDays >= cfg.NoOrdersDays {
			return SignalSlowStart
		}
		return ""
	}
	if fullDays >= cfg.StallDays {
		return SignalStall
	}
	return ""
}

// titleWordCount — число слов в наименовании (разделитель — пробелы).
func titleWordCount(s string) int {
	n := 0
	inWord := false
	for _, r := range s {
		if r == ' ' || r == '\t' {
			inWord = false
		} else if !inWord {
			inWord = true
			n++
		}
	}
	return n
}

// titleEqSubject — наименование совпадает с предметом («Ботинки» == subject «Ботинки»):
// карточка не говорит о себе ничего, кроме категории.
func titleEqSubject(title, subject string) bool {
	return trimSpaces(title) != "" && trimSpaces(title) == trimSpaces(subject)
}

// TitleBad — триггер «совсем короткого наименования» (критический ярус):
// короче порога ИЛИ однословное ИЛИ совпадает с предметом.
func TitleBad(title, subject string, cfg *ThresholdsConfig) bool {
	t := trimSpaces(title)
	if t == "" {
		return true
	}
	return len([]rune(t)) < cfg.TitleLenCritical ||
		titleWordCount(t) == 1 ||
		titleEqSubject(t, subject)
}

// TitleShort — ярус «коротковатое» (некритичное, но подозрительное).
func TitleShort(title string, cfg *ThresholdsConfig) bool {
	t := trimSpaces(title)
	return t != "" && len([]rune(t)) < cfg.TitleLenWarning
}

// trimSpaces — обрезка пробелов по рунам (кириллица не режется).
func trimSpaces(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
