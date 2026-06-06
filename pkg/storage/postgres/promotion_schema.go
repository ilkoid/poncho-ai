package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// initPromotionSchema creates all promotion V2 tables in PostgreSQL.
// Translated from pkg/storage/sqlite/schema.go PromotionV2SchemaSQL.
//
// PG-specific translations:
//   - INTEGER PRIMARY KEY AUTOINCREMENT → BIGSERIAL PRIMARY KEY
//   - REAL → DOUBLE PRECISION
//   - INTEGER booleans → BOOLEAN
//   - CURRENT_TIMESTAMP → TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
func initPromotionSchema(ctx context.Context, pool *pgxpool.Pool) error {
	for _, ddl := range []string{
		pgCampaignBidsDDL,
		pgNormqueryStatsDDL,
		pgNormqueryBidsDDL,
		pgNormqueryMinusDDL,
		pgNormqueryClustersDDL,
		pgBidRecommendationsDDL,
		pgBidRecommendationsNqDDL,
		pgPromotionExpensesDDL,
		pgPromotionBalanceDDL,
		pgPromotionBalanceCashbacksDDL,
		pgPromotionPaymentsDDL,
		pgCalendarPromotionsDDL,
		pgCalendarPromotionDetailsDDL,
		pgCalendarPromotionAdvantagesDDL,
		pgCalendarPromotionRangingDDL,
		pgCalendarPromotionNomenclaturesDDL,
		pgCampaignBudgetDDL,
		pgMinBidsDDL,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			return fmt.Errorf("exec DDL: %w", err)
		}
	}
	// Create indexes separately (IF NOT EXISTS is safe)
	for _, idx := range []string{
		"CREATE INDEX IF NOT EXISTS idx_campaign_bids_nm ON campaign_bids(nm_id)",
		"CREATE INDEX IF NOT EXISTS idx_nq_stats_campaign_date ON normquery_stats(advert_id, stats_date)",
		"CREATE INDEX IF NOT EXISTS idx_nq_stats_date ON normquery_stats(stats_date)",
		"CREATE INDEX IF NOT EXISTS idx_bid_rec_nm ON bid_recommendations(nm_id)",
		"CREATE INDEX IF NOT EXISTS idx_bid_rec_date ON bid_recommendations(snapshot_date)",
		"CREATE INDEX IF NOT EXISTS idx_promo_exp_advert ON promotion_expenses(advert_id)",
		"CREATE INDEX IF NOT EXISTS idx_cal_prom_noms_nm ON wb_calendar_promotion_nomenclatures(nm_id)",
		"CREATE INDEX IF NOT EXISTS idx_cal_prom_noms_promo ON wb_calendar_promotion_nomenclatures(promotion_id)",
		"CREATE INDEX IF NOT EXISTS idx_min_bids_nm ON min_bids(nm_id)",
		"CREATE INDEX IF NOT EXISTS idx_min_bids_date ON min_bids(snapshot_date)",
	} {
		if _, err := pool.Exec(ctx, idx); err != nil {
			return fmt.Errorf("exec index: %w", err)
		}
	}
	if _, err := pool.Exec(ctx, promotionMigrations); err != nil {
		return fmt.Errorf("promotion migrations (int4→bigint): %w", err)
	}
	return nil
}

// ============================================================================
// Table DDLs — 18 promotion V2 tables
// ============================================================================

var pgCampaignBidsDDL = `
CREATE TABLE IF NOT EXISTS campaign_bids (
    advert_id    BIGINT NOT NULL,
    nm_id        BIGINT NOT NULL,
    subject_id   BIGINT DEFAULT 0,
    subject_name TEXT,
    bid_search   BIGINT DEFAULT 0,
    bid_reco     BIGINT DEFAULT 0,
    created_at   TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    updated_at   TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    UNIQUE(advert_id, nm_id)
)`

var pgNormqueryStatsDDL = `
CREATE TABLE IF NOT EXISTS normquery_stats (
    id         BIGSERIAL PRIMARY KEY,
    advert_id  BIGINT NOT NULL,
    nm_id      BIGINT NOT NULL,
    stats_date TEXT NOT NULL,
    normquery  TEXT NOT NULL,
    views      BIGINT DEFAULT 0,
    clicks     BIGINT DEFAULT 0,
    ctr        DOUBLE PRECISION DEFAULT 0,
    cpc        DOUBLE PRECISION DEFAULT 0,
    cpm        DOUBLE PRECISION DEFAULT 0,
    avg_pos    DOUBLE PRECISION DEFAULT 0,
    orders     BIGINT DEFAULT 0,
    shks       BIGINT DEFAULT 0,
    atbs       BIGINT DEFAULT 0,
    spend      DOUBLE PRECISION DEFAULT 0,
    created_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    UNIQUE(advert_id, nm_id, stats_date, normquery)
)`

