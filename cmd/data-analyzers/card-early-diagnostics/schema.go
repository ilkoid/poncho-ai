// schema.go — DDL аналитической таблицы card-early-diagnostics.
//
// Таблица analytical.card_health_daily — единственная сущность утилиты: ежедневный
// срез карточек, удовлетворяющих критериям гендира (вотчлист). Никаких статусов
// и рекомендаций — только анализ. Каждый DDL отдельным Exec (конвенция initFSASchema:
// pgx не парсит несколько операторов в одном Exec).
package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const createSchemaSQL = `CREATE SCHEMA IF NOT EXISTS analytical`

const createTableSQL = `
CREATE TABLE IF NOT EXISTS analytical.card_health_daily (
    snapshot_date TEXT NOT NULL,
    nm_id BIGINT NOT NULL,

    -- А. Идентификация (фильтры xlsx)
    category_1c TEXT NOT NULL DEFAULT '',
    subject_wb TEXT NOT NULL DEFAULT '',
    article_1c TEXT NOT NULL DEFAULT '',
    title_wb TEXT NOT NULL DEFAULT '',
    season_1c TEXT NOT NULL DEFAULT '',

    -- Б. Показывают продажи
    sales_start_date TEXT,               -- первый день остатка>0 (каскад), NULL = не начались
    anchor_source TEXT NOT NULL DEFAULT '', -- stocks | stock_products | funnel_open | cards_created
    full_days_no_orders INTEGER NOT NULL DEFAULT 0, -- полных дней без заказов (сегодня не считается)
    signal TEXT NOT NULL DEFAULT '',     -- slow_start | stall | pre_sales
    last_order_date TEXT,                -- MAX(orders.order_date), NULL = заказов не было никогда
    orders_total BIGINT NOT NULL DEFAULT 0, -- заказов за жизнь (is_cancel=false) — критерий разреза
    opens_7d BIGINT NOT NULL DEFAULT 0,  -- открытия карточки, окно funnel_window_days
    carts_7d BIGINT NOT NULL DEFAULT 0,  -- добавления в корзину, то же окно

    -- В. Влияют на продажи
    is_shadowed BOOLEAN,                 -- WB скрыл из каталога; NULL = нет данных item_ratings
    card_rating DOUBLE PRECISION,        -- рейтинг карточки WB 0-10 (products); NULL = нет строки
    title_len INTEGER NOT NULL DEFAULT 0,
    description_len INTEGER NOT NULL DEFAULT 0, -- длина описания, символы (0 = описания нет)
    name_im_1c TEXT NOT NULL DEFAULT '', -- наименование 1С — образец для исправления
    photo_count INTEGER NOT NULL DEFAULT 0,
    has_video BOOLEAN NOT NULL DEFAULT false, -- у карточки есть видео (cards.video непуст)
    char_count INTEGER NOT NULL DEFAULT 0,
    wb_stock_total BIGINT,              -- суммарный остаток по всем размерам (каскад)
    sizes_wb_in_stock INTEGER,           -- размеров с qty>0 на WB; NULL = нет размерных данных
    sizes_1c_fbo INTEGER,                -- размеров с free>0 на 1С-складе каскада
    fbo_storage_used TEXT NOT NULL DEFAULT '',
    sizes_ratio DOUBLE PRECISION,        -- sizes_wb_in_stock / sizes_1c_fbo; NULL = не посчитать
    stock_availability TEXT NOT NULL DEFAULT '', -- nonLiquid | balanced | invalidData | ''
    campaigns_active INTEGER NOT NULL DEFAULT 0,

    -- Г. Итог
    top_hypothesis TEXT NOT NULL DEFAULT '',

    -- Служебное
    updated_at TEXT NOT NULL DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),

    PRIMARY KEY (snapshot_date, nm_id)
)`

const idxNmSQL = `CREATE INDEX IF NOT EXISTS idx_card_health_daily_nm ON analytical.card_health_daily(nm_id)`

const idxDaysSQL = `CREATE INDEX IF NOT EXISTS idx_card_health_daily_days
    ON analytical.card_health_daily(snapshot_date, full_days_no_orders)`

// migrateOrdersTotal — эволюция схемы: колонка заказов за жизнь (появилась после
// первой версии таблицы; IF NOT EXISTS делает миграцию идемпотентной).
const migrateOrdersTotal = `ALTER TABLE analytical.card_health_daily
    ADD COLUMN IF NOT EXISTS orders_total BIGINT NOT NULL DEFAULT 0`

