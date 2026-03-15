// Package main provides JSON to SQLite auto-mapping functionality.
//
// SchemaMapper analyzes JSON structures and creates appropriate SQLite tables
// automatically, following the "Raw In, String Out" philosophy.
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// SchemaMapper handles automatic JSON to SQLite schema conversion.
type SchemaMapper struct {
	db *sql.DB
}

// NewSchemaMapper creates a new SchemaMapper for the given database.
func NewSchemaMapper(db *sql.DB) *SchemaMapper {
	return &SchemaMapper{db: db}
}

// ColumnDef represents a column definition in SQLite.
type ColumnDef struct {
	Name     string
	Type     string
	Nullable bool
}

// TableDef represents a table definition with columns.
type TableDef struct {
	Name    string
	Columns []ColumnDef
}

// JSONToTable analyzes JSON data and creates/inserts into appropriate tables.
// It handles nested structures by creating separate tables with foreign keys.
func (sm *SchemaMapper) JSONToTable(endpoint string, jsonData []byte, requestParams map[string]interface{}) error {
	var data map[string]interface{}
	if err := json.Unmarshal(jsonData, &data); err != nil {
		return fmt.Errorf("unmarshal JSON: %w", err)
	}

	// Create metadata table to track fetches
	if err := sm.createMetaTable(endpoint); err != nil {
		return fmt.Errorf("create meta table: %w", err)
	}

	// Insert fetch metadata
	fetchID, err := sm.insertFetchMeta(endpoint, requestParams, jsonData)
	if err != nil {
		return fmt.Errorf("insert fetch meta: %w", err)
	}

	// Process based on endpoint type
	switch endpoint {
	case "funnel", "funnel_history", "search_positions", "search_queries":
		return sm.processFunnelData(endpoint, data, fetchID)
	case "fullstats":
		return sm.processFullstatsData(endpoint, data, fetchID)
	case "feedbacks":
		return sm.processFeedbacksData(endpoint, data, fetchID)
	case "questions":
		return sm.processQuestionsData(endpoint, data, fetchID)
	case "attribution":
		return sm.processAttributionData(endpoint, data, fetchID)
	case "sales":
		return sm.processSalesData(endpoint, data, fetchID)
	default:
		// Generic processing
		return sm.processGenericData(endpoint, data, fetchID)
	}
}