var pgNormqueryBidsDDL = `
CREATE TABLE IF NOT EXISTS normquery_bids (
    id         BIGSERIAL PRIMARY KEY,
    advert_id  BIGINT NOT NULL,
    nm_id      BIGINT NOT NULL,
    normquery  TEXT NOT NULL,
    bid        BIGINT DEFAULT 0,
    created_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    UNIQUE(advert_id, nm_id, normquery)
)`

var pgNormqueryMinusDDL = `
CREATE TABLE IF NOT EXISTS normquery_minus (
    id          BIGSERIAL PRIMARY KEY,
    advert_id   BIGINT NOT NULL,
    nm_id       BIGINT NOT NULL,
    minus_query TEXT NOT NULL,
    created_at  TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    UNIQUE(advert_id, nm_id, minus_query)
)`

var pgNormqueryClustersDDL = `
CREATE TABLE IF NOT EXISTS normquery_clusters (
    id          BIGSERIAL PRIMARY KEY,
    advert_id   BIGINT NOT NULL,
    nm_id       BIGINT NOT NULL,
    normquery   TEXT NOT NULL,
    is_excluded BOOLEAN DEFAULT FALSE,
    created_at  TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    UNIQUE(advert_id, nm_id, normquery)
)`

var pgBidRecommendationsDDL = `
CREATE TABLE IF NOT EXISTS bid_recommendations (
    id              BIGSERIAL PRIMARY KEY,
    nm_id           BIGINT NOT NULL,
    advert_id       BIGINT DEFAULT 0,
    snapshot_date   TEXT NOT NULL,
    competitive_bid BIGINT DEFAULT 0,
    leaders_bid     BIGINT DEFAULT 0,
    top2            BIGINT DEFAULT 0,
    created_at      TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    UNIQUE(nm_id, advert_id, snapshot_date)
)`

var pgBidRecommendationsNqDDL = `
CREATE TABLE IF NOT EXISTS bid_recommendations_nq (
    id              BIGSERIAL PRIMARY KEY,
    nm_id           BIGINT NOT NULL,
    normquery       TEXT NOT NULL,
    snapshot_date   TEXT NOT NULL,
    reach_min_bid    BIGINT DEFAULT 0,
    reach_medium_bid BIGINT DEFAULT 0,
    reach_max_bid    BIGINT DEFAULT 0,
    created_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    UNIQUE(nm_id, normquery, snapshot_date)
)`

var pgPromotionExpensesDDL = `
CREATE TABLE IF NOT EXISTS promotion_expenses (
    id            BIGSERIAL PRIMARY KEY,
    advert_id     BIGINT NOT NULL,
    upd_num       BIGINT NOT NULL,
    upd_time      TEXT,
    upd_sum       BIGINT DEFAULT 0,
    camp_name     TEXT,
    advert_type   INTEGER DEFAULT 0,
    payment_type  TEXT,
    advert_status INTEGER DEFAULT 0,
    created_at    TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    UNIQUE(advert_id, upd_num)
)`

var pgPromotionBalanceDDL = `
CREATE TABLE IF NOT EXISTS promotion_balance (
    snapshot_date TEXT PRIMARY KEY,
    balance BIGINT DEFAULT 0,
    net     BIGINT DEFAULT 0,
    bonus   BIGINT DEFAULT 0,
    created_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
)`

var pgPromotionBalanceCashbacksDDL = `
CREATE TABLE IF NOT EXISTS promotion_balance_cashbacks (
    id              BIGSERIAL PRIMARY KEY,
    snapshot_date   TEXT NOT NULL,
    sum_val         BIGINT DEFAULT 0,
    percent_val     INTEGER DEFAULT 0,
    expiration_date TEXT,
    created_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    UNIQUE(snapshot_date, expiration_date)
)`

var pgPromotionPaymentsDDL = `
CREATE TABLE IF NOT EXISTS promotion_payments (
    id           BIGSERIAL PRIMARY KEY,
    payment_id   BIGINT NOT NULL,
    sum_val      BIGINT DEFAULT 0,
    payment_date TEXT,
    type_val     INTEGER DEFAULT 0,
    status_id    INTEGER DEFAULT 0,
    card_status  TEXT,
    created_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    UNIQUE(payment_id)
)`

var pgCalendarPromotionsDDL = `
CREATE TABLE IF NOT EXISTS wb_calendar_promotions (
    promotion_id BIGINT PRIMARY KEY,
    name TEXT,
    start_date TEXT,
    end_date TEXT,
    type TEXT,
    created_at TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
)`

