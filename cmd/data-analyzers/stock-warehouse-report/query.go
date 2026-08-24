// query.go — read-only SQL остатков FBO (склады WB) и структуры строк.
//
// Один запрос: для свежего среза snapshot_date достаёт гранулярные остатки
// (склад × nm_id × chrt_id × 3 типа остатков) и подтягивает справочники:
//
//	cards      — по nm_id  → vendor_code / brand / subject_name;
//	card_sizes — по chrt_id → tech_size / wb_size + skus_json со штрихкодами.
//
// LEFT JOIN (не INNER): строка остатка важнее справочника — если карточка или
// размер ещё не подтянуты, vendor_code/barcode будут пустыми, но штуки не теряются.
//
// Источник — public.stocks_daily_warehouses наполняется от endpoint'а
// /api/analytics/v1/stocks-report/wb-warehouses (swagger 11-analytics.yaml:1257),
// который возвращает именно остатки на складах WB (FBO), не склады продавца (FBS).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// StockRow — одна строка остатка: склад × nm_id × chrt_id (SKU) с тремя типами остатка.
type StockRow struct {
	WarehouseID     int64
	WarehouseName   string
	RegionName      string
	NmID            int64
	ChrtID          int64
	VendorCode      string // артикул продавца (из cards)
	Brand           string
	SubjectName     string
	TechSize        string
	WBSize          string
	SKUsJSON        string // сырой JSON-массив штрихкодов из card_sizes.skus_json
	Quantity        int64  // свободный остаток (доступен в корзину)
	InWayToClient   int64  // в пути к клиенту
	InWayFromClient int64  // в пути от клиента (возврат)
}

// Total возвращает суммарный остаток строки (для колонки «Итого»).
func (r StockRow) Total() int64 { return r.Quantity + r.InWayToClient + r.InWayFromClient }

// Barcodes парсит skus_json в строку штрихкодов через запятую.
// При ошибке парсинга или пустом массиве возвращает пустую строку — строка не теряется.
func (r StockRow) Barcodes() string {
	s := strings.TrimSpace(r.SKUsJSON)
	if s == "" || s == "[]" {
		return ""
	}
	var arr []string
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		// Не致命но — оставляем сырой JSON как есть (для дебага).
		return strings.Trim(s, "[]\"")
	}
	return strings.Join(arr, ", ")
}

// stockQuery — основной запрос: гранулярные остатки + справочники за один проход.
//
// snapshot_date хранится как TEXT в ISO-формате (YYYY-MM-DD), лексикографический
// max/сравнение корректны и используют idx_stocks_date. Сортировка по warehouse_name
// (для стабильного порядка листов) и vendor_code + chrt_id (для читаемости внутри склада).
const stockQuery = `
SELECT
  s.warehouse_id,
  COALESCE(NULLIF(s.warehouse_name,''), 'Склад ' || s.warehouse_id::text) AS warehouse_name,
  COALESCE(NULLIF(s.region_name,''), '(без региона)') AS region_name,
  s.nm_id,
  s.chrt_id,
  COALESCE(NULLIF(c.vendor_code,''), '')   AS vendor_code,
  COALESCE(NULLIF(c.brand,''), '')         AS brand,
  COALESCE(NULLIF(c.subject_name,''), '')  AS subject_name,
  COALESCE(NULLIF(cs.tech_size,''), '')    AS tech_size,
  COALESCE(NULLIF(cs.wb_size,''), '')      AS wb_size,
  COALESCE(NULLIF(cs.skus_json,''), '')    AS skus_json,
  s.quantity,
  s.in_way_to_client,
  s.in_way_from_client
FROM stocks_daily_warehouses s
LEFT JOIN cards      c  ON c.nm_id  = s.nm_id
LEFT JOIN card_sizes cs ON cs.chrt_id = s.chrt_id
WHERE s.snapshot_date = $1
ORDER BY s.warehouse_name, c.vendor_code, s.chrt_id`

// latestSnapshotDate возвращает последний доступный день среза (MAX как text).
// Использует Index Only Scan по idx_stocks_date (миллисекунды на ~10M строк).
// Возвращает ("", nil) если таблица пуста.
func latestSnapshotDate(ctx context.Context, conn *pgxpool.Pool) (string, error) {
	const q = `SELECT MAX(snapshot_date) FROM stocks_daily_warehouses`
	var d *string
	if err := conn.QueryRow(ctx, q).Scan(&d); err != nil {
		return "", fmt.Errorf("latest snapshot_date: %w", err)
	}
	if d == nil {
		return "", nil
	}
	return *d, nil
}

// snapshotExists проверяет, что срез на дату есть и непустой.
func snapshotExists(ctx context.Context, conn *pgxpool.Pool, date string) (int64, error) {
	const q = `SELECT COUNT(*) FROM stocks_daily_warehouses WHERE snapshot_date = $1`
	var n int64
	if err := conn.QueryRow(ctx, q, date).Scan(&n); err != nil {
		return 0, fmt.Errorf("snapshot count %s: %w", date, err)
	}
	return n, nil
}

// loadStocks выполняет запрос и возвращает все гранулярные строки остатков.
//
// Строки идут в порядке warehouse_name → vendor_code → chrt_id (см. ORDER BY в SQL),
// что упрощает дальнейшую группировку по складам в Go.
func loadStocks(ctx context.Context, conn *pgxpool.Pool, date string) ([]StockRow, error) {
	rows, err := conn.Query(ctx, stockQuery, date)
	if err != nil {
		return nil, fmt.Errorf("stock query: %w", err)
	}
	defer rows.Close()

	var out []StockRow
	for rows.Next() {
		var r StockRow
		if err := rows.Scan(
			&r.WarehouseID, &r.WarehouseName, &r.RegionName,
			&r.NmID, &r.ChrtID,
			&r.VendorCode, &r.Brand, &r.SubjectName,
			&r.TechSize, &r.WBSize, &r.SKUsJSON,
			&r.Quantity, &r.InWayToClient, &r.InWayFromClient,
		); err != nil {
			return nil, fmt.Errorf("stock scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// verifyTotal возвращает контрольную сумму по дате для сверки отчёта.
// Простая сумма без джойнов — независима от покрытия cards/card_sizes, поэтому
// совпадение с Σ по листам подтверждает, что группировка ничего не потеряла.
func verifyTotal(ctx context.Context, conn *pgxpool.Pool, date string) (onStock, toClient, fromClient int64, err error) {
	const q = `SELECT COALESCE(SUM(quantity),0), COALESCE(SUM(in_way_to_client),0), COALESCE(SUM(in_way_from_client),0)
	           FROM stocks_daily_warehouses WHERE snapshot_date = $1`
	err = conn.QueryRow(ctx, q, date).Scan(&onStock, &toClient, &fromClient)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("verify total: %w", err)
	}
	return onStock, toClient, fromClient, nil
}