// createMetaTable creates the metadata tracking table for an endpoint.
func (sm *SchemaMapper) createMetaTable(endpoint string) error {
	tableName := fmt.Sprintf("%s_fetches", endpoint)
	createSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			fetched_at TEXT NOT NULL,
			request_params TEXT,
			raw_json TEXT,
			record_count INTEGER DEFAULT 0
		)`, tableName)

	_, err := sm.db.Exec(createSQL)
	return err
}

// insertFetchMeta inserts a new fetch record and returns its ID.
func (sm *SchemaMapper) insertFetchMeta(endpoint string, params map[string]interface{}, rawJSON []byte) (int64, error) {
	tableName := fmt.Sprintf("%s_fetches", endpoint)

	paramsJSON, _ := json.Marshal(params)

	insertSQL := fmt.Sprintf(`
		INSERT INTO %s (fetched_at, request_params, raw_json)
		VALUES (?, ?, ?)`, tableName)

	result, err := sm.db.Exec(insertSQL, time.Now().Format(time.RFC3339), string(paramsJSON), string(rawJSON))
	if err != nil {
		return 0, err
	}

	return result.LastInsertId()
}

// processFunnelData processes funnel-related endpoint data.
func (sm *SchemaMapper) processFunnelData(endpoint string, data map[string]interface{}, fetchID int64) error {
	// Extract products from data.data.products
	dataWrapper, ok := data["data"].(map[string]interface{})
	if !ok {
		return nil // No data to process
	}

	products, ok := dataWrapper["products"].([]interface{})
	if !ok {
		return nil // No products
	}

	// Create products table
	tableName := fmt.Sprintf("%s_products", endpoint)
	createSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			fetch_id INTEGER,
			nm_id INTEGER,
			vendor_code TEXT,
			title TEXT,
			brand_name TEXT,
			subject_id INTEGER,
			subject_name TEXT,
			stock_wb INTEGER,
			stock_mp INTEGER,
			product_rating REAL,
			feedback_rating REAL,
			open_count INTEGER,
			cart_count INTEGER,
			order_count INTEGER,
			buyout_count INTEGER,
			cancel_count INTEGER,
			order_sum INTEGER,
			buyout_sum INTEGER,
			avg_price INTEGER,
			add_to_cart_percent REAL,
			cart_to_order_percent REAL,
			buyout_percent REAL,
			foreign KEY (fetch_id) REFERENCES %s_fetches(id)
		)`, tableName, endpoint)

	if _, err := sm.db.Exec(createSQL); err != nil {
		return fmt.Errorf("create products table: %w", err)
	}

	// Insert products
	insertSQL := fmt.Sprintf(`
		INSERT INTO %s (
			fetch_id, nm_id, vendor_code, title, brand_name,
			subject_id, subject_name, stock_wb, stock_mp,
			product_rating, feedback_rating,
			open_count, cart_count, order_count, buyout_count, cancel_count,
			order_sum, buyout_sum, avg_price,
			add_to_cart_percent, cart_to_order_percent, buyout_percent
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tableName)

	for _, p := range products {
		product, ok := p.(map[string]interface{})
		if !ok {
			continue
		}

		// Extract product info
		productInfo, _ := product["product"].(map[string]interface{})
		statistics, _ := product["statistic"].(map[string]interface{})
		selected, _ := statistics["selected"].(map[string]interface{})
		conversions, _ := selected["conversions"].(map[string]interface{})
		stocks, _ := productInfo["stocks"].(map[string]interface{})

		_, err := sm.db.Exec(insertSQL,
			fetchID,
			getInt(productInfo, "nmId"),
			getString(productInfo, "vendorCode"),
			getString(productInfo, "title"),
			getString(productInfo, "brandName"),
			getInt(productInfo, "subjectId"),
			getString(productInfo, "subjectName"),
			getInt(stocks, "wb"),
			getInt(stocks, "mp"),
			getFloat(productInfo, "productRating"),
			getFloat(productInfo, "feedbackRating"),
			getInt(selected, "openCount"),
			getInt(selected, "cartCount"),
			getInt(selected, "orderCount"),
			getInt(selected, "buyoutCount"),
			getInt(selected, "cancelCount"),
			getInt(selected, "orderSum"),
			getInt(selected, "buyoutSum"),
			getInt(selected, "avgPrice"),
			getFloat(conversions, "addToCartPercent"),
			getFloat(conversions, "cartToOrderPercent"),
			getFloat(conversions, "buyoutPercent"),
		)
		if err != nil {
			return fmt.Errorf("insert product: %w", err)
		}
	}

	// Update record count
	sm.updateRecordCount(endpoint, fetchID, len(products))

	return nil
}

// processFullstatsData processes advertising fullstats data.
func (sm *SchemaMapper) processFullstatsData(endpoint string, data map[string]interface{}, fetchID int64) error {
	// Fullstats returns an array directly
	campaigns, ok := data["data"].([]interface{})
	if !ok {
		// Try direct array
		campaigns, ok = data[""].([]interface{})
		if !ok {
			return nil
		}
	}

	// Create campaigns table
	tableName := fmt.Sprintf("%s_campaigns", endpoint)
	createSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			fetch_id INTEGER,
			advert_id INTEGER,
			views INTEGER,
			clicks INTEGER,
			ctr REAL,
			cpc REAL,
			cr REAL,
			orders INTEGER,
			shks INTEGER,
			atbs INTEGER,
			canceled INTEGER,
			sum REAL,
			sum_price REAL,
			foreign KEY (fetch_id) REFERENCES %s_fetches(id)
		)`, tableName, endpoint)

	if _, err := sm.db.Exec(createSQL); err != nil {
		return fmt.Errorf("create campaigns table: %w", err)
	}

	// Create daily stats table
	dailyTableName := fmt.Sprintf("%s_daily", endpoint)
	createDailySQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			campaign_id INTEGER,
			stats_date TEXT,
			views INTEGER,
			clicks INTEGER,
			orders INTEGER,
			sum REAL,
			sum_price REAL,
			foreign KEY (campaign_id) REFERENCES %s(id)
		)`, dailyTableName, tableName)

	if _, err := sm.db.Exec(createDailySQL); err != nil {
		return fmt.Errorf("create daily table: %w", err)
	}

	// Insert campaigns
	insertSQL := fmt.Sprintf(`
		INSERT INTO %s (
			fetch_id, advert_id, views, clicks, ctr, cpc, cr,
			orders, shks, atbs, canceled, sum, sum_price
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tableName)

	insertDailySQL := fmt.Sprintf(`
		INSERT INTO %s (campaign_id, stats_date, views, clicks, orders, sum, sum_price)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, dailyTableName)

	count := 0
	for _, c := range campaigns {
		campaign, ok := c.(map[string]interface{})
		if !ok {
			continue
		}

		result, err := sm.db.Exec(insertSQL,
			fetchID,
			getInt(campaign, "advertId"),
			getInt(campaign, "views"),
			getInt(campaign, "clicks"),
			getFloat(campaign, "ctr"),
			getFloat(campaign, "cpc"),
			getFloat(campaign, "cr"),
			getInt(campaign, "orders"),
			getInt(campaign, "shks"),
			getInt(campaign, "atbs"),
			getInt(campaign, "canceled"),
			getFloat(campaign, "sum"),
			getFloat(campaign, "sum_price"),
		)
		if err != nil {
			return fmt.Errorf("insert campaign: %w", err)
		}

		campaignID, _ := result.LastInsertId()
		count++

		// Insert daily stats
		if days, ok := campaign["days"].([]interface{}); ok {
			for _, d := range days {
				day, ok := d.(map[string]interface{})
				if !ok {
					continue
				}

				_, err := sm.db.Exec(insertDailySQL,
					campaignID,
					getString(day, "date"),
					getInt(day, "views"),
					getInt(day, "clicks"),
					getInt(day, "orders"),
					getFloat(day, "sum"),
					getFloat(day, "sum_price"),
				)
				if err != nil {
					return fmt.Errorf("insert daily: %w", err)
				}
			}
		}
	}

	sm.updateRecordCount(endpoint, fetchID, count)

	return nil
}

// processFeedbacksData processes feedbacks data.
func (sm *SchemaMapper) processFeedbacksData(endpoint string, data map[string]interface{}, fetchID int64) error {
	dataWrapper, ok := data["data"].(map[string]interface{})
	if !ok {
		return nil
	}

	feedbacks, ok := dataWrapper["feedbacks"].([]interface{})
	if !ok {
		return nil
	}

	// Create feedbacks table
	tableName := fmt.Sprintf("%s_items", endpoint)
	createSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			fetch_id INTEGER,
			feedback_id TEXT,
			text TEXT,
			product_valuation INTEGER,
			created_date TEXT,
			user_name TEXT,
			nm_id INTEGER,
			product_name TEXT,
			brand_name TEXT,
			supplier_article TEXT,
			has_answer INTEGER DEFAULT 0,
			answer_text TEXT,
			foreign KEY (fetch_id) REFERENCES %s_fetches(id)
		)`, tableName, endpoint)

	if _, err := sm.db.Exec(createSQL); err != nil {
		return fmt.Errorf("create feedbacks table: %w", err)
	}

	insertSQL := fmt.Sprintf(`
		INSERT INTO %s (
			fetch_id, feedback_id, text, product_valuation, created_date,
			user_name, nm_id, product_name, brand_name, supplier_article,
			has_answer, answer_text
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tableName)

	for _, f := range feedbacks {
		feedback, ok := f.(map[string]interface{})
		if !ok {
			continue
		}

		productDetails, _ := feedback["productDetails"].(map[string]interface{})
		answer, _ := feedback["answer"].(map[string]interface{})
		hasAnswer := 0
		if answer != nil {
			hasAnswer = 1
		}

		_, err := sm.db.Exec(insertSQL,
			fetchID,
			getString(feedback, "id"),
			getString(feedback, "text"),
			getInt(feedback, "productValuation"),
			getString(feedback, "createdDate"),
			getString(feedback, "userName"),
			getInt(productDetails, "nmId"),
			getString(productDetails, "productName"),
			getString(productDetails, "brandName"),
			getString(productDetails, "supplierArticle"),
			hasAnswer,
			getString(answer, "text"),
		)
		if err != nil {
			return fmt.Errorf("insert feedback: %w", err)
		}
	}

	sm.updateRecordCount(endpoint, fetchID, len(feedbacks))

	return nil
}

// processQuestionsData processes questions data.
func (sm *SchemaMapper) processQuestionsData(endpoint string, data map[string]interface{}, fetchID int64) error {
	dataWrapper, ok := data["data"].(map[string]interface{})
	if !ok {
		return nil
	}

	questions, ok := dataWrapper["questions"].([]interface{})
	if !ok {
		return nil
	}

	// Create questions table
	tableName := fmt.Sprintf("%s_items", endpoint)
	createSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			fetch_id INTEGER,
			question_id TEXT,
			text TEXT,
			created_date TEXT,
			state TEXT,
			was_viewed INTEGER DEFAULT 0,
			nm_id INTEGER,
			product_name TEXT,
			brand_name TEXT,
			supplier_article TEXT,
			has_answer INTEGER DEFAULT 0,
			answer_text TEXT,
			foreign KEY (fetch_id) REFERENCES %s_fetches(id)
		)`, tableName, endpoint)

	if _, err := sm.db.Exec(createSQL); err != nil {
		return fmt.Errorf("create questions table: %w", err)
	}

	insertSQL := fmt.Sprintf(`
		INSERT INTO %s (
			fetch_id, question_id, text, created_date, state, was_viewed,
			nm_id, product_name, brand_name, supplier_article,
			has_answer, answer_text
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tableName)

	for _, q := range questions {
		question, ok := q.(map[string]interface{})
		if !ok {
			continue
		}

		productDetails, _ := question["productDetails"].(map[string]interface{})
		answer, _ := question["answer"].(map[string]interface{})
		hasAnswer := 0
		if answer != nil {
			hasAnswer = 1
		}
		wasViewed := 0
		if getBool(question, "wasViewed") {
			wasViewed = 1
		}

		_, err := sm.db.Exec(insertSQL,
			fetchID,
			getString(question, "id"),
			getString(question, "text"),
			getString(question, "createdDate"),
			getString(question, "state"),
			wasViewed,
			getInt(productDetails, "nmId"),
			getString(productDetails, "productName"),
			getString(productDetails, "brandName"),
			getString(productDetails, "supplierArticle"),
			hasAnswer,
			getString(answer, "text"),
		)
		if err != nil {
			return fmt.Errorf("insert question: %w", err)
		}
	}

	sm.updateRecordCount(endpoint, fetchID, len(questions))

	return nil
}

// processAttributionData processes attribution analysis data.
func (sm *SchemaMapper) processAttributionData(endpoint string, data map[string]interface{}, fetchID int64) error {
	// Attribution is computed data, store as key metrics
	tableName := fmt.Sprintf("%s_summary", endpoint)
	createSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			fetch_id INTEGER,
			period_start TEXT,
			period_end TEXT,
			total_orders INTEGER,
			organic_orders INTEGER,
			ad_orders INTEGER,
			total_views INTEGER,
			organic_views INTEGER,
			ad_views INTEGER,
			ad_spent REAL,
			foreign KEY (fetch_id) REFERENCES %s_fetches(id)
		)`, tableName, endpoint)

	if _, err := sm.db.Exec(createSQL); err != nil {
		return fmt.Errorf("create attribution table: %w", err)
	}

	// Insert summary
	insertSQL := fmt.Sprintf(`
		INSERT INTO %s (
			fetch_id, period_start, period_end,
			total_orders, organic_orders, ad_orders,
			total_views, organic_views, ad_views, ad_spent
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tableName)

	_, err := sm.db.Exec(insertSQL,
		fetchID,
		getString(data, "periodStart"),
		getString(data, "periodEnd"),
		getInt(data, "totalOrders"),
		getInt(data, "organicOrders"),
		getInt(data, "adOrders"),
		getInt(data, "totalViews"),
		getInt(data, "organicViews"),
		getInt(data, "adViews"),
		getFloat(data, "adSpent"),
	)
	if err != nil {
		return fmt.Errorf("insert attribution: %w", err)
	}

	sm.updateRecordCount(endpoint, fetchID, 1)

	return nil
}

// processSalesData processes sales data from Statistics API.
func (sm *SchemaMapper) processSalesData(endpoint string, data map[string]interface{}, fetchID int64) error {
	// Extract rows from data.data.rows
	dataWrapper, ok := data["data"].(map[string]interface{})
	if !ok {
		return nil // No data to process
	}

	rows, ok := dataWrapper["rows"].([]interface{})
	if !ok {
		return nil // No rows
	}

	// Create sales_rows table
	tableName := fmt.Sprintf("%s_rows", endpoint)
	createSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			fetch_id INTEGER,
			rrd_id INTEGER,
			doc_type_name TEXT,
			sale_id TEXT,
			nm_id INTEGER,
			sa_name TEXT,
			subject_name TEXT,
			brand_name TEXT,
			ts_name TEXT,
			barcode TEXT,
			quantity INTEGER,
			is_cancel INTEGER DEFAULT 0,
			delivery_method TEXT,
			ppvz_for_pay REAL,
			retail_price REAL,
			retail_amount REAL,
			sale_percent REAL,
			commission_percent REAL,
			delivery_rub REAL,
			order_dt TEXT,
			sale_dt TEXT,
			rr_dt TEXT,
			foreign KEY (fetch_id) REFERENCES %s_fetches(id)
		)`, tableName, endpoint)

	if _, err := sm.db.Exec(createSQL); err != nil {
		return fmt.Errorf("create sales table: %w", err)
	}

	// Insert rows
	insertSQL := fmt.Sprintf(`
		INSERT INTO %s (
			fetch_id, rrd_id, doc_type_name, sale_id, nm_id,
			sa_name, subject_name, brand_name, ts_name, barcode,
			quantity, is_cancel, delivery_method,
			ppvz_for_pay, retail_price, retail_amount,
			sale_percent, commission_percent, delivery_rub,
			order_dt, sale_dt, rr_dt
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tableName)

	for _, r := range rows {
		row, ok := r.(map[string]interface{})
		if !ok {
			continue
		}

		isCancel := 0
		if getBool(row, "is_cancel") {
			isCancel = 1
		}

		_, err := sm.db.Exec(insertSQL,
			fetchID,
			getInt(row, "rrd_id"),
			getString(row, "doc_type_name"),
			getString(row, "sale_id"),
			getInt(row, "nm_id"),
			getString(row, "sa_name"),
			getString(row, "subject_name"),
			getString(row, "brand_name"),
			getString(row, "ts_name"),
			getString(row, "barcode"),
			getInt(row, "quantity"),
			isCancel,
			getString(row, "delivery_method"),
			getFloat(row, "ppvz_for_pay"),
			getFloat(row, "retail_price"),
			getFloat(row, "retail_amount"),
			getFloat(row, "sale_percent"),
			getFloat(row, "commission_percent"),
			getFloat(row, "delivery_rub"),
			getString(row, "order_dt"),
			getString(row, "sale_dt"),
			getString(row, "rr_dt"),
		)
		if err != nil {
			return fmt.Errorf("insert sales row: %w", err)
		}
	}

	sm.updateRecordCount(endpoint, fetchID, len(rows))

	return nil
}

// processGenericData handles any JSON structure generically.
func (sm *SchemaMapper) processGenericData(endpoint string, data map[string]interface{}, fetchID int64) error {
	// For unknown structures, create a simple key-value table
	tableName := fmt.Sprintf("%s_data", endpoint)
	createSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			fetch_id INTEGER,
			key TEXT,
			value TEXT,
			foreign KEY (fetch_id) REFERENCES %s_fetches(id)
		)`, tableName, endpoint)

	if _, err := sm.db.Exec(createSQL); err != nil {
		return fmt.Errorf("create generic table: %w", err)
	}

	// Flatten and insert
	insertSQL := fmt.Sprintf("INSERT INTO %s (fetch_id, key, value) VALUES (?, ?, ?)", tableName)

	flattened := flattenMap(data, "")
	for key, value := range flattened {
		_, err := sm.db.Exec(insertSQL, fetchID, key, value)
		if err != nil {
			return fmt.Errorf("insert generic: %w", err)
		}
	}

	sm.updateRecordCount(endpoint, fetchID, len(flattened))

	return nil
}

