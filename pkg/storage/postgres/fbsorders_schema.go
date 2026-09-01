package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FBS-задания: сборочные задания FBS + текущие статусы + журнал статусов + лента заказов.
//
// Источники (локальный сваггер):
//   - GET  /api/v3/orders              — docs/wb_api_swagger/03-orders-fbs.yaml:463
//   - POST /api/v3/orders/status       — docs/wb_api_swagger/03-orders-fbs.yaml:548
//   - POST /api/analytics/v1/order-feed — docs/wb_api_swagger/11-analytics.yaml:1682
//
// Утилита начинает с нуля: каноническая DDL, миграций нет. Если в БД остались
// легаси-таблицы разового TEXT-снапшота 2026-08-16 (import-fbs-snapshot.sh),
// инициализация падает с подсказкой снять их DROP'ом перед первым прогоном.
const (
	fbsOrdersSchemaSQL = `
CREATE TABLE IF NOT EXISTS fbs_orders (
    id BIGINT PRIMARY KEY,
    rid TEXT NOT NULL DEFAULT '',
    order_uid TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    supply_id TEXT NOT NULL DEFAULT '',
    warehouse_id BIGINT NOT NULL DEFAULT 0,
    office_id BIGINT NOT NULL DEFAULT 0,
    nm_id BIGINT NOT NULL DEFAULT 0,
    article TEXT NOT NULL DEFAULT '',
    chrt_id BIGINT NOT NULL DEFAULT 0,
    price BIGINT NOT NULL DEFAULT 0,
    converted_price BIGINT NOT NULL DEFAULT 0,
    currency_code INTEGER NOT NULL DEFAULT 0,
    converted_currency_code INTEGER NOT NULL DEFAULT 0,
    cargo_type SMALLINT NOT NULL DEFAULT 0,
    cross_border_type SMALLINT NOT NULL DEFAULT 0,
    scan_price BIGINT NOT NULL DEFAULT 0,
    is_zero_order BOOLEAN NOT NULL DEFAULT false,
    is_b2b BOOLEAN NOT NULL DEFAULT false,
    barcode TEXT NOT NULL DEFAULT '',
    downloaded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_fbs_orders_created_at ON fbs_orders(created_at);
CREATE INDEX IF NOT EXISTS idx_fbs_orders_nm ON fbs_orders(nm_id);
CREATE INDEX IF NOT EXISTS idx_fbs_orders_rid ON fbs_orders(rid);
CREATE INDEX IF NOT EXISTS idx_fbs_orders_supply ON fbs_orders(supply_id);

CREATE TABLE IF NOT EXISTS fbs_orders_status (
    order_id BIGINT PRIMARY KEY,
    supplier_status TEXT NOT NULL DEFAULT '',
    wb_status TEXT NOT NULL DEFAULT '',
    is_cancellable BOOLEAN NOT NULL DEFAULT false,
    downloaded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_fbs_status_supplier ON fbs_orders_status(supplier_status);
CREATE INDEX IF NOT EXISTS idx_fbs_status_wb ON fbs_orders_status(wb_status);

-- Журнал статусов: одна строка на уникальное состояние (order_id, supplier_status, wb_status),
-- first_seen/last_seen — когда состояние впервые увидено и когда подтверждалось в последний раз.
-- Гранулярность истории = частота запусков загрузчика (сутки при daily-пайплайне).
CREATE TABLE IF NOT EXISTS fbs_orders_status_log (
    order_id BIGINT NOT NULL,
    supplier_status TEXT NOT NULL DEFAULT '',
    wb_status TEXT NOT NULL DEFAULT '',
    is_cancellable BOOLEAN NOT NULL DEFAULT false,
    first_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (order_id, supplier_status, wb_status)
);

-- Поставки FBS (GET /api/v3/supplies): полный список качается каждый прогон,
-- upsert по supply_id идемпотентен и дообновляет scan_dt/closed_at открытых.
-- scan_dt = приёмка на СЦ («дата сканирования поставки или первого заказа»);
-- closed_at = передача в доставку (в живых данных РАНЬШЕ scan_dt на ~0.5–1 ч).
CREATE TABLE IF NOT EXISTS fbs_supplies (
    supply_id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ,
    closed_at TIMESTAMPTZ,
    scan_dt TIMESTAMPTZ,
    reject_dt TIMESTAMPTZ,
    done BOOLEAN NOT NULL DEFAULT false,
    is_b2b BOOLEAN,
    cargo_type SMALLINT NOT NULL DEFAULT 0,
    cross_border_type SMALLINT,
    destination_office_id BIGINT NOT NULL DEFAULT 0,
    recommended_wh_id BIGINT NOT NULL DEFAULT 0,
    downloaded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_fbs_supplies_scan ON fbs_supplies(scan_dt);
CREATE INDEX IF NOT EXISTS idx_fbs_supplies_created ON fbs_supplies(created_at);

-- Лента заказов (analytics/v1/order-feed): статусы created/buyout/cancel/return/returnDefective,
-- причины отмен (cancel_type), география доставки, склад. is_mp = true → склад продавца (FBS/DBS).
CREATE TABLE IF NOT EXISTS order_feed (
    srid TEXT PRIMARY KEY,
    nm_id BIGINT NOT NULL DEFAULT 0,
    chrt_id BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL DEFAULT '',
    cancel_type TEXT,
    warehouse_name TEXT NOT NULL DEFAULT '',
    warehouse_region TEXT NOT NULL DEFAULT '',
    is_mp BOOLEAN NOT NULL DEFAULT false,
    destination_city TEXT NOT NULL DEFAULT '',
    destination_district TEXT NOT NULL DEFAULT '',
    seller_price DOUBLE PRECISION NOT NULL DEFAULT 0,
    is_b2b BOOLEAN NOT NULL DEFAULT false,
    downloaded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_order_feed_is_mp_updated ON order_feed(is_mp, updated_at);
CREATE INDEX IF NOT EXISTS idx_order_feed_status ON order_feed(status);
CREATE INDEX IF NOT EXISTS idx_order_feed_nm ON order_feed(nm_id);
`
)

