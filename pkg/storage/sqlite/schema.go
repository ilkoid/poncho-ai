// Package sqlite provides SQLite storage implementation.
package sqlite

const (
	// SchemaSQL defines the sales table structure.
	// Table stores detailed sales data from WB API reportDetailByPeriod.
	SchemaSQL = `
-- Main sales table with all fields from WB RealizationReportRow
CREATE TABLE IF NOT EXISTS sales (
    -- Primary key
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    -- WB API identifiers (unique for pagination)
    rrd_id INTEGER UNIQUE NOT NULL,
    realizationreport_id INTEGER,

    -- Product identifiers
    nm_id INTEGER NOT NULL,
    supplier_article TEXT,
    barcode TEXT,

    -- Product metadata
    brand_name TEXT,
    subject_name TEXT,
    ts_name TEXT,

    -- Transaction details
    doc_type_name TEXT,                -- "Продажа", "Возврат"
    quantity INTEGER,
    retail_price REAL,
    retail_amount REAL,
    sale_percent REAL,
    commission_percent REAL,

    -- Financial
    ppvz_for_pay REAL,
    delivery_rub REAL,

    -- Delivery method (KEY for FBW filtering)
    delivery_method TEXT,
    gi_box_type_name TEXT,

    -- Warehouse
    office_name TEXT,

    -- Dates
    order_dt TEXT,
    sale_dt TEXT,
    rr_dt TEXT,

    -- Cancellation
    is_cancel INTEGER DEFAULT 0,
    cancel_dt TEXT,

    -- Metadata
    created_at TEXT DEFAULT CURRENT_TIMESTAMP
);

-- Index for fast lookups by nm_id (product analytics)
CREATE INDEX IF NOT EXISTS idx_sales_nm_id ON sales(nm_id);

-- Index for date range queries (period filtering)
CREATE INDEX IF NOT EXISTS idx_sales_sale_dt ON sales(sale_dt);

-- Index for delivery method filtering (FBW vs FBS)
CREATE INDEX IF NOT EXISTS idx_sales_delivery_method ON sales(delivery_method);

-- Index for report date queries
CREATE INDEX IF NOT EXISTS idx_sales_rr_dt ON sales(rr_dt);

-- Index for rrd_id pagination lookups
CREATE INDEX IF NOT EXISTS idx_sales_rrd_id ON sales(rrd_id);
`

	// ServiceRecordsSchemaSQL defines the service_records table structure.
	// Table stores logistics, pickup points, and deduction records from WB API.
	// Two types of service records:
	// 1. nm_id = 0: General logistics/warehouse/PVZ costs
	// 2. nm_id > 0 + empty doc_type_name: Product-specific logistics (returns to seller, etc.)
	ServiceRecordsSchemaSQL = `
-- Service records table for logistics, pickup points, and deductions
-- Includes both general (nm_id=0) and product-specific (nm_id>0, empty doc_type) records
CREATE TABLE IF NOT EXISTS service_records (
    -- Primary key
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    -- WB API identifiers
    rrd_id INTEGER UNIQUE NOT NULL,
    realizationreport_id INTEGER,

    -- Operation type (KEY field for classification!)
    -- Values: "Возмещение издержек...", "Удержание", "Логистика", etc.
    supplier_oper_name TEXT,

    -- Product info (for product-specific logistics, NULL for general records)
    nm_id INTEGER DEFAULT 0,
    supplier_article TEXT,
    brand_name TEXT,
    subject_name TEXT,

    -- Partial identifiers (often filled for service records)
    barcode TEXT,
    shk_id INTEGER,
    srid TEXT,

    -- Delivery info (for product-specific logistics)
    delivery_method TEXT,
    gi_box_type_name TEXT,
    delivery_rub REAL,

    -- Financial data
    ppvz_vw REAL,              -- Корректировка
    ppvz_vw_nds REAL,          -- НДС корректировки
    rebill_logistic_cost REAL, -- Стоимость логистики

    -- Dates
    rr_dt TEXT,
    order_dt TEXT,
    sale_dt TEXT,

    -- Metadata
    created_at TEXT DEFAULT CURRENT_TIMESTAMP
);

-- Index for operation type queries (group by logistics/deductions/etc)
CREATE INDEX IF NOT EXISTS idx_service_oper ON service_records(supplier_oper_name);

-- Index for report date queries
CREATE INDEX IF NOT EXISTS idx_service_rr_dt ON service_records(rr_dt);

-- Index for rrd_id lookups
CREATE INDEX IF NOT EXISTS idx_service_rrd_id ON service_records(rrd_id);

-- Index for product-specific queries
CREATE INDEX IF NOT EXISTS idx_service_nm_id ON service_records(nm_id);
`

	// CreateViewSQL creates a view for FBW-only sales.
	// FBW = Fulfillment by Wildberries (sales from WB warehouses)
	// Note: Exact delivery_method values for FBW need to be verified
	// through real API response. This is a conservative filter.
	CreateViewSQL = `
CREATE VIEW IF NOT EXISTS fbw_sales AS
SELECT * FROM sales
WHERE delivery_method LIKE '%WB%' OR delivery_method NOT LIKE '%FBS%'
   OR delivery_method LIKE '%ФБВ%' OR delivery_method = '';
`

	// FunnelSchemaSQL defines the funnel analytics tables structure.
	// Stores product metadata and daily funnel metrics from WB Analytics API v3.
	// Used for trend analysis and correlation with actual sales data.
	FunnelSchemaSQL = `
-- ============================================================================
-- FUNNEL ANALYTICS TABLES (WB Analytics API v3)
-- ============================================================================

-- Products metadata table (slowly changing dimension)
-- Stores product info that changes rarely
CREATE TABLE IF NOT EXISTS products (
    -- Primary key (WB product ID)
    nm_id INTEGER PRIMARY KEY,

    -- Product identification
    vendor_code TEXT,
    title TEXT,
    brand_name TEXT,

    -- Category hierarchy
    subject_id INTEGER,
    subject_name TEXT,

    -- Quality metrics (updated when funnel data is loaded)
    product_rating REAL,
    feedback_rating REAL,

    -- Stock levels (snapshot from last API call)
    stock_wb INTEGER DEFAULT 0,
    stock_mp INTEGER DEFAULT 0,
    stock_balance_sum INTEGER DEFAULT 0,

    -- Tags (JSON array of ProductTag)
    tags TEXT,

    -- Metadata
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);

-- Index for category-based analysis
CREATE INDEX IF NOT EXISTS idx_products_subject_id ON products(subject_id);

-- Index for brand analysis
CREATE INDEX IF NOT EXISTS idx_products_brand_name ON products(brand_name);

-- ============================================================================
-- Daily funnel metrics (core time-series fact table)
-- Grain: one row per (nm_id, date) combination
-- ============================================================================
CREATE TABLE IF NOT EXISTS funnel_metrics_daily (
    -- Surrogate primary key
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    -- Natural key (business key for upserts)
    nm_id INTEGER NOT NULL,
    metric_date TEXT NOT NULL,

    -- Funnel counts (raw metrics)
    open_count INTEGER DEFAULT 0,
    cart_count INTEGER DEFAULT 0,
    order_count INTEGER DEFAULT 0,
    buyout_count INTEGER DEFAULT 0,
    cancel_count INTEGER DEFAULT 0,
    add_to_wishlist INTEGER DEFAULT 0,

    -- Financial metrics (in rubles)
    order_sum INTEGER DEFAULT 0,
    buyout_sum INTEGER DEFAULT 0,
    cancel_sum INTEGER DEFAULT 0,
    avg_price INTEGER DEFAULT 0,

    -- Conversion rates (pre-calculated for query performance)
    conversion_add_to_cart REAL,
    conversion_cart_to_order REAL,
    conversion_buyout REAL,

    -- WB Club metrics (premium customer segment)
    wb_club_order_count INTEGER DEFAULT 0,
    wb_club_buyout_count INTEGER DEFAULT 0,
    wb_club_buyout_percent REAL,

    -- Operational metrics
    time_to_ready_days INTEGER DEFAULT 0,
    time_to_ready_hours INTEGER DEFAULT 0,
    time_to_ready_mins INTEGER DEFAULT 0,
    localization_percent REAL,

    -- Metadata
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,

    -- Unique constraint for upsert logic
    UNIQUE(nm_id, metric_date)
);

-- Composite index for time-series queries (product-focused)
CREATE INDEX IF NOT EXISTS idx_funnel_product_date
    ON funnel_metrics_daily(nm_id, metric_date);

-- Composite index for cross-product analysis (date-focused)
CREATE INDEX IF NOT EXISTS idx_funnel_date_product
    ON funnel_metrics_daily(metric_date, nm_id);

-- Index for trending detection (order count by date)
CREATE INDEX IF NOT EXISTS idx_funnel_orders
    ON funnel_metrics_daily(metric_date, order_count);

-- Index for conversion analysis queries
CREATE INDEX IF NOT EXISTS idx_funnel_conversion
    ON funnel_metrics_daily(metric_date, conversion_buyout);
`

	// PromotionSchemaSQL defines the promotion analytics tables structure.
	// Stores campaign metadata and daily stats from WB Promotion API.
	// Used for ROI analysis and advertising performance tracking.
	PromotionSchemaSQL = `
-- ============================================================================
-- PROMOTION API TABLES (WB Promotion API)
-- ============================================================================

-- Campaigns table - metadata for advertising campaigns
-- Source: GET /adv/v1/promotion/count
CREATE TABLE IF NOT EXISTS campaigns (
    -- Primary key (WB campaign ID)
    advert_id INTEGER PRIMARY KEY,

    -- From /adv/v1/promotion/count
    campaign_type INTEGER NOT NULL,   -- type: 8=search, 9=auto, 50=catalog
    status INTEGER NOT NULL,          -- -1=deleted, 4=ready, 7=finished, 8=canceled, 9=active, 11=paused
    change_time TEXT,                 -- Last modification time

    -- Aggregated stats from /adv/v3/fullstats (updated on each load)
    total_views INTEGER DEFAULT 0,
    total_clicks INTEGER DEFAULT 0,
    total_orders INTEGER DEFAULT 0,
    total_sum REAL DEFAULT 0,         -- Total spent on ads
    total_sum_price REAL DEFAULT 0,   -- Total order value

    -- Metadata
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);

-- Index for status filtering (active vs paused vs finished)
CREATE INDEX IF NOT EXISTS idx_campaigns_status ON campaigns(status);

-- Index for type analysis (search vs auto vs catalog)
CREATE INDEX IF NOT EXISTS idx_campaigns_type ON campaigns(campaign_type);

-- Index for change_time (resume mode)
CREATE INDEX IF NOT EXISTS idx_campaigns_change_time ON campaigns(change_time);

-- ============================================================================
-- Daily campaign statistics (core time-series fact table)
-- Source: GET /adv/v3/fullstats
-- Grain: one row per (advert_id, date) combination
-- ============================================================================
CREATE TABLE IF NOT EXISTS campaign_stats_daily (
    -- Surrogate primary key
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    -- Natural key (business key for upserts)
    advert_id INTEGER NOT NULL,
    stats_date TEXT NOT NULL,

    -- Core metrics from /adv/v3/fullstats
    views INTEGER DEFAULT 0,
    clicks INTEGER DEFAULT 0,
    ctr REAL DEFAULT 0,            -- Click-through rate (%)
    cpc REAL DEFAULT 0,            -- Cost per click (rubles)
    cr REAL DEFAULT 0,             -- Conversion rate (%)

    -- Orders & revenue
    orders INTEGER DEFAULT 0,
    shks INTEGER DEFAULT 0,        -- Buyouts (штрихкод продажи)
    atbs INTEGER DEFAULT 0,        -- Returns (возвраты)
    canceled INTEGER DEFAULT 0,    -- Cancellations

    -- Financial
    sum REAL DEFAULT 0,            -- Ad spend (rubles)
    sum_price REAL DEFAULT 0,      -- Order value (rubles)

    -- Metadata
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,

    -- Unique constraint for upsert logic
    UNIQUE(advert_id, stats_date)
);

-- Composite index for time-series queries (campaign-focused)
CREATE INDEX IF NOT EXISTS idx_campaign_stats_campaign_date
    ON campaign_stats_daily(advert_id, stats_date);

-- Composite index for cross-campaign analysis (date-focused)
CREATE INDEX IF NOT EXISTS idx_campaign_stats_date
    ON campaign_stats_daily(stats_date);

-- Index for spend analysis
CREATE INDEX IF NOT EXISTS idx_campaign_stats_spend
    ON campaign_stats_daily(stats_date, sum);

-- ============================================================================
-- Campaign-Product relationship (many-to-many)
-- Source: /adv/v3/fullstats (nms array in days[].apps[])
-- ============================================================================
CREATE TABLE IF NOT EXISTS campaign_products (
    -- Surrogate primary key
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    -- Foreign keys
    advert_id INTEGER NOT NULL,
    nm_id INTEGER NOT NULL,

    -- Product info in campaign context
    product_name TEXT,

    -- Per-product stats (aggregated from fullstats)
    total_views INTEGER DEFAULT 0,
    total_clicks INTEGER DEFAULT 0,
    total_orders INTEGER DEFAULT 0,
    total_sum REAL DEFAULT 0,

    -- Metadata
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,

    -- Unique constraint
    UNIQUE(advert_id, nm_id)
);

-- Index for product-focused queries (what campaigns promote this product?)
CREATE INDEX IF NOT EXISTS idx_campaign_products_nm
    ON campaign_products(nm_id);

-- Index for campaign-focused queries (what products in this campaign?)
CREATE INDEX IF NOT EXISTS idx_campaign_products_campaign
    ON campaign_products(advert_id);
`

	// FunnelAggregatedSchemaSQL defines the aggregated funnel metrics table.
	// Stores aggregated data from WB Analytics API v3 /sales-funnel/products.
	// Grain: one row per (nm_id, period_start, period_end).
	FunnelAggregatedSchemaSQL = `
-- ============================================================================
-- AGGREGATED FUNNEL METRICS (WB Analytics API v3 - /sales-funnel/products)
-- Grain: one row per (nm_id, selected_period_start, selected_period_end)
-- ============================================================================

CREATE TABLE IF NOT EXISTS funnel_metrics_aggregated (
    -- Primary key
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    -- Natural key (business key for upserts)
    nm_id INTEGER NOT NULL,
    period_start TEXT NOT NULL,
    period_end TEXT NOT NULL,

    -- Selected period metrics
    selected_open_count INTEGER DEFAULT 0,
    selected_cart_count INTEGER DEFAULT 0,
    selected_order_count INTEGER DEFAULT 0,
    selected_order_sum INTEGER DEFAULT 0,
    selected_buyout_count INTEGER DEFAULT 0,
    selected_buyout_sum INTEGER DEFAULT 0,
    selected_cancel_count INTEGER DEFAULT 0,
    selected_cancel_sum INTEGER DEFAULT 0,
    selected_avg_price INTEGER DEFAULT 0,
    selected_avg_orders_count_per_day REAL,
    selected_share_order_percent REAL,
    selected_add_to_wishlist INTEGER DEFAULT 0,
    selected_localization_percent REAL,
    selected_time_to_ready_days INTEGER DEFAULT 0,
    selected_time_to_ready_hours INTEGER DEFAULT 0,
    selected_time_to_ready_mins INTEGER DEFAULT 0,

    -- Selected WB Club metrics
    selected_wb_club_order_count INTEGER DEFAULT 0,
    selected_wb_club_order_sum INTEGER DEFAULT 0,
    selected_wb_club_buyout_count INTEGER DEFAULT 0,
    selected_wb_club_buyout_sum INTEGER DEFAULT 0,
    selected_wb_club_cancel_count INTEGER DEFAULT 0,
    selected_wb_club_cancel_sum INTEGER DEFAULT 0,
    selected_wb_club_avg_price INTEGER DEFAULT 0,
    selected_wb_club_buyout_percent REAL,
    selected_wb_club_avg_order_count_per_day REAL,

    -- Selected Conversions
    selected_conversion_add_to_cart REAL,
    selected_conversion_cart_to_order REAL,
    selected_conversion_buyout REAL,

    -- Past period metrics (nullable)
    past_period_start TEXT,
    past_period_end TEXT,
    past_open_count INTEGER,
    past_cart_count INTEGER,
    past_order_count INTEGER,
    past_order_sum INTEGER,
    past_buyout_count INTEGER,
    past_buyout_sum INTEGER,
    past_cancel_count INTEGER,
    past_cancel_sum INTEGER,
    past_avg_price INTEGER,
    past_avg_orders_count_per_day REAL,
    past_share_order_percent REAL,
    past_add_to_wishlist INTEGER,
    past_localization_percent REAL,
    past_time_to_ready_days INTEGER,
    past_time_to_ready_hours INTEGER,
    past_time_to_ready_mins INTEGER,

    -- Past WB Club metrics
    past_wb_club_order_count INTEGER,
    past_wb_club_order_sum INTEGER,
    past_wb_club_buyout_count INTEGER,
    past_wb_club_buyout_sum INTEGER,
    past_wb_club_cancel_count INTEGER,
    past_wb_club_cancel_sum INTEGER,
    past_wb_club_avg_price INTEGER,
    past_wb_club_buyout_percent REAL,
    past_wb_club_avg_order_count_per_day REAL,

    -- Past Conversions
    past_conversion_add_to_cart REAL,
    past_conversion_cart_to_order REAL,
    past_conversion_buyout REAL,

    -- Comparison metrics (nullable)
    comparison_open_count_dynamic INTEGER,
    comparison_cart_count_dynamic INTEGER,
    comparison_order_count_dynamic INTEGER,
    comparison_order_sum_dynamic INTEGER,
    comparison_buyout_count_dynamic INTEGER,
    comparison_buyout_sum_dynamic INTEGER,
    comparison_cancel_count_dynamic INTEGER,
    comparison_cancel_sum_dynamic INTEGER,
    comparison_avg_orders_count_per_day_dynamic REAL,
    comparison_avg_price_dynamic INTEGER,
    comparison_share_order_percent_dynamic REAL,
    comparison_add_to_wishlist_dynamic INTEGER,
    comparison_localization_percent_dynamic REAL,
    comparison_time_to_ready_days INTEGER,
    comparison_time_to_ready_hours INTEGER,
    comparison_time_to_ready_mins INTEGER,

    -- Comparison WB Club metrics
    comparison_wb_club_order_count INTEGER,
    comparison_wb_club_order_sum INTEGER,
    comparison_wb_club_buyout_count INTEGER,
    comparison_wb_club_buyout_sum INTEGER,
    comparison_wb_club_cancel_count INTEGER,
    comparison_wb_club_cancel_sum INTEGER,
    comparison_wb_club_avg_price INTEGER,
    comparison_wb_club_buyout_percent REAL,
    comparison_wb_club_avg_order_count_per_day REAL,

    -- Comparison Conversions
    comparison_conversion_add_to_cart REAL,
    comparison_conversion_cart_to_order REAL,
    comparison_conversion_buyout REAL,

    -- Metadata
    currency TEXT DEFAULT 'RUB',
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,

    -- Unique constraint for upsert logic
    UNIQUE(nm_id, period_start, period_end)
);

-- Index for product-focused queries
CREATE INDEX IF NOT EXISTS idx_funnel_agg_product_period
    ON funnel_metrics_aggregated(nm_id, period_start, period_end);

-- Index for period-focused queries
CREATE INDEX IF NOT EXISTS idx_funnel_agg_period
    ON funnel_metrics_aggregated(period_start, period_end);

-- Index for order dynamics analysis
CREATE INDEX IF NOT EXISTS idx_funnel_agg_orders
    ON funnel_metrics_aggregated(period_start, selected_order_count);

-- Index for conversion analysis
CREATE INDEX IF NOT EXISTS idx_funnel_agg_conversion
    ON funnel_metrics_aggregated(period_start, selected_conversion_buyout);
`

	// CampaignFullstatsSchemaSQL defines tables for detailed campaign breakdown.
	// Stores platform-level (apps) and product-level (nms) daily stats,
	// plus booster-specific stats from GET /adv/v3/fullstats.
	// Grain: campaign_stats_app = (advert_id, stats_date, app_type)
	//        campaign_stats_nm  = (advert_id, stats_date, app_type, nm_id)
	//        campaign_booster_stats = (advert_id, stats_date, nm_id)
	CampaignFullstatsSchemaSQL = `
-- ============================================================================
-- CAMPAIGN FULLSTATS TABLES (WB Advertising API v3 - /adv/v3/fullstats)
-- ============================================================================

-- Platform-level daily stats (grain: advert_id + date + app_type)
-- Source: GET /adv/v3/fullstats → days[].apps[]
CREATE TABLE IF NOT EXISTS campaign_stats_app (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    advert_id INTEGER NOT NULL,
    stats_date TEXT NOT NULL,
    app_type INTEGER NOT NULL,              -- 1=site, 32=Android, 64=iOS
    views INTEGER DEFAULT 0,
    clicks INTEGER DEFAULT 0,
    ctr REAL DEFAULT 0,
    cpc REAL DEFAULT 0,
    cr REAL DEFAULT 0,
    orders INTEGER DEFAULT 0,
    shks INTEGER DEFAULT 0,
    atbs INTEGER DEFAULT 0,
    canceled INTEGER DEFAULT 0,
    sum REAL DEFAULT 0,
    sum_price REAL DEFAULT 0,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(advert_id, stats_date, app_type)
);

CREATE INDEX IF NOT EXISTS idx_campaign_stats_app_campaign_date
    ON campaign_stats_app(advert_id, stats_date);

CREATE INDEX IF NOT EXISTS idx_campaign_stats_app_date
    ON campaign_stats_app(stats_date);

-- Product-level daily stats per platform (grain: advert_id + date + app_type + nm_id)
-- Source: GET /adv/v3/fullstats → days[].apps[].nms[]
CREATE TABLE IF NOT EXISTS campaign_stats_nm (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    advert_id INTEGER NOT NULL,
    stats_date TEXT NOT NULL,
    app_type INTEGER NOT NULL,
    nm_id INTEGER NOT NULL,
    nm_name TEXT,
    views INTEGER DEFAULT 0,
    clicks INTEGER DEFAULT 0,
    ctr REAL DEFAULT 0,
    cpc REAL DEFAULT 0,
    cr REAL DEFAULT 0,
    orders INTEGER DEFAULT 0,
    shks INTEGER DEFAULT 0,
    atbs INTEGER DEFAULT 0,
    canceled INTEGER DEFAULT 0,
    sum REAL DEFAULT 0,
    sum_price REAL DEFAULT 0,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(advert_id, stats_date, app_type, nm_id)
);

CREATE INDEX IF NOT EXISTS idx_campaign_stats_nm_campaign_date
    ON campaign_stats_nm(advert_id, stats_date);

CREATE INDEX IF NOT EXISTS idx_campaign_stats_nm_nm
    ON campaign_stats_nm(nm_id);

CREATE INDEX IF NOT EXISTS idx_campaign_stats_nm_date
    ON campaign_stats_nm(stats_date);

-- Booster-specific stats (grain: advert_id + date + nm_id)
-- Source: GET /adv/v3/fullstats → boosterStats[]
CREATE TABLE IF NOT EXISTS campaign_booster_stats (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    advert_id INTEGER NOT NULL,
    stats_date TEXT NOT NULL,
    nm_id INTEGER NOT NULL,
    avg_position REAL DEFAULT 0,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(advert_id, stats_date, nm_id)
);

CREATE INDEX IF NOT EXISTS idx_campaign_booster_campaign_date
    ON campaign_booster_stats(advert_id, stats_date);
`

)

// GetSchemaSQL returns the main table schema.
func GetSchemaSQL() string {
	return SchemaSQL
}

// GetServiceRecordsSchemaSQL returns the service_records table schema.
func GetServiceRecordsSchemaSQL() string {
	return ServiceRecordsSchemaSQL
}

// GetCreateViewSQL returns the FBW view creation SQL.
func GetCreateViewSQL() string {
	return CreateViewSQL
}

// GetFunnelSchemaSQL returns the funnel analytics tables schema.
func GetFunnelSchemaSQL() string {
	return FunnelSchemaSQL
}

// GetPromotionSchemaSQL returns the promotion analytics tables schema.
func GetPromotionSchemaSQL() string {
	return PromotionSchemaSQL
}

// GetFunnelAggregatedSchemaSQL returns the aggregated funnel metrics table schema.
func GetFunnelAggregatedSchemaSQL() string {
	return FunnelAggregatedSchemaSQL
}

// GetCampaignFullstatsSchemaSQL returns the campaign fullstats tables schema.
func GetCampaignFullstatsSchemaSQL() string {
	return CampaignFullstatsSchemaSQL
}
