// query.go — read-only SQL-запрос готовности карточек и структура Row.
//
// Один запрос: onec_goods (движок) → LEFT JOIN cards (nmID, название, описание),
// products (карточный рейтинг 0-10, звёзды 0-5), stock_products (агрегаты WB),
// 1С-остатки (onec_rests), кол-во складов с остатком (stocks_daily_warehouses).
// Товары без nmID всплывают автоматически — это и есть сигнал «нет на WB».
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Row — одна строка отчёта (авто-часть: всё, что берётся из PG).
type Row struct {
	// ── 1С (onec_goods) ──
	Article        string // Артикул
	ArticleNum     string // Артикул (числовое значение, digits-only)
	Sex            string // Пол
	Collection     string // Коллекция
	AgeSegment     string // Возраст (парсится из collection)
	NameIM         string // Наименование для печати / Вид номенклатуры (best-effort)
	Category       string // детальная категория 1С: category_level2_name → level1_name → корневой category
	ProductionYear int    // год производства из символов 2-3 артикула (конвенция репо); 0 = нет данных/легаси
	Color          string // цвет
	SizeRange      string // Диапазон размеров
	ModelStatus    string // этап/движение товара (model_status)
	ArticleBlocked bool   // заблокирован в 1С (is_article_blocked)
	ModelCancelled bool   // модель снята (is_model_cancelled)
	// ── WB (cards / products / stock_products / stocks_daily_warehouses) ──
	NmID           *int64  // nmID; NULL = карточка на WB не создана
	WBName         string  // Наименование WB (cards.title)
	HasDescription bool    // описание готово (cards.description непусто)
	Description    string  // Описание WB — полный текст cards.description (обрезается при выводе до maxDescriptionLen)
	ProductRating  float64 // карточный рейтинг WB 0-10 (products.product_rating)
	FeedbackRating float64 // звёзды отзывов 0-5 (products.feedback_rating)
	OrdersCount    int64   // заказы (stock_products.orders_count, latest)
	BuyoutCount    int64   // выкупы (stock_products.buyout_count, latest)
	WBStock        int64   // остаток WB (stock_products.stock_count, latest)
	OneCReserv     int64   // остаток 1С резерв (SUM onec_rests.reserv, latest)
	OneCFree       int64   // остаток 1С свободно (SUM onec_rests.free, latest)
	WHWithStock    int64   // кол-во складов WB с остатком (stocks_daily_warehouses)
	// ── Фото (card_photos; заполняется отдельным батч-запросом loadPhotoURLs) ──
	PhotoTM  string // URL миниатюры WB (card_photos.tm) — для встраивания
	PhotoBig string // URL полноразмерного фото (card_photos.big) — для ссылки
	// ── Характеристики карточки (card_characteristics + card_sizes; заполняется отдельным батч-запросом loadCardChars) ──
	CharLines []string // «Название: значение1, значение2» — по строке на характеристику, отсортированы по названию
	WBSizes   string   // размеры карточки WB через запятую (card_sizes.tech_size, distinct)
	WBCertNum string   // номер сертификата с карточки WB (char_id 15001136; «» = не заполнен)
	WBDeclNum string   // номер декларации с карточки WB (char_id 15001135; «» = не заполнен)
	WBTnved   string   // ТНВЭД с карточки WB (char_id 15000001; отображается в колонке сертификата)
	// ── Сертификат/декларация из 1С (onec_goods.certificate*) ──
	HasOneCCert bool   // 1С декларирует наличие документа (has_certificate)
	CertType    string // русская метка: «Сертификат» / «Декларация» (из certificate_type)
	CertNumber  string // номер документа (без «№»)
	CertEnd     string // срок действия, как в 1С (текст; парсится при выводе)
}

// PhotoURL — пара URL фото для одного nmID (миниатюра + полноразмерное).
type PhotoURL struct {
	TM  string
	Big string
}