var pgCalendarPromotionDetailsDDL = `
CREATE TABLE IF NOT EXISTS wb_calendar_promotion_details (
    promotion_id                  BIGINT PRIMARY KEY,
    description                   TEXT,
    in_promo_action_leftovers     BIGINT DEFAULT 0,
    in_promo_action_total         BIGINT DEFAULT 0,
    not_in_promo_action_leftovers BIGINT DEFAULT 0,
    not_in_promo_action_total     BIGINT DEFAULT 0,
    participation_percentage      INTEGER DEFAULT 0,
    exception_products_count      BIGINT DEFAULT 0,
    created_at                    TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    updated_at                    TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
)`

var pgCalendarPromotionAdvantagesDDL = `
CREATE TABLE IF NOT EXISTS wb_calendar_promotion_advantages (
    id           BIGSERIAL PRIMARY KEY,
    promotion_id BIGINT NOT NULL,
    advantage    TEXT NOT NULL,
    created_at   TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    UNIQUE(promotion_id, advantage)
)`

var pgCalendarPromotionRangingDDL = `
CREATE TABLE IF NOT EXISTS wb_calendar_promotion_ranging (
    id                BIGSERIAL PRIMARY KEY,
    promotion_id      BIGINT NOT NULL,
    condition         TEXT NOT NULL,
    participation_rate INTEGER DEFAULT 0,
    boost             BIGINT DEFAULT 0,
    created_at        TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    UNIQUE(promotion_id, condition)
)`

var pgCalendarPromotionNomenclaturesDDL = `
CREATE TABLE IF NOT EXISTS wb_calendar_promotion_nomenclatures (
    id            BIGSERIAL PRIMARY KEY,
    promotion_id  BIGINT NOT NULL,
    nm_id         BIGINT NOT NULL,
    in_action     BOOLEAN DEFAULT FALSE,
    price         DOUBLE PRECISION DEFAULT 0,
    plan_price    DOUBLE PRECISION DEFAULT 0,
    discount      INTEGER DEFAULT 0,
    plan_discount INTEGER DEFAULT 0,
    currency_code TEXT DEFAULT 'RUB',
    snapshot_date TEXT NOT NULL,
    created_at    TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    UNIQUE(promotion_id, nm_id, snapshot_date)
)`

var pgCampaignBudgetDDL = `
CREATE TABLE IF NOT EXISTS campaign_budget (
    advert_id     BIGINT NOT NULL,
    snapshot_date TEXT NOT NULL,
    total_budget  BIGINT DEFAULT 0,
    created_at    TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    UNIQUE(advert_id, snapshot_date)
)`

var pgMinBidsDDL = `
CREATE TABLE IF NOT EXISTS min_bids (
    id             BIGSERIAL PRIMARY KEY,
    nm_id          BIGINT NOT NULL,
    advert_id      BIGINT NOT NULL,
    placement_type TEXT NOT NULL,
    min_bid        BIGINT DEFAULT 0,
    snapshot_date  TEXT NOT NULL,
    created_at     TEXT DEFAULT TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
    UNIQUE(nm_id, advert_id, placement_type, snapshot_date)
)`

