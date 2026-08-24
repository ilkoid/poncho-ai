// stock-snapshot-report — отчёт по самому свежему срезу остатков на складах WB.
//
// Что делает:
//  1. Определяет самую свежую дату snapshot_date в stocks_daily_warehouses (главный источник — гранулярность nm_id + chrt_id/SKU + склад).
//  2. Срез по nm_id: количество товаров и штук на каждом складе, денежная оценка (по ценам из product_prices — три варианта).
//  3. Срез по SKU (chrt_id, размер): то же самое, но в разрезе каждой размерной SKU.
//
// Источники (PG-схемы в pkg/storage/postgres/):
//   - stocks_daily_warehouses   — гранулярный остаток (snapshot_date, nm_id, chrt_id, warehouse_id, warehouse_name, quantity, in_way_*).
//   - stock_products            — агрегат от WB (snapshot_date, nm_id, stock_count, stock_sum — собственная оценка WB).
//   - product_prices            — цены (snapshot_date, nm_id, price — до скидки, discounted_price — со скидкой, club_discounted_price — со скидкой по клубу).
//   - card_sizes, cards         — справочники: chrt_id → tech_size/wb_size, nm_id → vendor_code/subject_name/brand_name.
//
// Денежная оценка рассчитывается локально: quantity × цена из product_prices за ту же дату (или ближайщую свежую).
// Поля помечаются явно: «по базовой цене (до скидки)», «по цене со скидкой», «по клубной цене».
// Для сравнения рядом выводится собственная оценка WB (stock_sum из stock_products) — она даётся без расшифровки типа цены.
//
// Конфигурация — стандартная pkg/config.V2StorageConfig (поддержка PGHOST/PGPORT/PGUSER/PGDATABASE/PG_PWD).
//
// Правила из dev_manifest.md / AGENTS.md:
//   - cmd/ = только orchestration (rule 6);
//   - read-only анализатор (data-analyzers категория);
//   - context propagation для отмены (rule 11).
package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

// ----------------------------------------------------------------------------
// Config
// ----------------------------------------------------------------------------

// Config утилиты — только storage-блок (как у v2 downloaders).
type Config struct {
	Storage config.V2StorageConfig `yaml:"storage"`
}

// ----------------------------------------------------------------------------
// CLI
// ----------------------------------------------------------------------------

