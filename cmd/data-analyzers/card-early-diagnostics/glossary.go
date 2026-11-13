// glossary.go — лист «Глоссарий»: каждая колонка отчёта — что показывает,
// из какого сырья (RAW, схема public) и как читать. Ключ = заголовок из
// reportColumns, порядок строк — тот же: глоссарий генерируется из того же
// списка и не может разойтись с колонками (тест TestGlossaryCoversAllColumns).
package main

import (
	"fmt"

	"github.com/xuri/excelize/v2"
)

// glossaryEntry — описание одной колонки.
type glossaryEntry struct {
	meaning string // что показывает
	source  string // сырьё: RAW-таблицы/расчёт
	notes   string // нюансы чтения (пусто = без нюансов)
}

// glossary — единый словарь семантики колонок. Термины якоря/сигнала — те же
// строки, что рендерят AnchorSourceRu/SignalRu (один термин — одно место).
var glossary = map[string]glossaryEntry{
	"Фото": {
		meaning: "миниатюра первого фото карточки",
		source:  "card_photos (tm-URL, CDN WB)",
		notes:   "рендер-колонка, в БД не пишется; --no-photos оставляет пустой",
	},
	"№": {
		meaning: "порядковый номер строки отчёта",
		source:  "—",
		notes:   "служебная",
	},
	"Категория 1С": {
		meaning: "товарная категория 1С (фильтр xlsx)",
		source:  "onec_goods: category_level2_name → level1_name → category (первый непустой)",
	},
	"Предмет WB": {
		meaning: "предмет каталога WB (фильтр xlsx)",
		source:  "cards.subject_name",
	},
	"Артикул": {
		meaning: "артикул продавца; символы 2-3 = год коллекции (фильтр allowed_years)",
		source:  "cards.vendor_code = onec_goods.article (ключ связи WB ↔ 1С)",
	},
	"nmID": {
		meaning: "идентификатор карточки WB",
		source:  "cards.nm_id",
	},
	"Наименование WB": {
		meaning: "заголовок карточки; триггер: <20 симв / однословное / = предмет — красный",
		source:  "cards.title",
		notes:   "эталон для исправления — «Наименование 1С»",
	},
	"Сезон 1С": {
		meaning: "сезон из 1С (фильтр xlsx)",
		source:  "onec_goods.season",
	},
	"Начало продаж": {
		meaning: "первый день фактического начала продаж (первый остаток>0); пусто = не начались",
		source:  "самая ранняя из дат остатка: stocks_daily_warehouses (FBO) / stock_products (портал) → fallback funnel_metrics_daily (первый показ) → cards.created_at",
		notes:   "какая ветка сработала — колонка «Якорь»; чем позже ветка, тем грубее дата",
	},
	"Якорь": {
		meaning: "источник «Начало продаж»: остаток WB / портал (из них — давший раннюю дату) / первый показ / создание карточки",
		source:  "расчёт (каскад якоря, detect.go)",
		notes:   "«остаток WB» и «портал» — даты доступности, берётся ранняя; «первый показ» и «создание карточки» — грубые fallback (открытие ≠ доступность: карточки вне наличия WB показывает в поиске)",
	},
	"Дней без заказов": {
		meaning: "полных дней тишины; подсветка 3-4 / 5-6 / 7+ (письмо гендира: оценка на 5-й, эскалация на 7-й)",
		source:  "расчёт: сегодня − max(последний заказ, начало продаж) − 1; сегодня не считается",
	},
	"Сигнал": {
		meaning: "медленный старт (заказов за жизнь ≤ slow_start_max_orders=2) / остановка продаж (больше — разгонялась)",
		source:  "расчёт по orders_total и порогам config.yaml (watch)",
	},
	"Последний заказ": {
		meaning: "дата последнего заказа; пусто = заказов не было никогда",
		source:  "orders: MAX(order_date), только неотменённые (is_cancel=false)",
	},
	"Заказов всего": {
		meaning: "заказов за жизнь — критерий разреза медленный старт / остановка",
		source:  "orders: COUNT(*) неотменённых; строка = единица заказа",
	},
	"Открытия 7д": {
		meaning: "сколько раз открывали карточку за окно funnel_window_days=7 (включая сегодня)",
		source:  "funnel_metrics_daily: SUM(open_count)",
		notes:   "0 может означать и «вне покрытия воронки» (воронка покрывает не весь парк): у таких карточек «нет видимости» — возможно, артефакт покрытия, а не факт",
	},
	"Корзины 7д": {
		meaning: "добавления в корзину, то же окно",
		source:  "funnel_metrics_daily: SUM(cart_count)",
	},
	"Скрыта": {
		meaning: "ДА = WB скрыл карточку из каталога (kill-фактор: показов не будет); н/д = нет данных",
		source:  "item_ratings.is_shadowed последнего периода",
		notes:   "н/д ≠ «не скрыта»: строки item_ratings может не быть",
	},
	"Рейтинг 0-10": {
		meaning: "рейтинг карточки WB; н/д = нет строки",
		source:  "products.product_rating",
		notes:   "у новых карточек часто н/д",
	},
	"Длина назв.": {
		meaning: "длина наименования WB в символах",
		source:  "расчёт Go: len([]rune(title)) — кириллица считается по буквам, не байтам",
	},
	"Длина опис.": {
		meaning: "длина описания карточки в символах; 0 = описания нет",
		source:  "cards.description: COALESCE(length(...), 0) — символы, не байты; сам текст не выводится",
		notes:   "WB ограничивает описание ~2000 симв.; в среднем по парку ~690, описания нет у ~20%",
	},
	"Наименование 1С": {
		meaning: "полное наименование из 1С — образец для исправления короткого названия WB",
		source:  "onec_goods.name_im",
	},
	"Фото шт.": {
		meaning: "число фото карточки; триггер <4 — янтарь",
		source:  "card_photos: COUNT(*)",
	},
	"Видео": {
		meaning: "да = у карточки есть видео; «—» = нет",
		source:  "cards.video (URL присутствует)",
		notes:   "видео есть у ~10% парка",
	},
	"Характ. шт.": {
		meaning: "число заполненных характеристик карточки",
		source:  "card_characteristics: COUNT(*)",
	},
	"Остаток WB": {
		meaning: "суммарный остаток по всем размерам; н/д = нет ни одного источника остатков",
		source:  "каскад: stocks_daily_warehouses → warehouse_remains → stock_products (первый с qty>0)",
		notes:   "ветка не выводится: FBS-товар с нулевым FBO молча берёт цифру портала (например 61 шт у карточки-примера гендира)",
	},
	"Размеров WB": {
		meaning: "размеров с остатком>0 на WB; н/д = нет размерных данных",
		source:  "stocks: COUNT(DISTINCT chrt_id) → fallback warehouse_remains (tech_size)",
	},
	"Размеров 1С": {
		meaning: "размеров с free>0 на 1С-складе каскада",
		source:  "onec_rests: COUNT(DISTINCT sku_guid), склад из колонки «Склад 1С»",
	},
	"Склад 1С": {
		meaning: "какой склад каскада дал «Размеров 1С»: РВБ ФБО → Внуково",
		source:  "onec_rests.storage_name (приоритет unnest WITH ORDINALITY из config.yaml onec)",
		notes:   "«Склад ОПТ» — зеркало Внукова, в каскад не входит",
	},
	"Ряд WB/1С": {
		meaning: "отношение «Размеров WB» к «Размеров 1С»; триггер <0.5 — янтарь",
		source:  "расчёт Go; н/д = не посчитать (нет размерных данных WB или 1С)",
	},
	"Доступность": {
		meaning: "оценка WB оборачиваемости товара: «залежался (неликвид)» — остатков много, покупают мало; «баланс (норма)»; «не рассчитано»",
		source:  "stock_products.availability (последний срез); классификация «Доступность товара» — официальная, от WB",
		notes:   "оценка самого WB, не наша; все категории с расшифровками — лист «Оценка WB»; пусто = строки портала нет вовсе",
	},
	"Кампаний": {
		meaning: "живых рекламных кампаний; 0 при нулевых открытиях — карточку не находят",
		source:  "campaign_products ⋈ campaigns, статус 9 «Активна» (config.yaml thresholds)",
	},
	"Главная гипотеза": {
		meaning: "дерево диагностики: нет открытий → видимость/продвижение/тень; открытия есть, корзин нет → наименование/контент; корзины есть → размеры/конверсия",
		source:  "расчёт Go по факторам строки (hypothesis.go)",
		notes:   "цена — не первая гипотеза (письмо гендира)",
	},
	"Ссылка на фото": {
		meaning: "гиперссылка на полноразмерное фото карточки",
		source:  "card_photos (big-URL)",
		notes:   "рендер-колонка, в БД не пишется",
	},
}

