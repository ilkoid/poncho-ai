package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// regionSalesDailyTableSQL — агрегат «продажи по регионам» (ANALYTICAL-слой).
//
// Замена отчёта WB «Продажи по регионам» (GET /api/v1/analytics/region-sale,
// отключается 3 ноября 2026 — release-notes id=590, 12-reports.yaml:1661).
// Собирается утилитой cmd/data-analyzers/build-region-sales-daily из уже
// скачиваемых RAW-таблиц (новый загрузчик не нужен):
//
//	public.order_feed        — город/ФО доставки, текущий статус юнита,
//	                           seller_price (11-analytics.yaml, order-feed)
//	public.orders            — регион/страна покупателя по srid (статистика)
//	public.operational_sales — деньги: price_with_disc, for_pay по srid
//	                           (статистика, sale_id 'S*'/'R*' = продажа/возврат)
//
// Grain: (stat_day, nm_id, destination_city, destination_district,
// region_name, country_name, is_mp). stat_day — МСК-дата текущего статуса
// юнита (order_feed.updated_at): лента отдаёт события «по времени текущего
// статуса» (swagger), все счётчики дня привязаны к этой семантике.
//
// Деньги — ДВЕ семантики (точного «к перечислению» старого отчёта нет ни в
// одном живом эндпоинте, утрата задокументирована):
//	seller_price_sum      — валовая цена продавца по выкупам (order_feed);
//	price_with_disc_sum   — цена со скидкой по продажам 'S*' (operational_sales);
//	for_pay_sales_sum − for_pay_returns_sum — упрощённый нетто WB
//	('S*' минус 'R*', оба положительные — конвенция нетто аудита sales).
const regionSalesDailyTableSQL = `
-- ============================================================================
-- REGION SALES DAILY (продажи по регионам, замена region-sale, ANALYTICAL)
-- Пишет: cmd/data-analyzers/build-region-sales-daily (после download-all).
-- Grain: день-статуса × nm × город × ФО × регион × страна × модель склада.
-- ============================================================================

CREATE TABLE IF NOT EXISTS analytical.region_sales_daily (
    stat_day             TEXT    NOT NULL,             -- МСК-день текущего статуса юнита (order_feed.updated_at)
    nm_id                BIGINT  NOT NULL,
    destination_city     TEXT    NOT NULL DEFAULT '',  -- населённый пункт доставки (лента)
    destination_district TEXT    NOT NULL DEFAULT '',  -- федеральный округ доставки (лента)
    region_name          TEXT    NOT NULL DEFAULT '',  -- регион покупателя (orders, fallback operational_sales)
    oblast_okrug_name    TEXT    NOT NULL DEFAULT '',  -- обл./округ покупателя (там же)
    country_name         TEXT    NOT NULL DEFAULT '',  -- страна покупателя (там же)
    is_mp                BOOLEAN NOT NULL DEFAULT FALSE, -- склад продавца (FBS/DBS) vs WB (FBO)

    events_qty           BIGINT  NOT NULL DEFAULT 0,   -- юнитов с событием статуса в этот день (все статусы)
    created_qty          BIGINT  NOT NULL DEFAULT 0,   -- status='created'
    buyout_qty           BIGINT  NOT NULL DEFAULT 0,   -- status='buyout' ≈ «Выкупили, шт.» старого отчёта
    cancel_qty           BIGINT  NOT NULL DEFAULT 0,   -- status='cancel' (отмена до/при получении, истечение)
    return_qty           BIGINT  NOT NULL DEFAULT 0,   -- status IN ('return','returnDefective')

    seller_price_sum     DOUBLE PRECISION NOT NULL DEFAULT 0, -- валовая цена продавца по выкупам (лента)
    price_with_disc_sum  DOUBLE PRECISION NOT NULL DEFAULT 0, -- цена со скидкой по продажам 'S*'
    for_pay_sales_sum    DOUBLE PRECISION NOT NULL DEFAULT 0, -- упрощённый нетто WB: продажи 'S*'
    for_pay_returns_sum  DOUBLE PRECISION NOT NULL DEFAULT 0, -- возвраты 'R*' (положительные, вычитать)
    os_rows              BIGINT  NOT NULL DEFAULT 0,   -- юнитов со сшитой строкой статистики (контроль денежного покрытия)

    updated_at           TEXT    NOT NULL DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    PRIMARY KEY (stat_day, nm_id, destination_city, destination_district, region_name, country_name, is_mp)
);

CREATE INDEX IF NOT EXISTS idx_region_sales_daily_day   ON analytical.region_sales_daily (stat_day);
CREATE INDEX IF NOT EXISTS idx_region_sales_daily_nm    ON analytical.region_sales_daily (stat_day, nm_id);
CREATE INDEX IF NOT EXISTS idx_region_sales_daily_region ON analytical.region_sales_daily (stat_day, region_name);
`

// opsalesSridIndexSQL — srid-джойн агрегата по operational_sales: до сентября
// 2026 таблица не имела srid-индекса (srid не участвует в пути загрузки).
// Идемпотентен; каноническое место объявления — opsalesSchemaSQL, здесь
// дублируется, чтобы анализатор поднял индекс до ближайшего прогона opsales.
const opsalesSridIndexSQL = `CREATE INDEX IF NOT EXISTS idx_opsales_srid ON public.operational_sales(srid)`

// InitRegionSalesDailySchema создаёт схему analytical (если нет), таблицу
// region_sales_daily, srid-индекс на operational_sales (если таблица есть —
// на тестовых БД без конвейера её может не быть, тогда индекс ставит первый
// прогон с RAW-сырьём) и выдаёт read-only права (доктрина dev_data_layers.md).
// Каждый DDL — отдельный Exec.
func InitRegionSalesDailySchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, analyticalSchemaSQL); err != nil {
		return fmt.Errorf("analytical schema: %w", err)
	}
	if _, err := pool.Exec(ctx, regionSalesDailyTableSQL); err != nil {
		return fmt.Errorf("region_sales_daily: %w", err)
	}
	var haveOpsales bool
	if err := pool.QueryRow(ctx,
		`SELECT to_regclass('public.operational_sales') IS NOT NULL`).Scan(&haveOpsales); err != nil {
		return fmt.Errorf("check operational_sales: %w", err)
	}
	if haveOpsales {
		if _, err := pool.Exec(ctx, opsalesSridIndexSQL); err != nil {
			return fmt.Errorf("opsales srid index: %w", err)
		}
	}
	return grantAnalyticalReadonly(ctx, pool)
}
