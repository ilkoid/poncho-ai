package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/config"

	_ "github.com/mattn/go-sqlite3"
)

// addressFOPatterns maps address substring patterns to federal district names.
// Ordered by specificity: international first, then region-level (край/область/республика),
// then city-level fallbacks. First match wins.
var addressFOPatterns = []struct{ Pattern, FO string }{
	// International (check first — unambiguous)
	{"Беларусь", "Беларусь"},
	{"Минская обл", "Беларусь"},
	{"Гродно", "Беларусь"},
	{"Брест", "Беларусь"},
	{"Казахстан", "Казахстан"},
	{"Нур-Султан", "Казахстан"},
	{"Астана", "Казахстан"},
	{"Алматы", "Казахстан"},
	{"Атакент", "Казахстан"},
	{"Актобе", "Казахстан"},
	{"Шымкент", "Казахстан"},
	{"Байсерке", "Казахстан"},
	{"Караганда", "Казахстан"},
	{"Армения", "Армения"},
	{"Узбекистан", "Узбекистан"},
	{"Ташкент", "Узбекистан"},
	{"Таджикистан", "Таджикистан"},
	{"Душанбе", "Таджикистан"},
	{"Dushanbe", "Таджикистан"},
	{"Грузия", "Грузия"},
	{"Тбилиси", "Грузия"},
	{"Tbilisi", "Грузия"},
	{"Кыргызстан", "Кыргызстан"},

	// Северо-Кавказский ФО (check before ЮФО)
	{"Дагестан", "Северо-Кавказский"},
	{"РСО-Алания", "Северо-Кавказский"},
	{"Северная Осетия", "Северо-Кавказский"},
	{"Ставропольский край", "Северо-Кавказский"},
	{"Ингушетия", "Северо-Кавказский"},
	{"Кабардино-Балкар", "Северо-Кавказский"},
	{"Карачаево-Черкес", "Северо-Кавказский"},
	{"Чеченск", "Северо-Кавказский"},

	// Южный ФО
	{"Краснодарский край", "Южный"},
	{"Астраханская обл", "Южный"},
	{"Волгоградская обл", "Южный"},
	{"Ростовская обл", "Южный"},
	{"Республика Адыгея", "Южный"},
	{"Калмыкия", "Южный"},
	{"Республика Крым", "Южный"},
	{"Симферополь", "Южный"},
	{"Севастополь", "Южный"},

	// Дальневосточный ФО (check before СФО — Забайкальский край moved to ДФО in 2018)
	{"Приморский край", "Дальневосточный"},
	{"Хабаровский край", "Дальневосточный"},
	{"Амурская обл", "Дальневосточный"},
	{"Забайкальский край", "Дальневосточный"},
	{"Республика Саха", "Дальневосточный"},
	{"Камчатский край", "Дальневосточный"},
	{"Магаданская обл", "Дальневосточный"},
	{"Сахалинская обл", "Дальневосточный"},
	{"Чукотск", "Дальневосточный"},
	{"Бурятия", "Дальневосточный"},

	// Сибирский ФО
	{"Новосибирская обл", "Сибирский"},
	{"Кемеровская обл", "Сибирский"},
	{"Кемеровская область", "Сибирский"},
	{"Томская обл", "Сибирский"},
	{"Омская обл", "Сибирский"},
	{"Иркутская обл", "Сибирский"},
	{"Алтайский край", "Сибирский"},
	{"Красноярский край", "Сибирский"},
	{"Республика Хакасия", "Сибирский"},
	{"Республика Алтай", "Сибирский"},
	{"Республика Тыва", "Сибирский"},

	// Уральский ФО
	{"Свердловская обл", "Уральский"},
	{"Тюменская обл", "Уральский"},
	{"Челябинская обл", "Уральский"},
	{"Курганская обл", "Уральский"},
	{"Ханты-Мансий", "Уральский"},
	{"Ямало-Ненецк", "Уральский"},

	// Приволжский ФО
	{"Республика Татарстан", "Приволжский"},
	{"Республика Башкортостан", "Приволжский"},
	{"Башкортостан", "Приволжский"},
	{"Удмуртск", "Приволжский"},
	{"Чувашск", "Приволжский"},
	{"Мордовия", "Приволжский"},
	{"Марий Эл", "Приволжский"},
	{"Пермский край", "Приволжский"},
	{"Кировская обл", "Приволжский"},
	{"Нижегородская обл", "Приволжский"},
	{"Оренбургская обл", "Приволжский"},
	{"Пензенская обл", "Приволжский"},
	{"Самарская обл", "Приволжский"},
	{"Саратовская обл", "Приволжский"},
	{"Ульяновская обл", "Приволжский"},

	// Северо-Западный ФО
	{"Ленинградская обл", "Северо-Западный"},
	{"Архангельская обл", "Северо-Западный"},
	{"Вологодская обл", "Северо-Западный"},
	{"Калининградская обл", "Северо-Западный"},
	{"Мурманская обл", "Северо-Западный"},
	{"Новгородская обл", "Северо-Западный"},
	{"Псковская обл", "Северо-Западный"},
	{"Республика Карелия", "Северо-Западный"},
	{"Республика Коми", "Северо-Западный"},
	{"Ненецк", "Северо-Западный"},

	// Центральный ФО
	{"Московская обл", "Центральный"},
	{"Московская область", "Центральный"},
	{"Белгородская обл", "Центральный"},
	{"Брянская обл", "Центральный"},
	{"Владимирская обл", "Центральный"},
	{"Воронежская обл", "Центральный"},
	{"Ивановская обл", "Центральный"},
	{"Калужская обл", "Центральный"},
	{"Костромская обл", "Центральный"},
	{"Курская обл", "Центральный"},
	{"Липецкая обл", "Центральный"},
	{"Орловская обл", "Центральный"},
	{"Рязанская обл", "Центральный"},
	{"Смоленская обл", "Центральный"},
	{"Тамбовская обл", "Центральный"},
	{"Тверская обл", "Центральный"},
	{"Тульская обл", "Центральный"},
	{"Ярославская обл", "Центральный"},

	// City-level fallbacks (for addresses without region info, matched in address or warehouse name)
	{"Подольск", "Центральный"},
	{"Коледино", "Центральный"},
	{"Электросталь", "Центральный"},
	{"Чехов", "Центральный"},
	{"Домодедово", "Центральный"},
	{"Калуга", "Центральный"},
	{"Тверь", "Центральный"},
	{"Курск", "Центральный"},
	{"Липецк", "Центральный"},
	{"Владимир", "Центральный"},
	{"Смоленск", "Центральный"},
	{"Воронеж", "Центральный"},
	{"Рязань", "Центральный"},
	{"Ярославль", "Центральный"},
	{"Брянск", "Центральный"},
	{"Тамбов", "Центральный"},
	{"Котовск", "Центральный"},
	{"Иваново", "Центральный"},
	{"Обухово", "Центральный"},
	{"Софьино", "Центральный"},
	{"Чашниково", "Центральный"},
	{"Солнечногорск", "Центральный"},
	{"Пушкино", "Центральный"},
	{"Истра", "Центральный"},
	{"Раменский", "Центральный"},
	{"Дмитровск", "Центральный"},
	{"Климовск", "Центральный"},
	{"Щербинка", "Центральный"},
	{"Голицыно", "Центральный"},
	{"Никольское", "Центральный"},
	{"Радумля", "Центральный"},
	{"Софрино", "Центральный"},
	{"Белая Дача", "Центральный"},
	{"Белые Столбы", "Центральный"},
	{"Москва", "Центральный"},
	{"Тула", "Центральный"},
	{"Внуково", "Центральный"},
	{"Шушары", "Северо-Западный"},
	{"Мурманск", "Северо-Западный"},
	{"Псков", "Северо-Западный"},
	{"Вологда", "Северо-Западный"},
	{"Череповец", "Северо-Западный"},
	{"Калининград", "Северо-Западный"},
	{"Сыктывкар", "Северо-Западный"},
	{"Архангельск", "Северо-Западный"},
	{"Красный Бор", "Северо-Западный"},
	{"Ломоносовский", "Северо-Западный"},
	{"Новосибирск", "Сибирский"},
	{"Кемерово", "Сибирский"},
	{"Томск", "Сибирский"},
	{"Омск", "Сибирский"},
	{"Барнаул", "Сибирский"},
	{"Абакан", "Сибирский"},
	{"Новокузнецк", "Сибирский"},
	{"Иркутск", "Сибирский"},
	{"Красноярск", "Сибирский"},
	{"Юрга", "Сибирский"},
	{"Екатеринбург", "Уральский"},
	{"Тюмень", "Уральский"},
	{"Сургут", "Уральский"},
	{"Челябинск", "Уральский"},
	{"Нижний Тагил", "Уральский"},
	{"Ноябрьск", "Уральский"},
	{"Казань", "Приволжский"},
	{"Ульяновск", "Приволжский"},
	{"Уфа", "Приволжский"},
	{"Пермь", "Приволжский"},
	{"Чебоксары", "Приволжский"},
	{"Сарапул", "Приволжский"},
	{"Новосемейкино", "Приволжский"},
	{"Ижевск", "Приволжский"},
	{"Оренбург", "Приволжский"},
	{"Кузнецк", "Приволжский"},
	{"Киров", "Приволжский"},
	{"Нижний Новгород", "Приволжский"},
	{"Пенза", "Приволжский"},
	{"Набережные Челны", "Приволжский"},
	{"Краснодар", "Южный"},
	{"Астрахань", "Южный"},
	{"Волгоград", "Южный"},
	{"Ростов", "Южный"},
	{"Крыловская", "Южный"},
	{"Владикавказ", "Северо-Кавказский"},
	{"Махачкала", "Северо-Кавказский"},
	{"Невинномысск", "Северо-Кавказский"},
	{"Пятигорск", "Северо-Кавказский"},
	{"Хабаровск", "Дальневосточный"},
	{"Владивосток", "Дальневосточный"},
	{"Артем", "Дальневосточный"},
	{"Белогорск", "Дальневосточный"},
	{"Чита", "Дальневосточный"},
	{"Гомель", "Беларусь"},
	{"Минск", "Беларусь"},
	{"Ереван", "Армения"},
	{"Остальные", "Центральный"},
	{"Вёшки", "Центральный"},
	{"Вешки", "Центральный"},
}