// addGlossarySheet — лист «Глоссарий»: по строке на колонку отчёта, в порядке
// reportColumns. Отсутствующее описание помечается явно (и роняет тест).
func addGlossarySheet(f *excelize.File) {
	sheet := "Глоссарий"
	f.NewSheet(sheet)

	title, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Size: 14}})
	head, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"4472C4"}}})
	bold, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	wrap, _ := f.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}})

	f.SetCellValue(sheet, "A1", "ГЛОССАРИЙ КОЛОНОК: СМЫСЛ — СЫРЬЁ — НЮАНСЫ")
	f.SetCellStyle(sheet, "A1", "A1", title)
	f.SetCellValue(sheet, "A2", "Сырьё — таблицы RAW (схема public) и расчёты. Порядок строк = порядок колонок отчёта.")

	row := 4
	for i, h := range []string{"Колонка", "Что показывает", "Сырьё (RAW / расчёт)", "Нюансы чтения"} {
		xSet(f, sheet, row, i+1, h)
		f.SetCellStyle(sheet, fmt.Sprintf("%s%d", string(rune('A'+i)), row),
			fmt.Sprintf("%s%d", string(rune('A'+i)), row), head)
	}
	row++
	for _, c := range reportColumns {
		g, ok := glossary[c.header]
		if !ok {
			g = glossaryEntry{meaning: "⚠ НЕТ ОПИСАНИЯ — добавьте в glossary.go", source: "—", notes: "—"}
		}
		xSet(f, sheet, row, 1, c.header)
		f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), bold)
		xSet(f, sheet, row, 2, g.meaning)
		xSet(f, sheet, row, 3, g.source)
		xSet(f, sheet, row, 4, g.notes)
		for col := 'A'; col <= 'D'; col++ {
			f.SetCellStyle(sheet, fmt.Sprintf("%c%d", col, row), fmt.Sprintf("%c%d", col, row), wrap)
		}
		f.SetRowHeight(sheet, row, 30)
		row++
	}

	f.SetColWidth(sheet, "A", "A", 18)
	f.SetColWidth(sheet, "B", "B", 52)
	f.SetColWidth(sheet, "C", "C", 52)
	f.SetColWidth(sheet, "D", "D", 48)

	// Смежные термины, звучащие рядом с отчётом, но не являющиеся колонками.
	// Атрибуция обязательна: чей термин — WB / общепринятый / наш.
	row += 1
	xSet(f, sheet, row, 1, "СМЕЖНЫЕ ТЕРМИНЫ (не колонки отчёта)")
	f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), title)
	row++
	for i, h := range []string{"Термин", "Чей термин", "Смысл и связь с отчётом", ""} {
		if h == "" {
			continue
		}
		xSet(f, sheet, row, i+1, h)
		f.SetCellStyle(sheet, fmt.Sprintf("%s%d", string(rune('A'+i)), row),
			fmt.Sprintf("%s%d", string(rune('A'+i)), row), head)
	}
	row++
	for _, t := range relatedTerms {
		xSet(f, sheet, row, 1, t.term)
		f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), bold)
		xSet(f, sheet, row, 2, t.whose)
		xSet(f, sheet, row, 3, t.meaning)
		for col := 'A'; col <= 'C'; col++ {
			f.SetCellStyle(sheet, fmt.Sprintf("%c%d", col, row), fmt.Sprintf("%c%d", col, row), wrap)
		}
		f.SetRowHeight(sheet, row, 42)
		row++
	}
}