func main() {
	var (
		configPath = flag.String("config", "", "путь к YAML (storage-блок как у v2 downloader'ов); если пусто — берём PGHOST/PGPORT/PGUSER/PGDATABASE/PG_PWD из окружения")
		outDir     = flag.String("out", "reports", "куда складывать отчёты (default: reports)")
		database   = flag.String("database", "", "переопределить имя PG-базы (override $PGDATABASE, default wb_data_prod)")
		date       = flag.String("date", "", "конкретная дата среза YYYY-MM-DD (default: самая свежая в базе)")
	)
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 1. Резолвим DSN.
	dsn, dbLabel, err := resolveDSN(*configPath, *database)
	if err != nil {
		log.Fatalf("DSN: %v", err)
	}

	// 2. Пул соединений.
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("connect %s: %v", dbLabel, err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("ping %s: %v", dbLabel, err)
	}
	fmt.Printf("✓ Connected to %s\n", dbLabel)

	// 3. Определяем дату среза.
	snapshotDate, err := resolveSnapshotDate(ctx, pool, *date)
	if err != nil {
		log.Fatalf("snapshot date: %v", err)
	}
	fmt.Printf("✓ Snapshot date: %s\n", snapshotDate)

	// 4. Поднимаем справочник цен (для денежной оценки).
	prices, err := loadPrices(ctx, pool, snapshotDate)
	if err != nil {
		log.Fatalf("load prices: %v", err)
	}
	fmt.Printf("✓ Prices for %d nm_ids (snapshot %s)\n", len(prices.m), prices.date)

	// 5. Поднимаем справочник nm → артик/предмет/бренд.
	cards, err := loadCards(ctx, pool)
	if err != nil {
		log.Fatalf("load cards: %v", err)
	}
	fmt.Printf("✓ Cards dictionary: %d nm_ids\n", len(cards))

	// 5b. Справочник chrt_id → tech_size/wb_size для SKU-отчёта.
	sizes, err := loadCardSizes(ctx, pool)
	if err != nil {
		fmt.Printf("⚠ card_sizes: %v (в SKU-отчёте размеры будут пустыми)\n", err)
		sizes = map[int64]sizeInfo{}
	}
	fmt.Printf("✓ card_sizes: %d SKU\n", len(sizes))

	// 6. Срез stocks_daily_warehouses за выбранную дату.
	rows, err := loadStockRows(ctx, pool, snapshotDate)
	if err != nil {
		log.Fatalf("load stocks: %v", err)
	}
	fmt.Printf("✓ stocks_daily_warehouses: %d строк за %s\n", len(rows), snapshotDate)

	// 7. Достаём собственный агрегат stock_products от WB (для контроля).
	spByNm, err := loadStockProducts(ctx, pool, snapshotDate)
	if err != nil {
		// Не критично — продолжаем без него.
		fmt.Printf("⚠ stock_products за %s: %v (продолжаем без него)\n", snapshotDate, err)
	}
	if len(spByNm) > 0 {
		fmt.Printf("✓ stock_products: %d nm_ids за %s\n", len(spByNm), snapshotDate)
	}

	// 8. Готовим отчёты.
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatalf("mkdir %s: %v", *outDir, err)
	}

	stamp := time.Now().Format("2006-01-02_1504")
	ts := time.Now().Format("2006-01-02 15:04:05 MST")

	// 8a. По складам (сводка).
	byWarehouse := aggregateByWarehouse(rows)
	// 8b. По nm_id.
	byNm := aggregateByNmID(rows, prices, cards)
	// 8c. По SKU (chrt_id, размер).
	bySKU := aggregateBySKU(rows, prices, cards, sizes)
	// 8d. Собственная оценка WB.
	wbByNm := wbStockProductsByNm(spByNm)

	writeSummaryCSV(filepath.Join(*outDir, "stocks-"+snapshotDate+"__by-warehouse.csv"), byWarehouse.headers, byWarehouse.rows)
	writeSummaryCSV(filepath.Join(*outDir, "stocks-"+snapshotDate+"__by-nm.csv"), byNm.headers, byNm.rows)
	writeSummaryCSV(filepath.Join(*outDir, "stocks-"+snapshotDate+"__by-sku.csv"), bySKU.headers, bySKU.rows)
	if len(wbByNm.rows) > 0 {
		writeSummaryCSV(filepath.Join(*outDir, "stocks-"+snapshotDate+"__wb-stock_products.csv"), wbByNm.headers, wbByNm.rows)
	}
	mdPath := filepath.Join(*outDir, "stocks-"+snapshotDate+"__report.md")
	writeMarkdown(mdPath, ts, snapshotDate, dbLabel, prices.date, rows, byWarehouse, byNm, bySKU, spByNm)

	fmt.Println()
	fmt.Println("══════════════════════════════════════════════════════════════════")
	fmt.Printf("Готово. Файлы (%s / %s):\n", *outDir, stamp)
	fmt.Printf("  • stocks-%s__by-warehouse.csv  — сводка по складам\n", snapshotDate)
	fmt.Printf("  • stocks-%s__by-nm.csv         — в разрезе nm_id (артикул)\n", snapshotDate)
	fmt.Printf("  • stocks-%s__by-sku.csv        — в разрезе SKU/размер (chrt_id)\n", snapshotDate)
	if len(wbByNm.rows) > 0 {
		fmt.Printf("  • stocks-%s__wb-stock_products.csv — собственная оценка WB\n", snapshotDate)
	}
	fmt.Printf("  • stocks-%s__report.md         — читаемый сводный отчёт\n", snapshotDate)
	fmt.Println("══════════════════════════════════════════════════════════════════")
}

// ----------------------------------------------------------------------------
// DSN resolution
// ----------------------------------------------------------------------------

func resolveDSN(configPath, databaseOverride string) (dsn, label string, err error) {
	var cfg Config
	if configPath != "" {
		if err := config.LoadYAML(configPath, &cfg); err != nil {
			return "", "", fmt.Errorf("load yaml %s: %w", configPath, err)
		}
	}
	if cfg.Storage.Backend == "" {
		cfg.Storage.Backend = "postgres"
	}
	if databaseOverride != "" {
		cfg.Storage.PgDatabase = databaseOverride
	}
	// GetDefaults применит $PGDATABASE override.
	d, err := cfg.Storage.GetEffectiveDSN()
	if err != nil {
		return "", "", err
	}
	return d, cfg.Storage.DisplayDB(), nil
}