// parseAddressToFO extracts federal district from a warehouse address and name.
// First tries address patterns, then falls back to warehouse name matching.
// Uses case-insensitive comparison for robustness.
func parseAddressToFO(address, whName string) string {
	addrLower := strings.ToLower(address)
	whLower := strings.ToLower(whName)
	// 1. Try address patterns
	for _, p := range addressFOPatterns {
		if strings.Contains(addrLower, strings.ToLower(p.Pattern)) {
			return p.FO
		}
	}
	// 2. Try warehouse name patterns
	for _, p := range addressFOPatterns {
		if strings.Contains(whLower, strings.ToLower(p.Pattern)) {
			return p.FO
		}
	}
	return ""
}

// SQL queries for source DB (wb-sales.db, read-only).

// Stock positions aggregated by (nm_id, chrt_id, warehouse_id).
// Region is resolved via warehouse FO map in Go code (warehouse_id → FO).
// Includes in_way_from_client (returns from customers) as available stock.
const stockPositionsSQL = `
SELECT nm_id, chrt_id, warehouse_id,
       SUM(quantity + COALESCE(in_way_from_client, 0)) AS stock_qty
FROM stocks_daily_warehouses
WHERE snapshot_date = ?
  AND nm_id IN (%s)
GROUP BY nm_id, chrt_id, warehouse_id
`