// loadPhotoURLs возвращает первое (MIN id) фото на каждый nmID из card_photos.
//
// Отдельный батч-запрос (а не LATERAL в основном SELECT): nmID уже есть в строках,
// а здесь мы одним round-trip получаем tm+big для всего списка. card_photos.id —
// BIGSERIAL (cards_schema.go:69), MIN(id) берёт первое/главное фото карточки.
func loadPhotoURLs(ctx context.Context, conn *pgxpool.Pool, nmIDs []int64) (map[int64]PhotoURL, error) {
	out := make(map[int64]PhotoURL, len(nmIDs))
	if len(nmIDs) == 0 {
		return out, nil
	}
	const q = `
SELECT nm_id, tm, big
FROM card_photos
WHERE nm_id = ANY($1)
  AND id IN (SELECT MIN(id) FROM card_photos WHERE nm_id = ANY($1) GROUP BY nm_id)`
	rows, err := conn.Query(ctx, q, nmIDs)
	if err != nil {
		return nil, fmt.Errorf("card_photos query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var nmID int64
		var pu PhotoURL
		if err := rows.Scan(&nmID, &pu.TM, &pu.Big); err != nil {
			return nil, fmt.Errorf("card_photos scan: %w", err)
		}
		out[nmID] = pu
	}
	return out, rows.Err()
}

// charID сертификатных характеристик карточки WB (те же константы, что в fix-certificates/stage.go).
const (
	charDeclNumberID int64 = 15001135 // «Номер декларации соответствия»
	charCertNumberID int64 = 15001136 // «Номер сертификата соответствия»
	charCertBeginID  int64 = 15001137 // «Дата регистрации сертификата/декларации»
	charCertEndID    int64 = 15001138 // «Дата окончания действия сертификата/декларации»
	charTnvedID      int64 = 15000001 // «ТНВЭД» — тоже ВЭД-домен, живёт в колонке сертификата
)

// CardChars — характеристики и размеры карточки WB (один nmID).
type CardChars struct {
	Lines   []string // «Название: значение1, значение2» — по строке, отсортированы по названию
	Sizes   []string // размеры (card_sizes.tech_size, distinct; числовые — по значению)
	CertNum string   // номер сертификата с карточки (char_id 15001136; «» = не заполнен)
	DeclNum string   // номер декларации с карточки (char_id 15001135; «» = не заполнен)
	Tnved   string   // ТНВЭД с карточки (char_id 15000001; «» = не заполнен)
}

// rawChar — сырая строка card_characteristics до рендера.
type rawChar struct {
	charID int64
	name   string
	values []string
}

// loadCardChars возвращает характеристики и размеры карточек по списку nmID.
//
// Отдельный батч-запрос (как loadPhotoURLs): одним round-trip на таблицу.
// card_characteristics — строка = одна характеристика, значения в json_value
// (JSON-массив, бывают мультизначения: Цвет: ["тёмно-синий","красный","белый"]).
// card_sizes — строка = один размер карточки (tech_size).
func loadCardChars(ctx context.Context, conn *pgxpool.Pool, nmIDs []int64) (map[int64]*CardChars, error) {
	out := make(map[int64]*CardChars, len(nmIDs))
	if len(nmIDs) == 0 {
		return out, nil
	}

	charsRows, err := conn.Query(ctx, `
SELECT nm_id, char_id, name, json_value
FROM card_characteristics
WHERE nm_id = ANY($1)`, nmIDs)
	if err != nil {
		return nil, fmt.Errorf("card_characteristics query: %w", err)
	}
	defer charsRows.Close()
	perNM := make(map[int64][]rawChar)
	for charsRows.Next() {
		var nmID, charID int64
		var name, jsonValue string
		if err := charsRows.Scan(&nmID, &charID, &name, &jsonValue); err != nil {
			return nil, fmt.Errorf("card_characteristics scan: %w", err)
		}
		vals, err := parseJSONValues(jsonValue)
		if err != nil {
			// Повреждённый JSON — показываем сырое значение, характеристику не теряем.
			vals = []string{strings.TrimSpace(jsonValue)}
		}
		perNM[nmID] = append(perNM[nmID], rawChar{charID: charID, name: name, values: vals})
	}
	if err := charsRows.Err(); err != nil {
		return nil, fmt.Errorf("card_characteristics: %w", err)
	}

	sizeRows, err := conn.Query(ctx, `
SELECT nm_id, tech_size
FROM card_sizes
WHERE nm_id = ANY($1)`, nmIDs)
	if err != nil {
		return nil, fmt.Errorf("card_sizes query: %w", err)
	}
	defer sizeRows.Close()
	sizesPerNM := make(map[int64][]string)
	for sizeRows.Next() {
		var nmID int64
		var size string
		if err := sizeRows.Scan(&nmID, &size); err != nil {
			return nil, fmt.Errorf("card_sizes scan: %w", err)
		}
		size = strings.TrimSpace(size)
		if size != "" && !slices.Contains(sizesPerNM[nmID], size) {
			sizesPerNM[nmID] = append(sizesPerNM[nmID], size)
		}
	}
	if err := sizeRows.Err(); err != nil {
		return nil, fmt.Errorf("card_sizes: %w", err)
	}

	for nmID, chars := range perNM {
		out[nmID] = renderCardChars(chars, sizesPerNM[nmID])
	}
	// Карточки без характеристик, но с размерами — тоже попадают в карту.
	for nmID, sizes := range sizesPerNM {
		if _, ok := out[nmID]; !ok {
			out[nmID] = renderCardChars(nil, sizes)
		}
	}
	return out, nil
}

// renderCardChars собирает характеристики в строки отчёта (чистая функция — для тестов).
// Пустые характеристики (нет значений) пропускаются: имя без значения не информативно.
// Разрешительная документация ВЭД (номер/даты сертификата и декларации, ТНВЭД) НЕ попадает
// в маркетинговые строки: номера идут в слоты для сверки с 1С, ТНВЭД — отдельным полем;
// всё это отображается колонкой «Сертификат/декларация».
func renderCardChars(chars []rawChar, sizes []string) *CardChars {
	cc := &CardChars{Sizes: sortSizes(sizes)}
	for _, ch := range chars {
		if len(ch.values) == 0 {
			continue
		}
		switch ch.charID {
		case charDeclNumberID:
			// Номера — из первого элемента значений (как normalizeValue в fix-certificates):
			// слоты нужны для сверки с 1С; пустой json_value — номер НЕ перенесён.
			cc.DeclNum = strings.TrimSpace(ch.values[0])
			continue
		case charCertNumberID:
			cc.CertNum = strings.TrimSpace(ch.values[0])
			continue
		case charCertBeginID, charCertEndID:
			continue
		case charTnvedID:
			cc.Tnved = strings.Join(ch.values, ", ")
			continue
		}
		name := strings.TrimSpace(ch.name)
		if name == "" {
			name = fmt.Sprintf("char_%d", ch.charID)
		}
		cc.Lines = append(cc.Lines, name+": "+strings.Join(ch.values, ", "))
	}
	sort.Strings(cc.Lines)
	return cc
}

// parseJSONValues разбирает json_value характеристики — JSON-массив строк/чисел
// («["хлопок 95%","эластан 5%"]», «[150]»).
func parseJSONValues(s string) ([]string, error) {
	var arr []interface{}
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		switch t := v.(type) {
		case string:
			if t != "" {
				out = append(out, t)
			}
		case float64:
			out = append(out, strconv.FormatFloat(t, 'f', -1, 64))
		case nil:
			// null — пропускаем
		default:
			out = append(out, fmt.Sprintf("%v", v))
		}
	}
	return out, nil
}