// updateRecordCount updates the record count in the fetch metadata.
func (sm *SchemaMapper) updateRecordCount(endpoint string, fetchID int64, count int) {
	tableName := fmt.Sprintf("%s_fetches", endpoint)
	updateSQL := fmt.Sprintf("UPDATE %s SET record_count = ? WHERE id = ?", tableName)
	sm.db.Exec(updateSQL, count, fetchID)
}

// Helper functions for extracting values from maps

func getInt(m map[string]interface{}, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	}
	return 0
}

func getFloat(m map[string]interface{}, key string) float64 {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}

func getString(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getBool(m map[string]interface{}, key string) bool {
	if m == nil {
		return false
	}
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

// flattenMap flattens a nested map into dot-notation keys.
func flattenMap(m map[string]interface{}, prefix string) map[string]string {
	result := make(map[string]string)

	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}

		switch val := v.(type) {
		case map[string]interface{}:
			nested := flattenMap(val, key)
			for nk, nv := range nested {
				result[nk] = nv
			}
		case []interface{}:
			// Skip arrays in generic flattening
			result[key] = "[array]"
		default:
			result[key] = fmt.Sprintf("%v", val)
		}
	}

	return result
}

// inferSQLiteType converts Go types to SQLite types.
func inferSQLiteType(v interface{}) string {
	if v == nil {
		return "TEXT"
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "INTEGER"
	case reflect.Float32, reflect.Float64:
		return "REAL"
	case reflect.Bool:
		return "INTEGER"
	case reflect.String:
		return "TEXT"
	default:
		return "TEXT"
	}
}