// All stock positions without year filter (allowed_years empty).
const stockPositionsAllSQL = `
SELECT nm_id, chrt_id, warehouse_id,
       SUM(quantity + COALESCE(in_way_from_client, 0)) AS stock_qty
FROM stocks_daily_warehouses
WHERE snapshot_date = ?
GROUP BY nm_id, chrt_id, warehouse_id
`

// Total unique sizes per nm_id from card_sizes (complete size reference).
const totalSizesFromCardsSQL = `
SELECT nm_id, COUNT(*) AS total_sizes
FROM card_sizes
WHERE nm_id IN (%s)
GROUP BY nm_id
`

const totalSizesFromCardsAllSQL = `
SELECT nm_id, COUNT(*) AS total_sizes
FROM card_sizes
GROUP BY nm_id
`

// Sizes with stock > threshold: raw rows for deduplication in Go.
// Go code maps warehouse_id → FO and counts DISTINCT chrt_id per (nm_id, FO)
// to avoid double-counting when same chrt_id appears in multiple warehouses.
const sizesInStockSQL = `
SELECT nm_id, chrt_id, warehouse_id
FROM stocks_daily_warehouses
WHERE snapshot_date = ?
  AND (quantity + COALESCE(in_way_from_client, 0)) > ?
  AND nm_id IN (%s)
GROUP BY nm_id, chrt_id, warehouse_id
`