// sortSizes — числовые размеры по значению («62, 74, 128», не лексикографически),
// при любом нечисловом размере (диапазоны, one_size) — обычная сортировка строк.
func sortSizes(sizes []string) []string {
	out := slices.Clone(sizes)
	nums := make([]int, len(out))
	allNum := true
	for i, s := range out {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			allNum = false
			break
		}
		nums[i] = n
	}
	if allNum {
		sort.Ints(nums)
		for i := range out {
			out[i] = strconv.Itoa(nums[i])
		}
		return out
	}
	sort.Strings(out)
	return out
}

// ruCertType переводит certificate_type из 1С (Certificate/Declaration) в русскую метку.
func ruCertType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "certificate":
		return "Сертификат"
	case "declaration":
		return "Декларация"
	}
	return strings.TrimSpace(t)
}

// HasWBCard — создана ли карточка на WB (есть nmID).
func (r Row) HasWBCard() bool { return r.NmID != nil }

// WBHasCertDecl — заполнен ли номер сертификата/декларации на карточке WB.
func (r Row) WBHasCertDecl() bool { return r.WBCertNum != "" || r.WBDeclNum != "" }

// reportQuery строит SQL. limit>0 добавляет LIMIT.
//
// snapshot_date хранится как TEXT в ISO-формате (YYYY-MM-DD), поэтому max() и сравнение
// делаем как text — лексикографический максимум = последний день, и используются индексы.
const reportQueryBase = `
WITH
  latest_sp    AS (SELECT max(snapshot_date) AS d FROM stock_products),
  latest_stock AS (SELECT max(snapshot_date) AS d FROM stocks_daily_warehouses),
  latest_rests AS (SELECT max(snapshot_date) AS d FROM onec_rests),
  rests AS (
    SELECT good_guid, COALESCE(sum(reserv),0) AS reserv, COALESCE(sum(free),0) AS free
    FROM onec_rests
    WHERE snapshot_date = (SELECT d FROM latest_rests)
    GROUP BY good_guid
  ),
  wh AS (
    SELECT nm_id, count(DISTINCT warehouse_id) AS cnt
    FROM stocks_daily_warehouses
    WHERE snapshot_date = (SELECT d FROM latest_stock) AND quantity > 0
    GROUP BY nm_id
  )
SELECT
  o.article,
  regexp_replace(o.article, '\D', '', 'g'),
  o.sex,
  o.collection,
  o.name_im,
  COALESCE(NULLIF(o.category_level2_name,''), NULLIF(o.category_level1_name,''), o.category),
  o.color,
  o.size_range,
  o.model_status,
  o.is_article_blocked,
  o.is_model_cancelled,
  o.has_certificate,
  COALESCE(o.certificate_type, ''),
  COALESCE(o.certificate_number, ''),
  COALESCE(o.certificate_end, ''),
  c.nm_id,
  COALESCE(c.title, ''),
  COALESCE(c.description IS NOT NULL AND c.description <> '', false),
  COALESCE(c.description, ''),
  COALESCE(p.product_rating, 0),
  COALESCE(p.feedback_rating, 0),
  COALESCE(sp.orders_count, 0),
  COALESCE(sp.buyout_count, 0),
  COALESCE(sp.stock_count, 0),
  COALESCE(r.reserv, 0),
  COALESCE(r.free, 0),
  COALESCE(w.cnt, 0)
FROM onec_goods o
LEFT JOIN cards          c  ON c.vendor_code = o.article
LEFT JOIN products       p  ON p.nm_id = c.nm_id
LEFT JOIN stock_products sp ON sp.nm_id = c.nm_id
                           AND sp.snapshot_date = (SELECT d FROM latest_sp)
LEFT JOIN rests          r  ON r.good_guid = o.guid
LEFT JOIN wh             w  ON w.nm_id = c.nm_id`

