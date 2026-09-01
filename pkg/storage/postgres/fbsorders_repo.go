package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/fbsorders"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: PgFBSOrdersRepo implements fbsorders.Writer.
var _ fbsorders.Writer = (*PgFBSOrdersRepo)(nil)

// PgFBSOrdersRepo implements fbsorders.Writer for PostgreSQL.
// PG-only домен (прецедент wbscraper): SQLite-адаптер не предусмотрен.
type PgFBSOrdersRepo struct {
	pool *pgxpool.Pool
}

// NewPgFBSOrdersRepo creates a new PostgreSQL FBS orders repository.
func NewPgFBSOrdersRepo(pool *pgxpool.Pool) *PgFBSOrdersRepo {
	return &PgFBSOrdersRepo{pool: pool}
}

// InitSchema создаёт и мигрирует таблицы FBS-заданий (см. fbsorders_schema.go).
func (r *PgFBSOrdersRepo) InitSchema(ctx context.Context) error {
	return initFBSOrdersSchema(ctx, r.pool)
}

const pgFBSChunkSize = 500

// ============================================================================
// SaveOrders
// ============================================================================

// SaveOrders — чанки по 500, multi-row INSERT с ON CONFLICT (id) DO UPDATE.
// Повторная загрузка того же периода идемпотентна.
func (r *PgFBSOrdersRepo) SaveOrders(ctx context.Context, orders []wb.FBSOrder) (int, error) {
	if len(orders) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(orders); i += pgFBSChunkSize {
		end := i + pgFBSChunkSize
		if end > len(orders) {
			end = len(orders)
		}
		n, err := r.saveOrdersChunk(ctx, orders[i:end])
		if err != nil {
			return 0, fmt.Errorf("save fbs orders chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

func (r *PgFBSOrdersRepo) saveOrdersChunk(ctx context.Context, chunk []wb.FBSOrder) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertFBSOrderCols)
	for _, o := range chunk {
		createdAt, err := parseFBSTime(o.CreatedAt)
		if err != nil {
			return 0, fmt.Errorf("order id=%d: %w", o.ID, err)
		}
		args = append(args,
			o.ID,
			o.RID, o.OrderUID, createdAt, o.SupplyID,
			o.WarehouseID, o.OfficeID,
			o.NmID, o.Article, o.ChrtID,
			o.Price, o.ConvertedPrice, o.CurrencyCode, o.ConvertedCurrencyCode,
			o.CargoType, o.CrossBorderType, o.ScanPrice,
			o.IsZeroOrder, o.Options.IsB2B, o.Barcode(),
		)
	}

	query := insertFBSOrderFullChunkSQL
	if len(chunk) < pgFBSChunkSize {
		query = BuildMultiRowInsert(insertFBSOrderPrefixSQL, insertFBSOrderOnConflictSQL, len(chunk), insertFBSOrderCols)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("save fbs orders batch (size %d): %w", len(chunk), err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

const (
	// $1-$20; downloaded_at использует DEFAULT now(), не плейсхолдер.
	insertFBSOrderCols = 20

	insertFBSOrderPrefixSQL = `INSERT INTO fbs_orders (
	    id,
	    rid, order_uid, created_at, supply_id,
	    warehouse_id, office_id,
	    nm_id, article, chrt_id,
	    price, converted_price, currency_code, converted_currency_code,
	    cargo_type, cross_border_type, scan_price,
	    is_zero_order, is_b2b, barcode
	) VALUES `

	insertFBSOrderOnConflictSQL = `
	ON CONFLICT (id) DO UPDATE SET
	    rid = EXCLUDED.rid,
	    order_uid = EXCLUDED.order_uid,
	    created_at = EXCLUDED.created_at,
	    supply_id = EXCLUDED.supply_id,
	    warehouse_id = EXCLUDED.warehouse_id,
	    office_id = EXCLUDED.office_id,
	    nm_id = EXCLUDED.nm_id,
	    article = EXCLUDED.article,
	    chrt_id = EXCLUDED.chrt_id,
	    price = EXCLUDED.price,
	    converted_price = EXCLUDED.converted_price,
	    currency_code = EXCLUDED.currency_code,
	    converted_currency_code = EXCLUDED.converted_currency_code,
	    cargo_type = EXCLUDED.cargo_type,
	    cross_border_type = EXCLUDED.cross_border_type,
	    scan_price = EXCLUDED.scan_price,
	    is_zero_order = EXCLUDED.is_zero_order,
	    is_b2b = EXCLUDED.is_b2b,
	    barcode = EXCLUDED.barcode`
)

var insertFBSOrderFullChunkSQL = BuildMultiRowInsert(insertFBSOrderPrefixSQL, insertFBSOrderOnConflictSQL, pgFBSChunkSize, insertFBSOrderCols)

// parseFBSTime парсит RFC3339-дату из API. Ошибка парсинга — фатальна:
// тихое пропускание строк ломало бы полноту данных.
func parseFBSTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse RFC3339 %q: %w", s, err)
	}
	return t, nil
}

// ============================================================================
// SaveStatuses — upsert последнего статуса + merge журнала, один tx на чанк
// ============================================================================

// SaveStatuses обновляет fbs_orders_status (последний статус, 1:1 по order_id)
// и вплетает состояния в журнал fbs_orders_status_log (UNIQUE-дедуп по состоянию,
// last_seen сдвигается на now при повторном подтверждении).
func (r *PgFBSOrdersRepo) SaveStatuses(ctx context.Context, statuses []wb.FBSOrderStatus) (int, error) {
	if len(statuses) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(statuses); i += pgFBSChunkSize {
		end := i + pgFBSChunkSize
		if end > len(statuses) {
			end = len(statuses)
		}
		n, err := r.saveStatusesChunk(ctx, statuses[i:end])
		if err != nil {
			return 0, fmt.Errorf("save fbs statuses chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

func (r *PgFBSOrdersRepo) saveStatusesChunk(ctx context.Context, chunk []wb.FBSOrderStatus) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertFBSStatusCols)
	for _, s := range chunk {
		args = append(args, s.ID, s.SupplierStatus, s.WbStatus, s.IsCancellable)
	}

	latestQuery := insertFBSStatusFullChunkSQL
	logQuery := insertFBSStatusLogFullChunkSQL
	if len(chunk) < pgFBSChunkSize {
		latestQuery = BuildMultiRowInsert(insertFBSStatusPrefixSQL, insertFBSStatusOnConflictSQL, len(chunk), insertFBSStatusCols)
		logQuery = BuildMultiRowInsert(insertFBSStatusLogPrefixSQL, insertFBSStatusLogOnConflictSQL, len(chunk), insertFBSStatusCols)
	}

	if _, err := tx.Exec(ctx, latestQuery, args...); err != nil {
		return 0, fmt.Errorf("upsert fbs statuses (size %d): %w", len(chunk), err)
	}
	if _, err := tx.Exec(ctx, logQuery, args...); err != nil {
		return 0, fmt.Errorf("merge fbs status log (size %d): %w", len(chunk), err)
	}
	return len(chunk), tx.Commit(ctx)
}

const (
	// $1-$4 для обеих таблиц (downloaded_at/first_seen/last_seen — DEFAULT/now()).
	insertFBSStatusCols = 4

	insertFBSStatusPrefixSQL = `INSERT INTO fbs_orders_status (
	    order_id, supplier_status, wb_status, is_cancellable
	) VALUES `

	insertFBSStatusOnConflictSQL = `
	ON CONFLICT (order_id) DO UPDATE SET
	    supplier_status = EXCLUDED.supplier_status,
	    wb_status = EXCLUDED.wb_status,
	    is_cancellable = EXCLUDED.is_cancellable,
	    downloaded_at = now()`

	insertFBSStatusLogPrefixSQL = `INSERT INTO fbs_orders_status_log (
	    order_id, supplier_status, wb_status, is_cancellable
	) VALUES `

	insertFBSStatusLogOnConflictSQL = `
	ON CONFLICT (order_id, supplier_status, wb_status) DO UPDATE SET
	    is_cancellable = EXCLUDED.is_cancellable,
	    last_seen = now()`
)

var (
	insertFBSStatusFullChunkSQL    = BuildMultiRowInsert(insertFBSStatusPrefixSQL, insertFBSStatusOnConflictSQL, pgFBSChunkSize, insertFBSStatusCols)
	insertFBSStatusLogFullChunkSQL = BuildMultiRowInsert(insertFBSStatusLogPrefixSQL, insertFBSStatusLogOnConflictSQL, pgFBSChunkSize, insertFBSStatusCols)
)

// ============================================================================
// SaveOrderFeed
// ============================================================================

// SaveOrderFeed — чанки по 500, upsert по srid (лента отдаёт текущее состояние
// заказа, повторная загрузка просто подтверждает его).
func (r *PgFBSOrdersRepo) SaveOrderFeed(ctx context.Context, items []wb.OrderFeedItem) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(items); i += pgFBSChunkSize {
		end := i + pgFBSChunkSize
		if end > len(items) {
			end = len(items)
		}
		n, err := r.saveOrderFeedChunk(ctx, items[i:end])
		if err != nil {
			return 0, fmt.Errorf("save order-feed chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

func (r *PgFBSOrdersRepo) saveOrderFeedChunk(ctx context.Context, chunk []wb.OrderFeedItem) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertOrderFeedCols)
	for _, it := range chunk {
		createdAt, err := parseFBSTime(it.CreatedAt)
		if err != nil {
			return 0, fmt.Errorf("feed srid=%s: %w", it.Srid, err)
		}
		updatedAt, err := parseFBSTime(it.UpdatedAt)
		if err != nil {
			return 0, fmt.Errorf("feed srid=%s: %w", it.Srid, err)
		}
		cancelType := (*string)(nil)
		if it.CancelType != nil {
			v := *it.CancelType
			cancelType = &v
		}
		args = append(args,
			it.Srid, it.NmID, it.ChrtID,
			createdAt, updatedAt,
			it.Status, cancelType,
			it.WarehouseName, it.WarehouseRegion, it.IsMp,
			it.DestinationCity, it.DestinationDistrict,
			it.SellerPrice, it.IsB2b,
		)
	}

	query := insertOrderFeedFullChunkSQL
	if len(chunk) < pgFBSChunkSize {
		query = BuildMultiRowInsert(insertOrderFeedPrefixSQL, insertOrderFeedOnConflictSQL, len(chunk), insertOrderFeedCols)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("save order-feed batch (size %d): %w", len(chunk), err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

const (
	// $1-$14; downloaded_at — DEFAULT now().
	insertOrderFeedCols = 14

	insertOrderFeedPrefixSQL = `INSERT INTO order_feed (
	    srid, nm_id, chrt_id,
	    created_at, updated_at,
	    status, cancel_type,
	    warehouse_name, warehouse_region, is_mp,
	    destination_city, destination_district,
	    seller_price, is_b2b
	) VALUES `

	insertOrderFeedOnConflictSQL = `
	ON CONFLICT (srid) DO UPDATE SET
	    nm_id = EXCLUDED.nm_id,
	    chrt_id = EXCLUDED.chrt_id,
	    created_at = EXCLUDED.created_at,
	    updated_at = EXCLUDED.updated_at,
	    status = EXCLUDED.status,
	    cancel_type = EXCLUDED.cancel_type,
	    warehouse_name = EXCLUDED.warehouse_name,
	    warehouse_region = EXCLUDED.warehouse_region,
	    is_mp = EXCLUDED.is_mp,
	    destination_city = EXCLUDED.destination_city,
	    destination_district = EXCLUDED.destination_district,
	    seller_price = EXCLUDED.seller_price,
	    is_b2b = EXCLUDED.is_b2b`
)

var insertOrderFeedFullChunkSQL = BuildMultiRowInsert(insertOrderFeedPrefixSQL, insertOrderFeedOnConflictSQL, pgFBSChunkSize, insertOrderFeedCols)

// ============================================================================
// SaveSupplies
// ============================================================================

// SaveSupplies — чанки по 500, upsert по supply_id. Полный список поставок
// качается каждый прогон: повторные прогоны дообновляют scan_dt/closed_at
// у поставок, которые были открыты на момент прошлого прогона.
func (r *PgFBSOrdersRepo) SaveSupplies(ctx context.Context, supplies []wb.FBSSupply) (int, error) {
	if len(supplies) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(supplies); i += pgFBSChunkSize {
		end := i + pgFBSChunkSize
		if end > len(supplies) {
			end = len(supplies)
		}
		n, err := r.saveSuppliesChunk(ctx, supplies[i:end])
		if err != nil {
			return 0, fmt.Errorf("save fbs supplies chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

func (r *PgFBSOrdersRepo) saveSuppliesChunk(ctx context.Context, chunk []wb.FBSSupply) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertFBSSupplyCols)
	for _, s := range chunk {
		createdAt, err := parseFBSTime(s.CreatedAt)
		if err != nil {
			return 0, fmt.Errorf("supply %s: %w", s.ID, err)
		}
		closedAt, err := parseFBSTimePtr(s.ClosedAt)
		if err != nil {
			return 0, fmt.Errorf("supply %s closedAt: %w", s.ID, err)
		}
		scanDt, err := parseFBSTimePtr(s.ScanDt)
		if err != nil {
			return 0, fmt.Errorf("supply %s scanDt: %w", s.ID, err)
		}
		rejectDt, err := parseFBSTimePtr(s.RejectDt)
		if err != nil {
			return 0, fmt.Errorf("supply %s rejectDt: %w", s.ID, err)
		}
		args = append(args,
			s.ID, s.Name,
			createdAt, closedAt, scanDt, rejectDt,
			s.Done, s.IsB2b, s.CargoType, s.CrossBorderType,
			s.DestinationOfficeID, s.RecommendedWhID,
		)
	}

	query := insertFBSSupplyFullChunkSQL
	if len(chunk) < pgFBSChunkSize {
		query = BuildMultiRowInsert(insertFBSSupplyPrefixSQL, insertFBSSupplyOnConflictSQL, len(chunk), insertFBSSupplyCols)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("save fbs supplies batch (size %d): %w", len(chunk), err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

const (
	// $1-$12; downloaded_at — DEFAULT now().
	insertFBSSupplyCols = 12

	insertFBSSupplyPrefixSQL = `INSERT INTO fbs_supplies (
	    supply_id, name,
	    created_at, closed_at, scan_dt, reject_dt,
	    done, is_b2b, cargo_type, cross_border_type,
	    destination_office_id, recommended_wh_id
	) VALUES `

	insertFBSSupplyOnConflictSQL = `
	ON CONFLICT (supply_id) DO UPDATE SET
	    name = EXCLUDED.name,
	    created_at = EXCLUDED.created_at,
	    closed_at = EXCLUDED.closed_at,
	    scan_dt = EXCLUDED.scan_dt,
	    reject_dt = EXCLUDED.reject_dt,
	    done = EXCLUDED.done,
	    is_b2b = EXCLUDED.is_b2b,
	    cargo_type = EXCLUDED.cargo_type,
	    cross_border_type = EXCLUDED.cross_border_type,
	    destination_office_id = EXCLUDED.destination_office_id,
	    recommended_wh_id = EXCLUDED.recommended_wh_id,
	    downloaded_at = now()`
)

var insertFBSSupplyFullChunkSQL = BuildMultiRowInsert(insertFBSSupplyPrefixSQL, insertFBSSupplyOnConflictSQL, pgFBSChunkSize, insertFBSSupplyCols)

// parseFBSTimePtr парсит nullable RFC3339-дату (null до события — поставка
// ещё не принята/не закрыта). Ошибка парсинга фатальна, как в parseFBSTime.
func parseFBSTimePtr(s *string) (*time.Time, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	t, err := parseFBSTime(*s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ============================================================================
// LoadStatusCandidateIDs — полнота статусов без пропусков
// ============================================================================

// терминальные wb_status (03-orders-fbs.yaml:589-607): дальше заказ не меняется.
const fbsTerminalWbStatuses = "('sold','canceled','canceled_by_client','declined_by_client','defect','canceled_by_carrier')"

var loadFBSStatusCandidatesSQL = `
SELECT o.id
FROM fbs_orders o
LEFT JOIN fbs_orders_status s ON s.order_id = o.id
WHERE o.created_at >= $1
   OR s.order_id IS NULL
   OR s.wb_status NOT IN ` + fbsTerminalWbStatuses + `
ORDER BY o.id`

// LoadStatusCandidateIDs возвращает ID заданий, статусы которых нужно обновить:
// всё свежее окно (created_at >= createdSince), плюс задания без статуса,
// плюс любые незакрытые заказы независимо от возраста — исключает пропуски
// переходов у «зависших» заданий за пределами окна.
func (r *PgFBSOrdersRepo) LoadStatusCandidateIDs(ctx context.Context, createdSince time.Time) ([]int64, error) {
	rows, err := r.pool.Query(ctx, loadFBSStatusCandidatesSQL, createdSince)
	if err != nil {
		return nil, fmt.Errorf("load status candidates: %w", err)
	}
	defer rows.Close()

	ids := make([]int64, 0, 1024)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan candidate id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