// Sizes with stock > threshold without year filter.
const sizesInStockAllSQL = `
SELECT nm_id, chrt_id, warehouse_id
FROM stocks_daily_warehouses
WHERE snapshot_date = ?
  AND (quantity + COALESCE(in_way_from_client, 0)) > ?
GROUP BY nm_id, chrt_id, warehouse_id
`

// Daily sales per (nm_id, barcode, office_name) for regional MA computation.
// office_name from sales maps to region_name via warehouse region map.
const dailySalesByRegionSQL = `
SELECT nm_id, barcode, office_name, date(sale_dt) AS d,
       SUM(CASE WHEN doc_type_name = 'Продажа' THEN quantity ELSE 0 END) AS sold
FROM sales
WHERE date(sale_dt) >= date(?, '-29 days')
  AND date(sale_dt) <= ?
  AND is_cancel = 0
  AND nm_id IN (%s)
GROUP BY nm_id, barcode, office_name, date(sale_dt)
`

const dailySalesByRegionAllSQL = `
SELECT nm_id, barcode, office_name, date(sale_dt) AS d,
       SUM(CASE WHEN doc_type_name = 'Продажа' THEN quantity ELSE 0 END) AS sold
FROM sales
WHERE date(sale_dt) >= date(?, '-29 days')
  AND date(sale_dt) <= ?
  AND is_cancel = 0
GROUP BY nm_id, barcode, office_name, date(sale_dt)
`

// Card sizes mapping: chrt_id → (nm_id, tech_size, barcode from skus_json).
const cardSizesSQL = `
SELECT chrt_id, nm_id, tech_size, skus_json
FROM card_sizes
`

// Product attributes via 3-table JOIN.
// Uses cards table (populated by download-wb-cards) instead of products (populated by funnel downloaders).
const productAttrsSQL = `
SELECT
    p.nm_id,
    COALESCE(o.article, '')           AS article,
    COALESCE(pm.identifier, o.article, '') AS identifier,
    COALESCE(p.vendor_code, '')       AS vendor_code,
    COALESCE(o.name, '')              AS name,
    COALESCE(o.brand, '')             AS brand,
    COALESCE(o.type, '')              AS type,
    COALESCE(o.category, '')          AS category,
    COALESCE(o.category_level1, '')   AS category_level1,
    COALESCE(o.category_level2, '')   AS category_level2,
    COALESCE(o.sex, '')               AS sex,
    COALESCE(o.season, '')            AS season,
    COALESCE(o.color, '')             AS color,
    COALESCE(o.collection, '')        AS collection
FROM cards p
LEFT JOIN onec_goods o  ON o.article = p.vendor_code
LEFT JOIN pim_goods pm  ON pm.identifier = p.vendor_code
WHERE p.nm_id IN (%s)
`

