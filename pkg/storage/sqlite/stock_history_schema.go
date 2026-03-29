// Package sqlite provides SQLite storage implementation.
package sqlite

const (
	// StockHistorySchemaSQL defines tables for stock history CSV reports.
	//
	// WB API generates CSV reports asynchronously via /api/v2/nm-report/downloads.
	// Two report types:
	//   - STOCK_HISTORY_REPORT_CSV: metrics with monthly columns
	//   - STOCK_HISTORY_DAILY_CSV: daily stock levels per warehouse
	//
	// All fields confirmed via testing on 2026-03-29.
	StockHistorySchemaSQL = `
-- ============================================================================
-- STOCK HISTORY CSV REPORTS (WB Analytics API — async CSV generation)
-- ============================================================================

-- Report metadata table
CREATE TABLE IF NOT EXISTS stock_history_reports (
	id TEXT PRIMARY KEY,              -- UUID отчёта
	report_type TEXT NOT NULL,        -- 'metrics' или 'daily'
	start_date TEXT NOT NULL,         -- YYYY-MM-DD
	end_date TEXT NOT NULL,           -- YYYY-MM-DD
	stock_type TEXT NOT NULL,         -- '', 'wb', 'mp'
	status TEXT NOT NULL,             -- 'SUCCESS', 'FAILED', 'TIMEOUT'
	file_size INTEGER,                -- bytes
	created_at TEXT NOT NULL,         -- timestamp из API
	downloaded_at TEXT,               -- timestamp загрузки
	rows_count INTEGER DEFAULT 0,     -- количество строк
	UNIQUE(report_type, start_date, end_date, stock_type)
);

-- STOCK_HISTORY_REPORT_CSV — metrics with monthly columns
CREATE TABLE IF NOT EXISTS stock_history_metrics (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	report_id TEXT NOT NULL,          -- FK to stock_history_reports

	-- Поля товара
	vendor_code TEXT,                 -- Артикул продавца
	name TEXT,                        -- Название товара
	nm_id INTEGER NOT NULL,           -- Артикул WB
	subject_name TEXT,                -- Название предмета
	brand_name TEXT,                  -- Бренд
	size_name TEXT,                   -- Название размера
	chrt_id INTEGER,                  -- ID размера

	-- Поля склада
	region_name TEXT,                 -- Регион отгрузки
	office_name TEXT,                 -- Название склада
	availability TEXT,                -- Доступность (deficient/actual/balanced)

	-- Заказы и продажи
	orders_count INTEGER,             -- Заказы, шт.
	orders_sum INTEGER,               -- Заказы, сумма
	buyout_count INTEGER,             -- Выкупы, шт.
	buyout_sum INTEGER,               -- Выкупы, сумма
	buyout_percent INTEGER,           -- Процент выкупа

	-- Средние значения
	avg_orders REAL,                  -- Среднее количество заказов в день

	-- Остатки
	stock_count INTEGER,              -- Остатки на текущий день, шт.
	stock_sum INTEGER,                -- Стоимость остатков на текущий день

	-- Оборачиваемость
	sale_rate INTEGER,                -- Оборачиваемость текущих остатков в часах
	avg_stock_turnover INTEGER,       -- Оборачиваемость средних остатков в часах

	-- В пути
	to_client_count INTEGER,          -- В пути к клиенту, шт.
	from_client_count INTEGER,        -- В пути от клиента, шт.

	-- Цена
	price INTEGER,                    -- Текущая цена продавца со скидкой

	-- Отсутствие
	office_missing_time INTEGER,      -- Время отсутствия товара на складе в часах

	-- Упущенное
	lost_orders_count REAL,           -- Упущенные заказы, шт.
	lost_orders_sum REAL,             -- Упущенные заказы, сумма
	lost_buyouts_count REAL,          -- Упущенные выкупы, шт.
	lost_buyouts_sum REAL,            -- Упущенные выкупы, сумма

	-- Динамические колонки ( AvgOrdersByMonth_MM.YYYY )
	monthly_data TEXT,                -- JSON: {"02.2024": 10.5, "03.2024": 15.2}

	-- Валюта
	currency TEXT,                    -- RUB, и т.д.

	-- Метаданные
	created_at TEXT DEFAULT CURRENT_TIMESTAMP,

	FOREIGN KEY (report_id) REFERENCES stock_history_reports(id)
);

-- STOCK_HISTORY_DAILY_CSV — daily stock levels per warehouse
CREATE TABLE IF NOT EXISTS stock_history_daily (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	report_id TEXT NOT NULL,          -- FK to stock_history_reports

	-- Поля товара
	vendor_code TEXT,                 -- Артикул продавца
	name TEXT,                        -- Название товара
	nm_id INTEGER NOT NULL,           -- Артикул WB
	subject_name TEXT,                -- Название предмета
	brand_name TEXT,                  -- Бренд
	size_name TEXT,                   -- Название размера
	chrt_id INTEGER,                  -- ID размера

	-- Поля склада
	office_name TEXT,                 -- Название склада

	-- Динамические колонки ( DD.MM.YYYY — остаток на 23:59 )
	daily_data TEXT,                  -- JSON: {"10.02.2024": 100, "11.02.2024": 95}

	-- Метаданные
	created_at TEXT DEFAULT CURRENT_TIMESTAMP,

	FOREIGN KEY (report_id) REFERENCES stock_history_reports(id)
);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_stock_history_reports_dates
	ON stock_history_reports(start_date, end_date);

CREATE INDEX IF NOT EXISTS idx_stock_history_metrics_nm_id
	ON stock_history_metrics(nm_id);

CREATE INDEX IF NOT EXISTS idx_stock_history_metrics_report_id
	ON stock_history_metrics(report_id);

CREATE INDEX IF NOT EXISTS idx_stock_history_metrics_office
	ON stock_history_metrics(office_name);

CREATE INDEX IF NOT EXISTS idx_stock_history_daily_nm_id
	ON stock_history_daily(nm_id);

CREATE INDEX IF NOT EXISTS idx_stock_history_daily_report_id
	ON stock_history_daily(report_id);

CREATE INDEX IF NOT EXISTS idx_stock_history_daily_office
	ON stock_history_daily(office_name);
`
)

// GetStockHistorySchemaSQL returns the stock history tables schema.
func GetStockHistorySchemaSQL() string {
	return StockHistorySchemaSQL
}