// relatedTerms — смежные термины для блока внизу «Глоссария»: звучат в
// обсуждениях вокруг отчёта, но колонками не являются. Атрибуция — чей термин.
var relatedTerms = []struct {
	term    string
	whose   string
	meaning string
}{
	{
		term:  "Slow movers (медленно оборачивающиеся)",
		whose: "общепринятый термин товарного управления (не WB и не наш)",
		meaning: "Товары, которые продаются медленнее, чем пополняется запас (оборот/остаток); антоним — fast movers. " +
			"В отчёте отражается тремя метриками: «залежался (неликвид)» — оценка оборачиваемости самим WB (он считает её по своим данным); " +
			"сигнал «остановка продаж» — slow mover по факту замолчания; сигнал «медленный старт» — slow mover на ранней стадии жизни карточки. " +
			"Не всякий slow mover — предмет этого монитора: медленный, но стабильный хвост вне сезона остаётся категорией наблюдения (письмо гендира — про раннюю диагностику свежей коллекции)",
	},
}

// availabilityCategories — классификация WB «Доступность товара».
// КОЛОНКА «НАЗВАНИЕ WB» — ДОСЛОВНО официальная (документация API Wildberries,
// 11-analytics.yaml availabilityFilters); расшифровки и следствия — наша
// интерпретация для бизнеса, на листе помечены отдельно. Политика подачи:
// гендир должен видеть, что категория — понятие маркетплейса, а не изобретение
// отчёта или AI-агента.
var availabilityCategories = []struct {
	code     string // код API WB (хранится в БД)
	official string // официальное название WB, дословно
	plain    string // наша расшифровка
	effect   string // практическое следствие (наше/практика)
}{
	{"nonLiquid", "Неликвид", "«Залежался»: на складах WB остатков много, а покупают мало — оборачиваемость низкая", "WB обычно ограничивает приёмку следующих поставок (практика портала). В отчёте: «залежался (неликвид)»"},
	{"balanced", "Баланс", "Продажи и остатки сбалансированы", "Норма — вмешательства не требуется"},
	{"deficient", "Дефицит", "Спрос выше остатков: товар быстро разбирают", "Сигнал пополнить склад"},
	{"actual", "Актуальный", "Товар в спросе, продаётся стабильно", "Норма"},
	{"nonActual", "Неактуальный", "Вне сезона или спрос упал", "Кандидат на распродажу или вывод"},
	{"invalidData", "Не рассчитано", "Данным WB недостаточно для оценки (например, карточка новая)", "Не вердикт — дождаться расчёта WB"},
}