// Vendor codes for year filtering.
// Uses cards table (populated by download-wb-cards) instead of products (populated by funnel downloaders).
const vendorCodesSQL = `
SELECT DISTINCT nm_id, vendor_code
FROM cards
WHERE nm_id IN (%s)
`

const vendorCodesAllSQL = `
SELECT DISTINCT nm_id, vendor_code
FROM cards
`

// Incoming supply per barcode from active (non-completed) supplies.
// quantity - ready_for_sale_quantity = units not yet reflected in stock.
// status_id 5 = completed, excluded because those are already in stock.
const supplyIncomingSQL = `
SELECT sg.barcode,
       SUM(sg.quantity) - SUM(sg.ready_for_sale_quantity) AS incoming
FROM supply_goods sg
JOIN supplies s ON s.supply_id = sg.supply_id AND s.preorder_id = sg.preorder_id
WHERE s.status_id NOT IN (5)
GROUP BY sg.barcode
HAVING incoming > 0
`

// Warehouse addresses for FO mapping from wb_warehouses.
// Returns id for direct JOIN with stocks_daily_warehouses.warehouse_id.
const warehouseAddressesSQL = `
SELECT id, name, address FROM wb_warehouses
`

// SourceRepo provides read-only access to wb-sales.db.
type SourceRepo struct {
	db *sql.DB
}

// NewSourceRepo opens the source database in read-only mode.
func NewSourceRepo(dbPath string) (*SourceRepo, error) {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open source db: %w", err)
	}
	return &SourceRepo{db: db}, nil
}

// QueryStockPositions returns stock data grouped by (nm_id, chrt_id, fo_name).
// Uses warehouse_id from stocks, resolved to FO via warehouseIDFOmap.
func (r *SourceRepo) QueryStockPositions(ctx context.Context, date string, nmIDs []int, warehouseIDFOmap map[int]string) (map[StockKey]StockInfo, error) {
	var query string
	var args []any

	if len(nmIDs) > 0 {
		ph, a := placeholders(nmIDs)
		query = fmt.Sprintf(stockPositionsSQL, ph)
		args = append([]any{date}, a...)
	} else {
		query = stockPositionsAllSQL
		args = []any{date}
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query stock positions: %w", err)
	}
	defer rows.Close()

	result := make(map[StockKey]StockInfo)
	var unmappedWh int
	for rows.Next() {
		var nmID int
		var chrtID int64
		var whID int
		var qty int64
		if err := rows.Scan(&nmID, &chrtID, &whID, &qty); err != nil {
			return nil, fmt.Errorf("scan stock position: %w", err)
		}

		fo, ok := warehouseIDFOmap[whID]
		if !ok {
			unmappedWh++
			continue
		}

		key := StockKey{NmID: nmID, ChrtID: chrtID, RegionName: fo}
		info := result[key]
		info.StockQty += qty
		result[key] = info
	}
	if unmappedWh > 0 {
		fmt.Printf("  (skipped %d stock rows: unmapped warehouse_id)\n", unmappedWh)
	}
	return result, rows.Err()
}

// QueryTotalSizes returns total unique sizes per nm_id from card_sizes.
func (r *SourceRepo) QueryTotalSizes(ctx context.Context, nmIDs []int) (map[int]int, error) {
	var query string
	var args []any

	if len(nmIDs) > 0 {
		ph, a := placeholders(nmIDs)
		query = fmt.Sprintf(totalSizesFromCardsSQL, ph)
		args = a
	} else {
		query = totalSizesFromCardsAllSQL
		args = nil
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query total sizes: %w", err)
	}
	defer rows.Close()

	result := make(map[int]int)
	for rows.Next() {
		var nmID, total int
		if err := rows.Scan(&nmID, &total); err != nil {
			return nil, fmt.Errorf("scan total sizes: %w", err)
		}
		result[nmID] = total
	}
	return result, rows.Err()
}