const promotionMigrations = `
-- campaign_bids
ALTER TABLE campaign_bids ALTER COLUMN advert_id TYPE BIGINT;
ALTER TABLE campaign_bids ALTER COLUMN nm_id TYPE BIGINT;
ALTER TABLE campaign_bids ALTER COLUMN subject_id TYPE BIGINT;
ALTER TABLE campaign_bids ALTER COLUMN bid_search TYPE BIGINT;
ALTER TABLE campaign_bids ALTER COLUMN bid_reco TYPE BIGINT;
-- normquery_stats
ALTER TABLE normquery_stats ALTER COLUMN advert_id TYPE BIGINT;
ALTER TABLE normquery_stats ALTER COLUMN nm_id TYPE BIGINT;
ALTER TABLE normquery_stats ALTER COLUMN views TYPE BIGINT;
ALTER TABLE normquery_stats ALTER COLUMN clicks TYPE BIGINT;
ALTER TABLE normquery_stats ALTER COLUMN orders TYPE BIGINT;
ALTER TABLE normquery_stats ALTER COLUMN shks TYPE BIGINT;
ALTER TABLE normquery_stats ALTER COLUMN atbs TYPE BIGINT;
-- normquery_bids
ALTER TABLE normquery_bids ALTER COLUMN advert_id TYPE BIGINT;
ALTER TABLE normquery_bids ALTER COLUMN nm_id TYPE BIGINT;
ALTER TABLE normquery_bids ALTER COLUMN bid TYPE BIGINT;
-- normquery_minus
ALTER TABLE normquery_minus ALTER COLUMN advert_id TYPE BIGINT;
ALTER TABLE normquery_minus ALTER COLUMN nm_id TYPE BIGINT;
-- normquery_clusters
ALTER TABLE normquery_clusters ALTER COLUMN advert_id TYPE BIGINT;
ALTER TABLE normquery_clusters ALTER COLUMN nm_id TYPE BIGINT;
-- bid_recommendations
ALTER TABLE bid_recommendations ALTER COLUMN nm_id TYPE BIGINT;
ALTER TABLE bid_recommendations ALTER COLUMN advert_id TYPE BIGINT;
ALTER TABLE bid_recommendations ALTER COLUMN competitive_bid TYPE BIGINT;
ALTER TABLE bid_recommendations ALTER COLUMN leaders_bid TYPE BIGINT;
ALTER TABLE bid_recommendations ALTER COLUMN top2 TYPE BIGINT;
-- bid_recommendations_nq
ALTER TABLE bid_recommendations_nq ALTER COLUMN nm_id TYPE BIGINT;
ALTER TABLE bid_recommendations_nq ALTER COLUMN reach_min_bid TYPE BIGINT;
ALTER TABLE bid_recommendations_nq ALTER COLUMN reach_medium_bid TYPE BIGINT;
ALTER TABLE bid_recommendations_nq ALTER COLUMN reach_max_bid TYPE BIGINT;
-- promotion_expenses
ALTER TABLE promotion_expenses ALTER COLUMN advert_id TYPE BIGINT;
ALTER TABLE promotion_expenses ALTER COLUMN upd_num TYPE BIGINT;
ALTER TABLE promotion_expenses ALTER COLUMN upd_sum TYPE BIGINT;
-- promotion_balance
ALTER TABLE promotion_balance ALTER COLUMN balance TYPE BIGINT;
ALTER TABLE promotion_balance ALTER COLUMN net TYPE BIGINT;
ALTER TABLE promotion_balance ALTER COLUMN bonus TYPE BIGINT;
-- promotion_balance_cashbacks
ALTER TABLE promotion_balance_cashbacks ALTER COLUMN sum_val TYPE BIGINT;
-- promotion_payments
ALTER TABLE promotion_payments ALTER COLUMN payment_id TYPE BIGINT;
ALTER TABLE promotion_payments ALTER COLUMN sum_val TYPE BIGINT;
-- wb_calendar_promotions
ALTER TABLE wb_calendar_promotions ALTER COLUMN promotion_id TYPE BIGINT;
-- wb_calendar_promotion_details
ALTER TABLE wb_calendar_promotion_details ALTER COLUMN promotion_id TYPE BIGINT;
ALTER TABLE wb_calendar_promotion_details ALTER COLUMN in_promo_action_leftovers TYPE BIGINT;
ALTER TABLE wb_calendar_promotion_details ALTER COLUMN in_promo_action_total TYPE BIGINT;
ALTER TABLE wb_calendar_promotion_details ALTER COLUMN not_in_promo_action_leftovers TYPE BIGINT;
ALTER TABLE wb_calendar_promotion_details ALTER COLUMN not_in_promo_action_total TYPE BIGINT;
ALTER TABLE wb_calendar_promotion_details ALTER COLUMN exception_products_count TYPE BIGINT;
-- wb_calendar_promotion_advantages
ALTER TABLE wb_calendar_promotion_advantages ALTER COLUMN promotion_id TYPE BIGINT;
-- wb_calendar_promotion_ranging
ALTER TABLE wb_calendar_promotion_ranging ALTER COLUMN promotion_id TYPE BIGINT;
ALTER TABLE wb_calendar_promotion_ranging ALTER COLUMN boost TYPE BIGINT;
-- wb_calendar_promotion_nomenclatures
ALTER TABLE wb_calendar_promotion_nomenclatures ALTER COLUMN promotion_id TYPE BIGINT;
ALTER TABLE wb_calendar_promotion_nomenclatures ALTER COLUMN nm_id TYPE BIGINT;
-- campaign_budget
ALTER TABLE campaign_budget ALTER COLUMN advert_id TYPE BIGINT;
ALTER TABLE campaign_budget ALTER COLUMN total_budget TYPE BIGINT;
-- min_bids
ALTER TABLE min_bids ALTER COLUMN nm_id TYPE BIGINT;
ALTER TABLE min_bids ALTER COLUMN advert_id TYPE BIGINT;
ALTER TABLE min_bids ALTER COLUMN min_bid TYPE BIGINT;
`