// addAvailabilitySheet — лист «Оценка WB»: официальная классификация
// «Доступность товара» с разведением «от WB дословно» и «наша расшифровка».
func addAvailabilitySheet(f *excelize.File) {
	sheet := "Оценка WB"
	f.NewSheet(sheet)

	title, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Size: 14}})
	head, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"4472C4"}}})
	wrap, _ := f.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}})
	official, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true},
		Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}})

	f.SetCellValue(sheet, "A1", "ОЦЕНКА WB: «ДОСТУПНОСТЬ ТОВАРА»")
	f.SetCellStyle(sheet, "A1", "A1", title)
	f.SetCellValue(sheet, "A2", "Классификация и названия категорий — официальные, из документации API Wildberries (аналитика продавца).")
	f.SetCellValue(sheet, "A3", "Это оценка самого маркетплейса, а не наша и не AI-агента. Расшифровки простыми словами — наши, в отдельной колонке.")
	f.SetCellValue(sheet, "A4", "Значение приходит в отчёт из портала продавца WB (остатки → доступность); в базе хранится исходный код WB.")

	row := 6
	for i, h := range []string{"Код API WB", "Название WB (официальное, дословно)", "Расшифровка (наша, для бизнеса)", "Практическое следствие (наше)"} {
		xSet(f, sheet, row, i+1, h)
		cell, _ := excelize.CoordinatesToCellName(i+1, row)
		f.SetCellStyle(sheet, cell, cell, head)
	}
	row++
	for _, c := range availabilityCategories {
		xSet(f, sheet, row, 1, c.code)
		xSet(f, sheet, row, 2, c.official)
		xSet(f, sheet, row, 3, c.plain)
		xSet(f, sheet, row, 4, c.effect)
		cellB := fmt.Sprintf("B%d", row)
		f.SetCellStyle(sheet, cellB, cellB, official)
		for col := 'A'; col <= 'D'; col++ {
			cell := fmt.Sprintf("%c%d", col, row)
			f.SetCellStyle(sheet, cell, cell, wrap)
		}
		f.SetRowHeight(sheet, row, 30)
		row++
	}

	f.SetColWidth(sheet, "A", "A", 14)
	f.SetColWidth(sheet, "B", "B", 30)
	f.SetColWidth(sheet, "C", "C", 52)
	f.SetColWidth(sheet, "D", "D", 48)
}