// loadRows выполняет read-only запрос и возвращает строки отчёта.
//
// Фильтры collections и seasons комбинируются через AND (OR внутри каждого списка).
// Хотя бы один из них должен быть задан. seasons матчит ОБА поля: season (функциональный
// сезон ткани) OR collection_season (коммерческая коллекция) — т.к. collection_season на 77%
// пуст и одно это поле теряет товары «School boys/girls YYYY». Для 'Школа': season=2880,
// collection_season=1439, union=2917. allowedYears — годы производства по символам 2-3
// артикула (SQL-side: substring = ANY; невалидные годы отбрасываются при активном фильтре).
func loadRows(ctx context.Context, conn *pgxpool.Pool, collections, seasons []string, allowedYears []int, limit int) ([]Row, error) {
	if len(collections) == 0 && len(seasons) == 0 {
		return nil, fmt.Errorf("не заданы ни коллекции, ни сезоны (collections/seasons в config.yaml или --collections/--seasons)")
	}

	// Динамическая сборка WHERE — filter.BuildSQL не используем (он SQLite-only).
	var conds []string
	args := []interface{}{}
	pi := 1 // номер pgx-плейсхолдера ($1, $2, …)
	if len(collections) > 0 {
		conds = append(conds, fmt.Sprintf("o.collection = ANY($%d::text[])", pi))
		args = append(args, collections)
		pi++
	}
	if len(seasons) > 0 {
		// Сезонный слой ассортимента: матч по season (функциональный сезон ткани) ИЛИ
		// collection_season (коммерческая коллекция). collection_season на 77% пуст, поэтому
		// одно это поле слишком узко (потеря «School boys/girls YYYY»). Один массив $pi
		// переиспользуется в обоих ANY — pgx допускает повтор плейсхолдера.
		conds = append(conds, fmt.Sprintf(
			"(o.season = ANY($%d::text[]) OR o.collection_season = ANY($%d::text[]))", pi, pi))
		args = append(args, seasons)
		pi++
	}
	if len(allowedYears) > 0 {
		// Год производства: символы 2-3 артикула (конвенция репо, как articleYear).
		// substring в SQL ≡ article[1:3] в Go: не-цифры/короткие артикулы не совпадут —
		// та же семантика, что была у Go-side фильтра по Row.ProductionYear (9d0f794),
		// но фильтр в WHERE: корректен с LIMIT и не тащит лишние строки.
		years := make([]string, len(allowedYears))
		for i, y := range allowedYears {
			years[i] = fmt.Sprintf("%02d", y%100)
		}
		conds = append(conds, fmt.Sprintf("substring(o.article from 2 for 2) = ANY($%d::text[])", pi))
		args = append(args, years)
		pi++
	}

	q := reportQueryBase + "\nWHERE " + strings.Join(conds, " AND ") +
		"\nORDER BY o.collection, o.article"
	if limit > 0 {
		q += fmt.Sprintf("\nLIMIT $%d", pi)
		args = append(args, limit)
	}

	rows, err := conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var out []Row
	for rows.Next() {
		var r Row
		if err := rows.Scan(
			&r.Article, &r.ArticleNum, &r.Sex, &r.Collection, &r.NameIM,
			&r.Category, &r.Color, &r.SizeRange, &r.ModelStatus,
			&r.ArticleBlocked, &r.ModelCancelled,
			&r.HasOneCCert, &r.CertType, &r.CertNumber, &r.CertEnd,
			&r.NmID, &r.WBName, &r.HasDescription, &r.Description,
			&r.ProductRating, &r.FeedbackRating,
			&r.OrdersCount, &r.BuyoutCount, &r.WBStock,
			&r.OneCReserv, &r.OneCFree, &r.WHWithStock,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		r.AgeSegment = parseAgeSegment(r.Collection)
		r.ProductionYear = articleYear(r.Article)
		r.CertType = ruCertType(r.CertType)
		out = append(out, r)
	}
	return out, rows.Err()
}

// parseAgeSegment извлекает возрастной сегмент из названия коллекции.
//
// onec_goods.age почти пуст (заполнен у <0.5% строк), зато сегмент зашит в collection:
// "CLASSIC 2026 girls Tween", "FLORA newborn-baby girls", "SWIMWEAR_2026 boys Kids".
// Возвращаем первый найденный токен по приоритету (от младших к старшим).
func parseAgeSegment(collection string) string {
	low := strings.ToLower(collection)
	// Порядок: специфичные/младшие вперёд (newborn-baby не должен стать просто baby).
	for _, seg := range []string{"newborn", "baby", "kids", "junior", "tween", "teen", "adults", "adult"} {
		if strings.Contains(low, seg) {
			return strings.ToUpper(seg[:1]) + seg[1:]
		}
	}
	return ""
}

// articleYear — год производства по символам 2-3 артикула продавца (конвенция репо,
// как pkg/config/utility.go:1387 FilterNmIDsByYear: article[1:3] = 1-индексные символы 2-3).
// Пример: "22527124" → "25" → 2025. 0 = артикул короче 3 символов или не-цифры (легаси/мусор).
func articleYear(article string) int {
	if len(article) < 3 {
		return 0
	}
	a, b := article[1], article[2]
	if a < '0' || a > '9' || b < '0' || b > '9' {
		return 0
	}
	return 2000 + int(a-'0')*10 + int(b-'0')
}