// migrateContentLen — эволюция схемы: длина описания + наличие видео
// (контентные метрики; каждый DDL отдельным Exec — конвенция pgx).
const migrateDescriptionLen = `ALTER TABLE analytical.card_health_daily
    ADD COLUMN IF NOT EXISTS description_len INTEGER NOT NULL DEFAULT 0`

const migrateHasVideo = `ALTER TABLE analytical.card_health_daily
    ADD COLUMN IF NOT EXISTS has_video BOOLEAN NOT NULL DEFAULT false`

// ensureSchema создаёт схему и таблицу, если их нет.
func ensureSchema(ctx context.Context, db *pgxpool.Pool) error {
	stmts := []struct {
		name string
		sql  string
	}{
		{"schema", createSchemaSQL},
		{"table", createTableSQL},
		{"idx_nm", idxNmSQL},
		{"idx_days", idxDaysSQL},
		{"migrate_orders_total", migrateOrdersTotal},
		{"migrate_description_len", migrateDescriptionLen},
		{"migrate_has_video", migrateHasVideo},
	}
	for _, s := range stmts {
		if _, err := db.Exec(ctx, s.sql); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	return nil
}

// deleteSnapshotSQL — идемпотентность: перед вставкой срез дня пересобирается целиком
// (upsert не удаляет карточки, вышедшие из вотчлиста, — остались бы призраками дня).
const deleteSnapshotSQL = `DELETE FROM analytical.card_health_daily WHERE snapshot_date = $1`

// insertRowSQL — upsert одной строки среза (ON CONFLICT по PK дня).
const insertRowSQL = `
INSERT INTO analytical.card_health_daily (
    snapshot_date, nm_id,
    category_1c, subject_wb, article_1c, title_wb, season_1c,
    sales_start_date, anchor_source, full_days_no_orders, signal, last_order_date,
    orders_total, opens_7d, carts_7d,
    is_shadowed, card_rating, title_len, description_len, name_im_1c,
    photo_count, has_video, char_count,
    wb_stock_total, sizes_wb_in_stock, sizes_1c_fbo, fbo_storage_used, sizes_ratio,
    stock_availability, campaigns_active,
    top_hypothesis, updated_at
) VALUES (
    $1,$2,
    $3,$4,$5,$6,$7,
    $8,$9,$10,$11,$12,
    $13,$14,$15,
    $16,$17,$18,$19,$20,
    $21,$22,$23,
    $24,$25,$26,$27,$28,
    $29,$30,
    $31, TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
)
ON CONFLICT (snapshot_date, nm_id) DO UPDATE SET
    category_1c = EXCLUDED.category_1c, subject_wb = EXCLUDED.subject_wb,
    article_1c = EXCLUDED.article_1c, title_wb = EXCLUDED.title_wb,
    season_1c = EXCLUDED.season_1c,
    sales_start_date = EXCLUDED.sales_start_date, anchor_source = EXCLUDED.anchor_source,
    full_days_no_orders = EXCLUDED.full_days_no_orders, signal = EXCLUDED.signal,
    last_order_date = EXCLUDED.last_order_date,
    orders_total = EXCLUDED.orders_total,
    opens_7d = EXCLUDED.opens_7d, carts_7d = EXCLUDED.carts_7d,
    is_shadowed = EXCLUDED.is_shadowed, card_rating = EXCLUDED.card_rating,
    title_len = EXCLUDED.title_len, description_len = EXCLUDED.description_len,
    name_im_1c = EXCLUDED.name_im_1c,
    photo_count = EXCLUDED.photo_count, has_video = EXCLUDED.has_video,
    char_count = EXCLUDED.char_count,
    wb_stock_total = EXCLUDED.wb_stock_total, sizes_wb_in_stock = EXCLUDED.sizes_wb_in_stock,
    sizes_1c_fbo = EXCLUDED.sizes_1c_fbo, fbo_storage_used = EXCLUDED.fbo_storage_used,
    sizes_ratio = EXCLUDED.sizes_ratio, stock_availability = EXCLUDED.stock_availability,
    campaigns_active = EXCLUDED.campaigns_active,
    top_hypothesis = EXCLUDED.top_hypothesis,
    updated_at = EXCLUDED.updated_at`

// trimRetentionSQL — срезы старше N дней (0 = не чистить).
const trimRetentionSQL = `DELETE FROM analytical.card_health_daily WHERE snapshot_date < $1`