// initFBSOrdersSchema создаёт таблицы FBS-заданий и проверяет, что они не легаси.
func initFBSOrdersSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, fbsOrdersSchemaSQL); err != nil {
		return fmt.Errorf("fbs orders schema: %w", err)
	}
	return verifyFBSSchema(ctx, pool)
}

// verifyFBSSchema fail-fast: CREATE IF NOT EXISTS молчит, если таблицы уже есть,
// а легаси-TEXT-облик разового снапшота уронит первую же запись невнятной ошибкой
// приведения типа. Проверяем несущие timestamp-колонки и даём actionable-подсказку.
func verifyFBSSchema(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx, `
		SELECT table_name || '.' || column_name, data_type
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND ( (table_name = 'fbs_orders'          AND column_name IN ('created_at', 'downloaded_at'))
		     OR (table_name = 'fbs_orders_status'   AND column_name = 'downloaded_at') )`)
	if err != nil {
		return fmt.Errorf("verify fbs schema: %w", err)
	}
	defer rows.Close()

	var legacy []string
	for rows.Next() {
		var name, dataType string
		if err := rows.Scan(&name, &dataType); err != nil {
			return fmt.Errorf("verify fbs schema: scan: %w", err)
		}
		if dataType != "timestamp with time zone" {
			legacy = append(legacy, fmt.Sprintf("%s (%s)", name, dataType))
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("verify fbs schema: %w", err)
	}
	if len(legacy) > 0 {
		return legacyFBSShapeError(legacy)
	}
	return nil
}

// legacyFBSShapeError — подсказка при обнаружении легаси-таблиц: единственное
// действие, которое принимает утилита «с нуля», — ручной DROP перед прогоном.
func legacyFBSShapeError(legacy []string) error {
	return fmt.Errorf(
		"fbs tables exist in legacy snapshot shape: %s\n"+
			"утилита начинает с нуля и не мигрирует их; перед первым прогоном выполните:\n"+
			"  DROP TABLE public.fbs_orders, public.fbs_orders_status;",
		strings.Join(legacy, ", "))
}
