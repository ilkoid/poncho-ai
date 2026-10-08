package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// sizeAvailDailyTableSQL — снапшот-таблица «доступность размеров WB ↔ остаток 1С».
//
// ANALYTICAL-слой (dev_data_layers.md): производная от RAW-источников
// stocks_daily_warehouses / card_sizes / cards / onec_goods / onec_rests,
// собирается утилитой cmd/data-analyzers/size-availability-report после
// основной загрузки сырых данных. Grain: (snapshot_date, article).
//
// snapshot_date — TEXT 'YYYY-MM-DD' (конвенция репо: лексикографический
// максимум = последний день, как onec_rests/stocks_daily_warehouses).
const sizeAvailDailyTableSQL = `
-- ============================================================================
-- SIZE AVAILABILITY DAILY (доступность размеров WB vs остаток 1С, ANALYTICAL)
-- Пишет: cmd/data-analyzers/size-availability-report (после загрузки сырья).
-- Grain: один артикул 1С на один день-снапшот стоков WB.
-- Universe: артикулы со свободным остатком на складе 1С (конфиг onec_storage).
-- ============================================================================
CREATE TABLE IF NOT EXISTS analytical.size_availability_daily (
    snapshot_date   TEXT    NOT NULL,             -- день снапшота стоков WB
    article         TEXT    NOT NULL,             -- артикул продавца (onec_goods.article)
    nm_id           BIGINT,                       -- карточка WB; NULL = карточки нет
    brand           TEXT    NOT NULL DEFAULT '',
    category_l1     TEXT    NOT NULL DEFAULT '',  -- category_level1_name с fallback на category
    category_l2     TEXT    NOT NULL DEFAULT '',
    season          TEXT    NOT NULL DEFAULT '',
    year_of_issue   INTEGER NOT NULL DEFAULT 0,   -- 20XX по символам 2-3 артикула; 0 = не определился
    sizes_total     INTEGER NOT NULL DEFAULT 0,   -- размеров в карточке (card_sizes)
    sizes_presented INTEGER NOT NULL DEFAULT 0,   -- размеров с остатком WB > порога утилиты
    wb_qty_total    BIGINT  NOT NULL DEFAULT 0,   -- суммарный остаток на складах WB, шт
    onec_free       BIGINT  NOT NULL DEFAULT 0,   -- свободный остаток 1С (склад из конфига), шт
    onec_reserv     BIGINT  NOT NULL DEFAULT 0,   -- резерв 1С того же склада, шт
    has_wb_card     BOOLEAN NOT NULL DEFAULT FALSE,
    unavailable     BOOLEAN NOT NULL DEFAULT FALSE, -- представлено < порога размеров (нет карточки → TRUE)
    updated_at      TEXT    NOT NULL DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    PRIMARY KEY (snapshot_date, article)
);
`

// sizeAvailSizesTableSQL — пер-размерная детализация для drill-down «артикул →
// размеры». Включает и размеры с нулевым остатком (полная сетка карточки):
// COALESCE по стокам WB, чтобы «никогда не завозился» отличался от «1 шт».
const sizeAvailSizesTableSQL = `
-- ============================================================================
-- SIZE AVAILABILITY SIZES DAILY (размеры карточек из universe-артикулов)
-- Grain: (snapshot_date, nm_id, chrt_id) — один размер карточки.
-- ============================================================================
CREATE TABLE IF NOT EXISTS analytical.size_availability_sizes_daily (
    snapshot_date TEXT   NOT NULL,
    nm_id         BIGINT NOT NULL,
    chrt_id       BIGINT NOT NULL,
    article       TEXT   NOT NULL DEFAULT '',
    tech_size     TEXT   NOT NULL DEFAULT '',
    wb_qty        BIGINT NOT NULL DEFAULT 0,      -- остаток на складах WB, шт (0 = нет строк в стоках)
    is_presented  BOOLEAN NOT NULL DEFAULT FALSE, -- wb_qty > порога утилиты
    PRIMARY KEY (snapshot_date, nm_id, chrt_id)
);
`

const sizeAvailSizesIndexSQL = `
CREATE INDEX IF NOT EXISTS idx_size_availability_sizes_daily_nm
    ON analytical.size_availability_sizes_daily (snapshot_date, nm_id);
`

// InitSizeAvailabilitySchema создаёт схему analytical (если ещё нет) и обе
// таблицы доступности, затем выдаёт read-only права (доктрина dev_data_layers.md).
// Каждый DDL — отдельный Exec: pgx не парсит multi-statement блоки с логикой
// (гатч pgx.Exec, AGENTS.md).
func InitSizeAvailabilitySchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, analyticalSchemaSQL); err != nil {
		return fmt.Errorf("analytical schema: %w", err)
	}
	if _, err := pool.Exec(ctx, sizeAvailDailyTableSQL); err != nil {
		return fmt.Errorf("size_availability_daily: %w", err)
	}
	if _, err := pool.Exec(ctx, sizeAvailSizesTableSQL); err != nil {
		return fmt.Errorf("size_availability_sizes_daily: %w", err)
	}
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_size_availability_daily_date ON analytical.size_availability_daily (snapshot_date)`,
		`CREATE INDEX IF NOT EXISTS idx_size_availability_daily_nm ON analytical.size_availability_daily (snapshot_date, nm_id)`,
		`CREATE INDEX IF NOT EXISTS idx_size_availability_sizes_daily_nm ON analytical.size_availability_sizes_daily (snapshot_date, nm_id)`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("size_availability index: %w", err)
		}
	}
	return grantAnalyticalReadonly(ctx, pool)
}