// ----------------------------------------------------------------------------
// Snapshot date resolution
// ----------------------------------------------------------------------------

func resolveSnapshotDate(ctx context.Context, pool *pgxpool.Pool, requested string) (string, error) {
	if requested != "" {
		// Проверяем, что данные за эту дату есть.
		var n int64
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM stocks_daily_warehouses WHERE snapshot_date = $1`, requested,
		).Scan(&n); err != nil {
			return "", fmt.Errorf("check date %s: %w", requested, err)
		}
		if n == 0 {
			// Покажем несколько свежих доступных дат. MAX() использует Index Only Scan
			// (idx_stocks_date) за миллисекунды, тогда как GROUP BY+ORDER BY тут дорог
			// на ~10M строк. Берём MAX наибыстрейший; для списка дат — отдельный путь.
			avail, _ := recentDates(ctx, pool, 8)
			return "", fmt.Errorf("за %s нет данных (0 строк). Свежие даты в базе: %s",
				requested, strings.Join(avail, ", "))
		}
		return requested, nil
	}

	// Самая свежая дата — MAX() использует Index Only Scan по idx_stocks_date.
	var d string
	err := pool.QueryRow(ctx,
		`SELECT MAX(snapshot_date) FROM stocks_daily_warehouses`,
	).Scan(&d)
	if err != nil {
		return "", fmt.Errorf("найти свежий snapshot_date: %w", err)
	}
	return d, nil
}

// recentDates возвращает N последних дат среза.
// Использует MAX-итерацию по idx_stocks_date (быстрее GROUP BY+ORDER BY на ~10M строк).
func recentDates(ctx context.Context, pool *pgxpool.Pool, n int) ([]string, error) {
	var out []string
	for i := 0; i < n; i++ {
		var d string
		var pred string
		if len(out) > 0 {
			pred = " WHERE snapshot_date < $1"
		}
		q := "SELECT MAX(snapshot_date) FROM stocks_daily_warehouses" + pred
		var err error
		if len(out) > 0 {
			err = pool.QueryRow(ctx, q, out[len(out)-1]).Scan(&d)
		} else {
			err = pool.QueryRow(ctx, q).Scan(&d)
		}
		if err != nil || d == "" {
			break
		}
		out = append(out, d)
	}
	return out, nil
}

// ----------------------------------------------------------------------------
// Data types
// ----------------------------------------------------------------------------

type stockRow struct {
	nmID          int64
	chrtID        int64
	warehouseID   int64
	warehouseName string
	regionName    string
	quantity      int64
	inWayTo       int64
	inWayFrom     int64
}

type cardInfo struct {
	vendorCode  string
	subjectName string
	brandName   string
}

type sizeInfo struct {
	techSize string
	wbSize   string
}

type priceInfo struct {
	base              float64 // price — РРЦ, до скидки
	discounted        float64 // discounted_price — со скидкой
	clubDiscounted    float64 // club_discounted_price — клубная цена
}

type priceTable struct {
	date string
	m    map[int64]priceInfo
}

type stockProductRow struct {
	nmID       int64
	stockCount int64
	stockSum   float64
}

// ----------------------------------------------------------------------------
// Loaders
// ----------------------------------------------------------------------------

func loadStockRows(ctx context.Context, pool *pgxpool.Pool, snapshotDate string) ([]stockRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT nm_id, chrt_id, warehouse_id,
		       COALESCE(NULLIF(warehouse_name, ''), 'ID='||warehouse_id::text) AS warehouse_name,
		       COALESCE(NULLIF(region_name, ''), '') AS region_name,
		       quantity, in_way_to_client, in_way_from_client
		FROM stocks_daily_warehouses
		WHERE snapshot_date = $1
		ORDER BY nm_id, chrt_id, warehouse_id`, snapshotDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []stockRow
	for rows.Next() {
		var r stockRow
		if err := rows.Scan(&r.nmID, &r.chrtID, &r.warehouseID, &r.warehouseName,
			&r.regionName, &r.quantity, &r.inWayTo, &r.inWayFrom); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func loadPrices(ctx context.Context, pool *pgxpool.Pool, snapshotDate string) (priceTable, error) {
	// Берём последнюю дату цен <= snapshot_date (цены — ежедневный снимок; если на дату нет — ближайшая свежая в прошлом).
	// MAX() использует Index Only Scan по первичному ключу (nm_id, snapshot_date).
	var priceDate string
	err := pool.QueryRow(ctx,
		`SELECT MAX(snapshot_date) FROM product_prices WHERE snapshot_date <= $1`,
		snapshotDate,
	).Scan(&priceDate)
	if err != nil || priceDate == "" {
		// Может быть пусто — попробуем ближайшую вперёд (лучше что-то, чем ничего).
		if err2 := pool.QueryRow(ctx,
			`SELECT MIN(snapshot_date) FROM product_prices`,
		).Scan(&priceDate); err2 != nil || priceDate == "" {
			// Цен вообще нет — отчёт без денежной оценки.
			return priceTable{date: "—", m: map[int64]priceInfo{}}, nil
		}
	}

	rows, err := pool.Query(ctx, `
		SELECT nm_id, price, discounted_price, club_discounted_price
		FROM product_prices WHERE snapshot_date = $1`, priceDate)
	if err != nil {
		return priceTable{}, err
	}
	defer rows.Close()

	m := make(map[int64]priceInfo)
	for rows.Next() {
		var nm int64
		var p priceInfo
		if err := rows.Scan(&nm, &p.base, &p.discounted, &p.clubDiscounted); err != nil {
			return priceTable{}, err
		}
		m[nm] = p
	}
	return priceTable{date: priceDate, m: m}, rows.Err()
}

func loadCards(ctx context.Context, pool *pgxpool.Pool) (map[int64]cardInfo, error) {
	rows, err := pool.Query(ctx, `
		SELECT nm_id,
		       COALESCE(NULLIF(vendor_code, ''), '') AS vendor_code,
		       COALESCE(NULLIF(subject_name, ''), '') AS subject_name,
		       COALESCE(NULLIF(brand, ''), '') AS brand
		FROM cards`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[int64]cardInfo)
	for rows.Next() {
		var nm int64
		var c cardInfo
		if err := rows.Scan(&nm, &c.vendorCode, &c.subjectName, &c.brandName); err != nil {
			return nil, err
		}
		m[nm] = c
	}
	return m, rows.Err()
}

func loadCardSizes(ctx context.Context, pool *pgxpool.Pool) (map[int64]sizeInfo, error) {
	rows, err := pool.Query(ctx, `
		SELECT chrt_id,
		       COALESCE(NULLIF(tech_size, ''), '') AS tech_size,
		       COALESCE(NULLIF(wb_size, ''), '') AS wb_size
		FROM card_sizes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[int64]sizeInfo)
	for rows.Next() {
		var chrt int64
		var s sizeInfo
		if err := rows.Scan(&chrt, &s.techSize, &s.wbSize); err != nil {
			return nil, err
		}
		m[chrt] = s
	}
	return m, rows.Err()
}

func loadStockProducts(ctx context.Context, pool *pgxpool.Pool, snapshotDate string) (map[int64]stockProductRow, error) {
	// Берём ближайшую дату <= snapshot_date.
	var spDate string
	err := pool.QueryRow(ctx,
		`SELECT MAX(snapshot_date) FROM stock_products WHERE snapshot_date <= $1`,
		snapshotDate,
	).Scan(&spDate)
	if err != nil || spDate == "" {
		// Попробуем любую свежую.
		if err2 := pool.QueryRow(ctx,
			`SELECT MAX(snapshot_date) FROM stock_products`,
		).Scan(&spDate); err2 != nil || spDate == "" {
			return nil, fmt.Errorf("нет данных в stock_products")
		}
	}

	rows, err := pool.Query(ctx, `
		SELECT nm_id, stock_count, stock_sum
		FROM stock_products WHERE snapshot_date = $1`, spDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[int64]stockProductRow)
	for rows.Next() {
		var r stockProductRow
		if err := rows.Scan(&r.nmID, &r.stockCount, &r.stockSum); err != nil {
			return nil, err
		}
		m[r.nmID] = r
	}
	return m, rows.Err()
}

// ----------------------------------------------------------------------------
// Aggregations
// ----------------------------------------------------------------------------

type csvBlock struct {
	headers []string
	rows    [][]string
}

type whAgg struct {
	whID   int64
	whName string
	region string
	qty    int64
	inTo   int64
	inFrom int64
}

func aggregateByWarehouse(rows []stockRow) csvBlock {
	m := map[int64]*whAgg{}
	for _, r := range rows {
		a, ok := m[r.warehouseID]
		if !ok {
			a = &whAgg{whID: r.warehouseID, whName: r.warehouseName, region: r.regionName}
			m[r.warehouseID] = a
		}
		a.qty += r.quantity
		a.inTo += r.inWayTo
		a.inFrom += r.inWayFrom
	}

	out := csvBlock{
		headers: []string{"warehouse_id", "warehouse_name", "region", "items_qty", "in_way_to_client", "in_way_from_client"},
	}
	// Соберём для сортировки.
	keys := make([]int64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Сортировка по убыванию остатка.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && m[keys[j]].qty > m[keys[j-1]].qty; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	for _, k := range keys {
		a := m[k]
		out.rows = append(out.rows, []string{
			strconv.FormatInt(a.whID, 10), a.whName, a.region,
			strconv.FormatInt(a.qty, 10),
			strconv.FormatInt(a.inTo, 10),
			strconv.FormatInt(a.inFrom, 10),
		})
	}
	return out
}

type nmAgg struct {
	nmID         int64
	qty          int64
	inTo         int64
	inFrom       int64
	whCount      int64
	skuCount     int64
	sumBase      float64
	sumDiscount  float64
	sumClub      float64
	hasPrice     bool
}

func aggregateByNmID(rows []stockRow, prices priceTable, cards map[int64]cardInfo) csvBlock {
	m := map[int64]*nmAgg{}
	skus := map[int64]map[int64]struct{}{}
	for _, r := range rows {
		a, ok := m[r.nmID]
		if !ok {
			a = &nmAgg{nmID: r.nmID}
			m[r.nmID] = a
		}
		a.qty += r.quantity
		a.inTo += r.inWayTo
		a.inFrom += r.inWayFrom
		a.whCount++ // одна строка = одна позиция (nm, sku, wh) → не уникальные склады, но для оценки присутствия ок
		if _, ok := skus[r.nmID]; !ok {
			skus[r.nmID] = map[int64]struct{}{}
		}
		skus[r.nmID][r.chrtID] = struct{}{}

		if p, ok := prices.m[r.nmID]; ok {
			a.sumBase += float64(r.quantity) * p.base
			a.sumDiscount += float64(r.quantity) * p.discounted
			a.sumClub += float64(r.quantity) * p.clubDiscounted
			a.hasPrice = true
		}
	}

	out := csvBlock{
		headers: []string{
			"nm_id", "vendor_code", "subject_name", "brand_name",
			"sku_count", "warehouse_positions",
			"items_qty", "in_way_to_client", "in_way_from_client",
			"money_base_price_RUB", "money_discounted_price_RUB", "money_club_price_RUB",
			"price_date", "price_note",
		},
	}
	keys := make([]int64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Сортировка по убыванию денежной оценки (base price), затем по остатку.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0; j-- {
			cur, prev := m[keys[j]], m[keys[j-1]]
			if cur.sumBase > prev.sumBase || (cur.sumBase == prev.sumBase && cur.qty > prev.qty) {
				keys[j], keys[j-1] = keys[j-1], keys[j]
			} else {
				break
			}
		}
	}
	for _, k := range keys {
		a := m[k]
		c := cards[k]
		note := "нет цены в product_prices — денежная оценка 0"
		priceDate := "—"
		if a.hasPrice {
			priceDate = prices.date
			note = "базовая=до скидки (price); discounted=со скидкой; club=клубная цена"
		}
		out.rows = append(out.rows, []string{
			strconv.FormatInt(a.nmID, 10),
			c.vendorCode, c.subjectName, c.brandName,
			strconv.FormatInt(int64(len(skus[k])), 10),
			strconv.FormatInt(a.whCount, 10),
			strconv.FormatInt(a.qty, 10),
			strconv.FormatInt(a.inTo, 10),
			strconv.FormatInt(a.inFrom, 10),
			fmtRub(a.sumBase), fmtRub(a.sumDiscount), fmtRub(a.sumClub),
			priceDate, note,
		})
	}
	return out
}

type skuAgg struct {
	nmID         int64
	chrtID       int64
	qty          int64
	inTo         int64
	inFrom       int64
	whCount      int64
	sumBase      float64
	sumDiscount  float64
	sumClub      float64
	hasPrice     bool
}

func aggregateBySKU(rows []stockRow, prices priceTable, cards map[int64]cardInfo, sizes map[int64]sizeInfo) csvBlock {
	m := map[int64]*skuAgg{} // key = chrtID
	whPerSKU := map[int64]map[int64]struct{}{}
	for _, r := range rows {
		a, ok := m[r.chrtID]
		if !ok {
			a = &skuAgg{nmID: r.nmID, chrtID: r.chrtID}
			m[r.chrtID] = a
			whPerSKU[r.chrtID] = map[int64]struct{}{}
		}
		a.qty += r.quantity
		a.inTo += r.inWayTo
		a.inFrom += r.inWayFrom
		whPerSKU[r.chrtID][r.warehouseID] = struct{}{}

		if p, ok := prices.m[r.nmID]; ok {
			a.sumBase += float64(r.quantity) * p.base
			a.sumDiscount += float64(r.quantity) * p.discounted
			a.sumClub += float64(r.quantity) * p.clubDiscounted
			a.hasPrice = true
		}
	}

	// Подтянем tech_size/wb_size из card_sizes.
	out := csvBlock{
		headers: []string{
			"nm_id", "vendor_code", "subject_name", "brand_name",
			"chrt_id", "tech_size", "wb_size",
			"warehouse_positions", "items_qty", "in_way_to_client", "in_way_from_client",
			"money_base_price_RUB", "money_discounted_price_RUB", "money_club_price_RUB",
			"price_date", "price_note",
		},
	}
	keys := make([]int64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Сортировка по nm_id, затем chrt_id — читаемый порядок артикулов.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0; j-- {
			cur, prev := m[keys[j]], m[keys[j-1]]
			if cur.nmID < prev.nmID || (cur.nmID == prev.nmID && cur.chrtID < prev.chrtID) {
				keys[j], keys[j-1] = keys[j-1], keys[j]
			} else {
				break
			}
		}
	}
	for _, k := range keys {
		a := m[k]
		c := cards[a.nmID]
		s := sizes[k]
		note := "нет цены в product_prices — денежная оценка 0"
		priceDate := "—"
		if a.hasPrice {
			priceDate = prices.date
			note = "базовая=до скидки; discounted=со скидкой; club=клубная цена"
		}
		out.rows = append(out.rows, []string{
			strconv.FormatInt(a.nmID, 10),
			c.vendorCode, c.subjectName, c.brandName,
			strconv.FormatInt(a.chrtID, 10),
			s.techSize, s.wbSize,
			strconv.FormatInt(int64(len(whPerSKU[k])), 10),
			strconv.FormatInt(a.qty, 10),
			strconv.FormatInt(a.inTo, 10),
			strconv.FormatInt(a.inFrom, 10),
			fmtRub(a.sumBase), fmtRub(a.sumDiscount), fmtRub(a.sumClub),
			priceDate, note,
		})
	}
	return out
}

func wbStockProductsByNm(sp map[int64]stockProductRow) csvBlock {
	if len(sp) == 0 {
		return csvBlock{}
	}
	out := csvBlock{
		headers: []string{"nm_id", "wb_stock_count", "wb_stock_sum_RUB", "note"},
	}
	for nm, r := range sp {
		out.rows = append(out.rows, []string{
			strconv.FormatInt(nm, 10),
			strconv.FormatInt(r.stockCount, 10),
			fmtRub(r.stockSum),
			"собственная оценка WB из stock_products.stock_sum (тип цены WB не раскрывает)",
		})
	}
	return out
}

// ----------------------------------------------------------------------------
// Output writers
// ----------------------------------------------------------------------------

func writeSummaryCSV(path string, headers []string, rows [][]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	// BOM для корректной кириллицы в Excel.
	if _, err := f.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}
	w := csv.NewWriter(f)
	w.Comma = ';'
	if err := w.Write(headers); err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.Write(r); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func writeMarkdown(path, generatedAt, snapshotDate, dbLabel, priceDate string,
	allRows []stockRow, byWh, byNm, bySKU csvBlock, sp map[int64]stockProductRow) error {

	var b strings.Builder
	fmt.Fprintf(&b, "# Отчёт по остаткам на складах WB\n\n")
	fmt.Fprintf(&b, "- **Дата среза (snapshot_date):** `%s`\n", snapshotDate)
	fmt.Fprintf(&b, "- **База данных:** `%s`\n", dbLabel)
	fmt.Fprintf(&b, "- **Сгенерировано:** %s\n", generatedAt)
	fmt.Fprintf(&b, "- **Источник остатков:** `stocks_daily_warehouses` (гранулярность nm_id + chrt_id + warehouse_id)\n")
	fmt.Fprintf(&b, "- **Источник цен:** `product_prices` за `%s` (для денежной оценки)\n", priceDate)
	fmt.Fprintln(&b, "")
	fmt.Fprintln(&b, "## Про денежную оценку")
	fmt.Fprintln(&b, "")
	fmt.Fprintln(&b, "Денежная оценка считается локально: `quantity × цена`. Три варианта цены:")
	fmt.Fprintln(&b, "- `money_base_price_RUB` — по **базовой цене** (`product_prices.price`) — это РРЦ, **до скидки**.")
	fmt.Fprintln(&b, "- `money_discounted_price_RUB` — по **цене со скидкой** (`product_prices.discounted_price`) — то, что платит покупатель.")
	fmt.Fprintln(&b, "- `money_club_price_RUB` — по **клубной цене** (`product_prices.club_discounted_price`) — цена для подписчиков WB Club.")
	fmt.Fprintln(&b, "")
	fmt.Fprintln(&b, "Для сверки рядом — отдельный CSV `__wb-stock_products.csv` с собственной оценкой WB (`stock_products.stock_sum`).")
	fmt.Fprintln(&b, "WB не раскрывает, по какой именно цене считается `stock_sum`, поэтому воспринимать её как контрольное число, а не эталон.")
	fmt.Fprintln(&b, "")
	fmt.Fprintln(&b, "## Важные нюансы данных")
	fmt.Fprintln(&b, "")
	fmt.Fprintln(&b, "- **«Остальные» (warehouse_id=0)** — это служебная категория WB, не физический склад. У неё всегда `quantity=0`, но `in_way_to_client`/`in_way_from_client` там реальные — это товар в пути, который WB не разложил по конкретным складам. Не вычитать из остатков.")
	fmt.Fprintln(&b, "- **Склады с `quantity=0`, но ненулевым `in_way_*`** — нормально для WB: это транзитный товар (едет к клиенту или возвращается).")
	fmt.Fprintln(&b, "- **Клубная цена (`club_discounted_price`)** в этом срезе оказалась равна `discounted_price` (цена со скидкой) — WB отдаёт одинаковое значение, если по карточке нет отдельной клубной акции. Колонку оставил для полноты.")
	fmt.Fprintln(&b, "- **`chrt_id` = SKU = chartID** — идентификатор размерной SKU. Один `nm_id` может иметь несколько размеров → несколько `chrt_id`. Соответствие `chrt_id → tech_size/wb_size` лежит в таблице `card_sizes` (проставлено в SKU-отчёте).")
	fmt.Fprintln(&b, "")
	fmt.Fprintln(&b, "## Итоги по срезу")
	fmt.Fprintln(&b, "")

	var totalQty, totalInTo, totalInFrom int64
	for _, r := range allRows {
		totalQty += r.quantity
		totalInTo += r.inWayTo
		totalInFrom += r.inWayFrom
	}
	nmSet := map[int64]struct{}{}
	skuSet := map[int64]struct{}{}
	whSet := map[int64]struct{}{}
	var baseSum, discSum, clubSum float64
	for _, r := range allRows {
		nmSet[r.nmID] = struct{}{}
		skuSet[r.chrtID] = struct{}{}
		whSet[r.warehouseID] = struct{}{}
	}
	// Денежные итоги — из уже посчитанных byNm.
	for _, row := range byNm.rows {
		// Колонки: nm_id(0), vc, subj, brand, sku, wh, qty, into, infrom, base, disc, club, pdate, pnote
		if len(row) >= 12 {
			baseSum += parseRub(row[9])
			discSum += parseRub(row[10])
			clubSum += parseRub(row[11])
		}
	}

	fmt.Fprintf(&b, "| Метрика | Значение |\n|---|---:|\n")
	fmt.Fprintf(&b, "| Уникальных nm_id (артикулов) | %d |\n", len(nmSet))
	fmt.Fprintf(&b, "| Уникальных SKU (chrt_id, размеры) | %d |\n", len(skuSet))
	fmt.Fprintf(&b, "| Уникальных складов | %d |\n", len(whSet))
	fmt.Fprintf(&b, "| Строк в срезе | %d |\n", len(allRows))
	fmt.Fprintf(&b, "| **Всего штук на складах** | **%s** |\n", formatInt(totalQty))
	fmt.Fprintf(&b, "| В пути к клиенту | %s |\n", formatInt(totalInTo))
	fmt.Fprintf(&b, "| В пути от клиента | %s |\n", formatInt(totalInFrom))
	fmt.Fprintf(&b, "| Σ денег по базовой цене (до скидки) | **%s ₽** |\n", fmtRub(baseSum))
	fmt.Fprintf(&b, "| Σ денег по цене со скидкой | %s ₽ |\n", fmtRub(discSum))
	fmt.Fprintf(&b, "| Σ денег по клубной цене | %s ₽ |\n", fmtRub(clubSum))
	fmt.Fprintln(&b, "")
	fmt.Fprintln(&b, "## Сводка по складам")
	fmt.Fprintln(&b, "")
	fmt.Fprintln(&b, "| warehouse_id | Склад | Регион | Штук | В пути к клиенту | В пути от клиента |")
	fmt.Fprintln(&b, "|---:|---|---|---:|---:|---:|")
	for _, row := range byWh.rows {
		if len(row) < 6 {
			continue
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n",
			row[0], mdEscape(row[1]), mdEscape(row[2]), row[3], row[4], row[5])
	}
	fmt.Fprintln(&b, "")
	fmt.Fprintln(&b, "## Топ-20 артикулов (nm_id) по деньгам (базовая цена, до скидки)")
	fmt.Fprintln(&b, "")
	fmt.Fprintln(&b, "| nm_id | Артикул продавца | Предмет | Бренд | SKU | Штук | Σ базовая | Σ со скидкой |")
	fmt.Fprintln(&b, "|---:|---|---|---|---:|---:|---:|---:|")
	topN := 20
	if len(byNm.rows) < topN {
		topN = len(byNm.rows)
	}
	for i := 0; i < topN; i++ {
		row := byNm.rows[i]
		// 0:nm 1:vc 2:subj 3:brand 4:sku 5:wh 6:qty 7:inTo 8:inFrom 9:base 10:disc 11:club
		if len(row) < 12 {
			continue
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s |\n",
			row[0], mdEscape(row[1]), mdEscape(row[2]), mdEscape(row[3]), row[4], row[6], row[9], row[10])
	}
	fmt.Fprintln(&b, "")
	fmt.Fprintf(&b, "_Полный список — в `stocks-%s__by-nm.csv` (открыть в Excel/LibreOffice, разделитель `;`)._\n\n", snapshotDate)

	fmt.Fprintln(&b, "## Файлы")
	fmt.Fprintln(&b, "")
	fmt.Fprintf(&b, "- `stocks-%s__by-warehouse.csv` — сводка по складам (1 строка = 1 склад).\n", snapshotDate)
	fmt.Fprintf(&b, "- `stocks-%s__by-nm.csv` — в разрезе артикула (1 строка = 1 nm_id).\n", snapshotDate)
	fmt.Fprintf(&b, "- `stocks-%s__by-sku.csv` — в разрезе SKU/размера (1 строка = 1 chrt_id).\n", snapshotDate)
	if len(sp) > 0 {
		fmt.Fprintf(&b, "- `stocks-%s__wb-stock_products.csv` — собственная оценка остатков от WB (для сверки).\n", snapshotDate)
	}
	fmt.Fprintln(&b, "")

	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// ----------------------------------------------------------------------------
// Formatting helpers
// ----------------------------------------------------------------------------

func fmtRub(v float64) string {
	// Две цифры после запятой, разделитель тысяч — неразрывный пробел не используем (CSV дружелюбно).
	return strconv.FormatFloat(math.Round(v*100)/100, 'f', 2, 64)
}

func parseRub(s string) float64 {
	v, _ := strconv.ParseFloat(strings.ReplaceAll(s, " ", ""), 64)
	return v
}

func formatInt(v int64) string {
	return strconv.FormatInt(v, 10)
}

func mdEscape(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	if s == "" {
		return "—"
	}
	return s
}
