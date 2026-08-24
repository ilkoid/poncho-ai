package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FBS-задания: сборочные задания FBS + текущие статусы + журнал статусов + лента заказов.
//
// Источники (локальный сваггер):
//   - GET  /api/v3/orders              — docs/wb_api_swagger/03-orders-fbs.yaml:463
//   - POST /api/v3/orders/status       — docs/wb_api_swagger/03-orders-fbs.yaml:548
//   - POST /api/analytics/v1/order-feed — docs/wb_api_swagger/11-analytics.yaml:1682
//
// Особенность: таблицы fbs_orders / fbs_orders_status существуют в проде с 2026-08-16
// как разовый TEXT-снапшот (import-fbs-snapshot.sh). Схема ниже мигрирует их на месте
// (TEXT → TIMESTAMPTZ, новые колонки) с сохранением данных; на чистой БД создаёт сразу
// правильные типы. Имена колонок существующих таблиц не меняются — анализатор
// cmd/data-analyzers/fbs-orders-report продолжает работать без правок.
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

	// fbsOrdersMigrations приводит существующие TEXT-таблицы разового снапшота
	// (2026-08-16) к каноническим типам. Идемпотентно: на свежей БД это no-op.
	// NULLIF(...,'') — защита от пустых строк (в проде их нет, проверено 2026-08-23).
	fbsOrdersTypeMigrations = `
ALTER TABLE fbs_orders ALTER COLUMN created_at TYPE TIMESTAMPTZ USING NULLIF(created_at, '')::timestamptz;
ALTER TABLE fbs_orders ALTER COLUMN downloaded_at DROP DEFAULT;
ALTER TABLE fbs_orders ALTER COLUMN downloaded_at TYPE TIMESTAMPTZ USING NULLIF(downloaded_at, '')::timestamptz;
ALTER TABLE fbs_orders ALTER COLUMN downloaded_at SET DEFAULT now();
ALTER TABLE fbs_orders_status ALTER COLUMN downloaded_at DROP DEFAULT;
ALTER TABLE fbs_orders_status ALTER COLUMN downloaded_at TYPE TIMESTAMPTZ USING NULLIF(downloaded_at, '')::timestamptz;
ALTER TABLE fbs_orders_status ALTER COLUMN downloaded_at SET DEFAULT now();
`

	fbsOrdersColumnMigrations = `
ALTER TABLE fbs_orders ADD COLUMN IF NOT EXISTS converted_price BIGINT NOT NULL DEFAULT 0;
ALTER TABLE fbs_orders ADD COLUMN IF NOT EXISTS currency_code INTEGER NOT NULL DEFAULT 0;
ALTER TABLE fbs_orders ADD COLUMN IF NOT EXISTS converted_currency_code INTEGER NOT NULL DEFAULT 0;
ALTER TABLE fbs_orders ADD COLUMN IF NOT EXISTS cargo_type SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE fbs_orders ADD COLUMN IF NOT EXISTS cross_border_type SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE fbs_orders ADD COLUMN IF NOT EXISTS scan_price BIGINT NOT NULL DEFAULT 0;
ALTER TABLE fbs_orders ADD COLUMN IF NOT EXISTS is_b2b BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE fbs_orders ADD COLUMN IF NOT EXISTS barcode TEXT NOT NULL DEFAULT '';
`

	// fbsStatusLogBackfill переносит статусы разового снапшота в журнал, чтобы
	// история до появления загрузчика не потерялась (first_seen = downloaded_at снапшота).
	// Выполняется один раз: пока журнал пуст.
	fbsStatusLogBackfillSQL = `
INSERT INTO fbs_orders_status_log (order_id, supplier_status, wb_status, is_cancellable, first_seen, last_seen)
SELECT s.order_id, s.supplier_status, s.wb_status, s.is_cancellable, s.downloaded_at, s.downloaded_at
FROM fbs_orders_status s
WHERE NOT EXISTS (SELECT 1 FROM fbs_orders_status_log)
ON CONFLICT (order_id, supplier_status, wb_status) DO NOTHING;
`
)

// initFBSOrdersSchema создаёт/мигрирует таблицы FBS-заданий.
func initFBSOrdersSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, fbsOrdersSchemaSQL); err != nil {
		return fmt.Errorf("fbs orders schema: %w", err)
	}
	if _, err := pool.Exec(ctx, fbsOrdersTypeMigrations); err != nil {
		return fmt.Errorf("fbs orders type migrations (text→timestamptz): %w", err)
	}
	if _, err := pool.Exec(ctx, fbsOrdersColumnMigrations); err != nil {
		return fmt.Errorf("fbs orders column migrations: %w", err)
	}
	if _, err := pool.Exec(ctx, fbsStatusLogBackfillSQL); err != nil {
		return fmt.Errorf("fbs status log backfill: %w", err)
	}
	return nil
}
