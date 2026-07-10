// query.go — read-only SQL-запрос готовности карточек и структура Row.
//
// Один запрос: onec_goods (движок) → LEFT JOIN cards (nmID, название, описание),
// products (карточный рейтинг 0-10, звёзды 0-5), stock_products (агрегаты WB),
// 1С-остатки (onec_rests), кол-во складов с остатком (stocks_daily_warehouses).
// Товары без nmID всплывают автоматически — это и есть сигнал «нет на WB».
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Row — одна строка отчёта (авто-часть: всё, что берётся из PG).
type Row struct {
	// ── 1С (onec_goods) ──
	Article         string // Артикул
	ArticleNum      string // Артикул (числовое значение, digits-only)
	Sex             string // Пол
	Collection      string // Коллекция
	AgeSegment      string // Возраст (парсится из collection)
	NameIM          string // Наименование для печати / Вид номенклатуры (best-effort)
	Category        string // детальная категория 1С: category_level2_name → level1_name → корневой category
	ProductionYear  int    // год производства из символов 2-3 артикула (конвенция репо); 0 = нет данных/легаси
	Color           string // цвет
	SizeRange       string // Диапазон размеров
	ModelStatus     string // этап/движение товара (model_status)
	ArticleBlocked  bool   // заблокирован в 1С (is_article_blocked)
	ModelCancelled  bool   // модель снята (is_model_cancelled)
	// ── WB (cards / products / stock_products / stocks_daily_warehouses) ──
	NmID            *int64   // nmID; NULL = карточка на WB не создана
	WBName          string   // Наименование WB (cards.title)
	HasDescription  bool     // описание готово (cards.description непусто)
	Description     string   // Описание WB — полный текст cards.description (обрезается при выводе до maxDescriptionLen)
	ProductRating   float64  // карточный рейтинг WB 0-10 (products.product_rating)
	FeedbackRating  float64  // звёзды отзывов 0-5 (products.feedback_rating)
	OrdersCount     int64    // заказы (stock_products.orders_count, latest)
	BuyoutCount     int64    // выкупы (stock_products.buyout_count, latest)
	WBStock         int64    // остаток WB (stock_products.stock_count, latest)
	OneCReserv      int64    // остаток 1С резерв (SUM onec_rests.reserv, latest)
	OneCFree        int64    // остаток 1С свободно (SUM onec_rests.free, latest)
	WHWithStock     int64    // кол-во складов WB с остатком (stocks_daily_warehouses)
	// ── Фото (card_photos; заполняется отдельным батч-запросом loadPhotoURLs) ──
	PhotoTM         string   // URL миниатюры WB (card_photos.tm) — для встраивания
	PhotoBig        string   // URL полноразмерного фото (card_photos.big) — для ссылки
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

// HasWBCard — создана ли карточка на WB (есть nmID).
func (r Row) HasWBCard() bool { return r.NmID != nil }

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
// collection_season=1439, union=2917.
func loadRows(ctx context.Context, conn *pgxpool.Pool, collections, seasons []string, limit int) ([]Row, error) {
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
			&r.NmID, &r.WBName, &r.HasDescription, &r.Description,
			&r.ProductRating, &r.FeedbackRating,
			&r.OrdersCount, &r.BuyoutCount, &r.WBStock,
			&r.OneCReserv, &r.OneCFree, &r.WHWithStock,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		r.AgeSegment = parseAgeSegment(r.Collection)
		r.ProductionYear = articleYear(r.Article)
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