// GetSchema returns the current schema for an endpoint.
func (sm *SchemaMapper) GetSchema(endpoint string) ([]string, error) {
	var tables []string

	// Get all tables for this endpoint
	rows, err := sm.db.Query(`
		SELECT name FROM sqlite_master
		WHERE type='table' AND name LIKE ?
		ORDER BY name`, endpoint+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}

	return tables, nil
}

// GetTableInfo returns column information for a table.
func (sm *SchemaMapper) GetTableInfo(tableName string) ([]ColumnDef, error) {
	rows, err := sm.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []ColumnDef
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dfltValue interface{}

		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return nil, err
		}

		columns = append(columns, ColumnDef{
			Name:     name,
			Type:     colType,
			Nullable: notNull == 0,
		})
	}

	return columns, nil
}

// GenerateCreateStatement generates CREATE TABLE SQL from column definitions.
func (sm *SchemaMapper) GenerateCreateStatement(tableName string, columns []ColumnDef) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (", tableName))

	var colDefs []string
	for i, col := range columns {
		nullSpec := ""
		if !col.Nullable {
			nullSpec = " NOT NULL"
		}
		colDefs = append(colDefs, fmt.Sprintf("    %s %s%s", col.Name, col.Type, nullSpec))
		if i < len(columns)-1 {
			colDefs[len(colDefs)-1] += ","
		}
	}

	parts = append(parts, strings.Join(colDefs, "\n"))
	parts = append(parts, ")")

	return strings.Join(parts, "\n")
}