// QuerySizesInStock returns count of distinct sizes with stock > threshold per (nm_id, fo_name).
// Deduplicates chrt_id across warehouses within the same FO to prevent fill_pct > 100%.
func (r *SourceRepo) QuerySizesInStock(ctx context.Context, date string, threshold int, nmIDs []int, warehouseIDFOmap map[int]string) (map[SizeRegionKey]int, error) {
	var query string
	var args []any

	if len(nmIDs) > 0 {
		ph, a := placeholders(nmIDs)
		query = fmt.Sprintf(sizesInStockSQL, ph)
		args = append([]any{date, threshold}, a...)
	} else {
		query = sizesInStockAllSQL
		args = []any{date, threshold}
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query sizes in stock: %w", err)
	}
	defer rows.Close()

	type dedupKey struct {
		NmID       int
		RegionName string
		ChrtID     int64
	}
	seen := make(map[dedupKey]bool)
	for rows.Next() {
		var nmID int
		var chrtID int64
		var whID int
		if err := rows.Scan(&nmID, &chrtID, &whID); err != nil {
			return nil, fmt.Errorf("scan sizes in stock: %w", err)
		}

		fo, ok := warehouseIDFOmap[whID]
		if !ok {
			continue
		}

		seen[dedupKey{NmID: nmID, RegionName: fo, ChrtID: chrtID}] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := make(map[SizeRegionKey]int, len(seen))
	for dk := range seen {
		result[SizeRegionKey{NmID: dk.NmID, RegionName: dk.RegionName}]++
	}
	return result, nil
}

// QueryDailySalesByRegion returns daily sales per (nm_id, chrt_id, region, date) for 29-day window.
// Maps office_name → region_name via warehouseRegionMap, then barcode → chrt_id via barcodeToChrt.
// Returns: nm_id → chrt_id → region_name → date → sold.
// Sales with unmapped office_name are skipped (MA will be N/A for those positions).
func (r *SourceRepo) QueryDailySalesByRegion(
	ctx context.Context,
	refDate string,
	nmIDs []int,
	warehouseRegionMap map[string]string,
	barcodeToChrt map[string]int64,
) (map[int]map[int64]map[string]map[string]int, error) {
	var query string
	var args []any

	if len(nmIDs) > 0 {
		ph, a := placeholders(nmIDs)
		query = fmt.Sprintf(dailySalesByRegionSQL, ph)
		args = append([]any{refDate, refDate}, a...)
	} else {
		query = dailySalesByRegionAllSQL
		args = []any{refDate, refDate}
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query daily sales by region: %w", err)
	}
	defer rows.Close()

	// nm_id → chrt_id → region_name → date → sold
	result := make(map[int]map[int64]map[string]map[string]int)
	var skippedOffice, skippedBarcode int

	for rows.Next() {
		var nmID int
		var barcode, officeName, d string
		var sold int
		if err := rows.Scan(&nmID, &barcode, &officeName, &d, &sold); err != nil {
			return nil, fmt.Errorf("scan daily sales: %w", err)
		}

		// Map office_name → region_name
		region := MatchOfficeToRegion(officeName, warehouseRegionMap)
		if region == "" {
			skippedOffice += sold
			continue
		}

		// Map barcode → chrt_id
		chrtID, ok := barcodeToChrt[barcode]
		if !ok {
			skippedBarcode += sold
			continue
		}

		if result[nmID] == nil {
			result[nmID] = make(map[int64]map[string]map[string]int)
		}
		if result[nmID][chrtID] == nil {
			result[nmID][chrtID] = make(map[string]map[string]int)
		}
		if result[nmID][chrtID][region] == nil {
			result[nmID][chrtID][region] = make(map[string]int)
		}
		result[nmID][chrtID][region][d] += sold
	}
	if skippedOffice > 0 {
		fmt.Printf("  (skipped %d sold units: unmapped office_name)\n", skippedOffice)
	}
	if skippedBarcode > 0 {
		fmt.Printf("  (skipped %d sold units: unmapped barcode)\n", skippedBarcode)
	}
	return result, rows.Err()
}

// QueryWarehouseFOMaps returns warehouse_id → fo_name and warehouse_name → fo_name mappings.
// Parses addresses from wb_warehouses (primary source), then enriches from stock warehouse names
// for IDs not present in wb_warehouses (FBS warehouses, small/closed warehouses).
// byID is used for stock queries (warehouse_id from stocks_daily_warehouses).
// byName is used for sales queries (office_name from sales table).
func (r *SourceRepo) QueryWarehouseFOMaps(ctx context.Context, date string) (map[int]string, map[string]string, error) {
	// Step 1: Parse wb_warehouses addresses → build byID and byName maps
	rows, err := r.db.QueryContext(ctx, warehouseAddressesSQL)
	if err != nil {
		return nil, nil, fmt.Errorf("query warehouse addresses: %w", err)
	}
	defer rows.Close()

	byID := make(map[int]string)
	byName := make(map[string]string)

	for rows.Next() {
		var id int
		var name, address string
		if err := rows.Scan(&id, &name, &address); err != nil {
			return nil, nil, fmt.Errorf("scan warehouse address: %w", err)
		}
		fo := parseAddressToFO(address, name)
		if fo != "" {
			byID[id] = fo
			byName[name] = fo
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	// Step 2: Enrich from stock warehouse names for IDs not in wb_warehouses.
	// Some warehouses (FBS seller warehouses, small warehouses) exist in stocks
	// but not in wb_warehouses. Parse their names directly for FO mapping.
	stockRows, err := r.db.QueryContext(ctx,
		`SELECT DISTINCT warehouse_id, warehouse_name FROM stocks_daily_warehouses WHERE snapshot_date = ?`, date)
	if err != nil {
		return nil, nil, fmt.Errorf("query stock warehouse names: %w", err)
	}
	defer stockRows.Close()

	var enriched int
	for stockRows.Next() {
		var whID int
		var whName string
		if err := stockRows.Scan(&whID, &whName); err != nil {
			return nil, nil, fmt.Errorf("scan stock warehouse: %w", err)
		}

		// Already mapped from wb_warehouses? Ensure name variant is in byName.
		// Stock and wb names can differ for the same warehouse_id
		// (e.g., "Кемерово" vs "СЦ Кемерово", "Краснодар" vs "Краснодар (Тихорецкая)").
		if fo, ok := byID[whID]; ok {
			if _, nameOk := byName[whName]; !nameOk {
				byName[whName] = fo
			}
			continue
		}

		// ID not in wb_warehouses — try parsing warehouse name directly
		fo := parseAddressToFO("", whName)
		if fo != "" {
			byID[whID] = fo
			byName[whName] = fo
			enriched++
		}
	}
	if err := stockRows.Err(); err != nil {
		return nil, nil, err
	}
	if enriched > 0 {
		fmt.Printf("  (enriched %d warehouses from stock names)\n", enriched)
	}

	return byID, byName, nil
}

// MatchOfficeToRegion maps a sales office_name to region_name using the warehouse map.
// Pipeline: exact match → substring match (office contained in warehouse or vice versa).
// Returns empty string if no match found.
func MatchOfficeToRegion(officeName string, warehouseMap map[string]string) string {
	if officeName == "" {
		return ""
	}

	// 1. Exact match
	if region, ok := warehouseMap[officeName]; ok {
		return region
	}

	// 2. Substring match: office_name contained in warehouse_name
	for whName, region := range warehouseMap {
		if strings.Contains(whName, officeName) || strings.Contains(officeName, whName) {
			return region
		}
	}

	return ""
}

// CardSizeEntry maps chrt_id to nm_id, tech_size, and barcodes (from skus_json).
type CardSizeEntry struct {
	ChrtID    int64
	NmID      int
	TechSize  string
	Barcodes  []string // all barcodes from skus_json
}

// QueryCardSizes returns all card_sizes entries with parsed barcodes.
func (r *SourceRepo) QueryCardSizes(ctx context.Context) ([]CardSizeEntry, error) {
	rows, err := r.db.QueryContext(ctx, cardSizesSQL)
	if err != nil {
		return nil, fmt.Errorf("query card sizes: %w", err)
	}
	defer rows.Close()

	var result []CardSizeEntry
	for rows.Next() {
		var chrtID int64
		var nmID int
		var techSize, skusJSON string
		if err := rows.Scan(&chrtID, &nmID, &techSize, &skusJSON); err != nil {
			return nil, fmt.Errorf("scan card sizes: %w", err)
		}

		barcodes := parseAllBarcodes(skusJSON)
		if len(barcodes) == 0 {
			continue
		}

		result = append(result, CardSizeEntry{
			ChrtID:   chrtID,
			NmID:     nmID,
			TechSize: techSize,
			Barcodes: barcodes,
		})
	}
	return result, rows.Err()
}

// QueryProductAttrs returns product attributes for the given nm_ids.
func (r *SourceRepo) QueryProductAttrs(ctx context.Context, nmIDs []int) (map[int]*SKURow, error) {
	if len(nmIDs) == 0 {
		return nil, nil
	}

	ph, args := placeholders(nmIDs)
	query := fmt.Sprintf(productAttrsSQL, ph)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query product attrs: %w", err)
	}
	defer rows.Close()

	result := make(map[int]*SKURow, len(nmIDs))
	for rows.Next() {
		var a SKURow
		if err := rows.Scan(
			&a.NmID, &a.Article, &a.Identifier, &a.VendorCode,
			&a.Name, &a.Brand, &a.Type, &a.Category, &a.CategoryLevel1, &a.CategoryLevel2,
			&a.Sex, &a.Season, &a.Color, &a.Collection,
		); err != nil {
			return nil, fmt.Errorf("scan product attrs: %w", err)
		}
		result[a.NmID] = &a
	}
	return result, rows.Err()
}

// QueryVendorCodes returns all (nm_id, vendor_code) pairs.
func (r *SourceRepo) QueryVendorCodes(ctx context.Context, nmIDs []int) ([]config.YearEntry, error) {
	var query string
	var args []any

	if len(nmIDs) > 0 {
		ph, a := placeholders(nmIDs)
		query = fmt.Sprintf(vendorCodesSQL, ph)
		args = a
	} else {
		query = vendorCodesAllSQL
		args = nil
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query vendor codes: %w", err)
	}
	defer rows.Close()

	var result []config.YearEntry
	for rows.Next() {
		var e config.YearEntry
		if err := rows.Scan(&e.NmID, &e.VendorCode); err != nil {
			return nil, fmt.Errorf("scan vendor codes: %w", err)
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// Close closes the source database.
func (r *SourceRepo) Close() error {
	return r.db.Close()
}

// QuerySupplyIncoming returns incoming stock per barcode from active supplies.
// Returns map of barcode → incoming units (quantity - ready_for_sale_quantity).
func (r *SourceRepo) QuerySupplyIncoming(ctx context.Context) (map[string]int64, error) {
	rows, err := r.db.QueryContext(ctx, supplyIncomingSQL)
	if err != nil {
		return nil, fmt.Errorf("query supply incoming: %w", err)
	}
	defer rows.Close()

	result := make(map[string]int64)
	for rows.Next() {
		var barcode string
		var incoming int64
		if err := rows.Scan(&barcode, &incoming); err != nil {
			return nil, fmt.Errorf("scan supply incoming: %w", err)
		}
		result[barcode] = incoming
	}
	return result, rows.Err()
}

// parseAllBarcodes extracts all barcodes from skus_json array.
// skus_json format: ["4630047636342"] or ["barcode1","barcode2"]
func parseAllBarcodes(skusJSON string) []string {
	if skusJSON == "" || skusJSON == "[]" {
		return nil
	}
	var barcodes []string
	if err := json.Unmarshal([]byte(skusJSON), &barcodes); err != nil {
		return nil
	}
	return barcodes
}

// placeholders generates comma-separated "?" placeholders and args for nmIDs.
func placeholders(nmIDs []int) (string, []any) {
	ph := make([]string, len(nmIDs))
	args := make([]any, len(nmIDs))
	for i, id := range nmIDs {
		ph[i] = "?"
		args[i] = id
	}
	return strings.Join(ph, ","), args
}
