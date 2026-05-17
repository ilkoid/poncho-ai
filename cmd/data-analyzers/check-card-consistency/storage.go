package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
	_ "github.com/mattn/go-sqlite3"
)

// ResultsRepo — read/write доступ к card-analysis.db.
type ResultsRepo struct {
	db *sql.DB
}

func NewResultsRepo(dbPath string) (*ResultsRepo, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open results db: %w", err)
	}
	return &ResultsRepo{db: db}, nil
}

func (r *ResultsRepo) Close() error { return r.db.Close() }

// InitSchema создаёт таблицы card_analysis и card_change_log.
func (r *ResultsRepo) InitSchema(ctx context.Context) error {
	const createAnalysisSQL = `
	CREATE TABLE IF NOT EXISTS card_analysis (
		nm_id INTEGER PRIMARY KEY,
		vendor_code TEXT NOT NULL,
		title TEXT,
		subject_id INTEGER DEFAULT NULL,
		subject_name TEXT,

		text_done INTEGER DEFAULT 0,
		text_has_discrepancy INTEGER DEFAULT NULL,
		text_summary TEXT DEFAULT '',
		text_checked_at DATETIME,

		vision_done INTEGER DEFAULT 0,
		vision_product_type TEXT DEFAULT '',
		vision_attributes TEXT DEFAULT '',
		vision_photo_urls TEXT DEFAULT '',
		vision_summary TEXT DEFAULT '',
		vision_has_discrepancy INTEGER DEFAULT NULL,
		vision_checked_at DATETIME,

		new_title TEXT DEFAULT '',
		new_description TEXT DEFAULT '',
		new_characteristics TEXT DEFAULT '',
		new_subject_id INTEGER DEFAULT NULL,
		new_subject_name TEXT DEFAULT '',

		wb_updated INTEGER DEFAULT 0,
		wb_update_response TEXT DEFAULT '',
		wb_updated_at DATETIME,

		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`

	const createLogSQL = `
	CREATE TABLE IF NOT EXISTS card_change_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		nm_id INTEGER NOT NULL,
		vendor_code TEXT NOT NULL,
		field TEXT NOT NULL,
		old_value TEXT NOT NULL,
		new_value TEXT NOT NULL,
		changed_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`

	if _, err := r.db.ExecContext(ctx, createAnalysisSQL); err != nil {
		return fmt.Errorf("create card_analysis: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, createLogSQL); err != nil {
		return fmt.Errorf("create card_change_log: %w", err)
	}

	// Миграции: добавляем колонки если их нет
	migrations := []string{
		"ALTER TABLE card_analysis ADD COLUMN text_done INTEGER DEFAULT 0",
		"ALTER TABLE card_analysis ADD COLUMN vision_done INTEGER DEFAULT 0",
		"ALTER TABLE card_analysis ADD COLUMN generate_done INTEGER DEFAULT 0",
		"ALTER TABLE card_analysis ADD COLUMN max_visibility REAL DEFAULT 0",
		"ALTER TABLE card_analysis ADD COLUMN top_query TEXT DEFAULT ''",
		"ALTER TABLE card_analysis ADD COLUMN product_rating REAL DEFAULT 0",
		"ALTER TABLE card_analysis ADD COLUMN feedback_rating REAL DEFAULT 0",
		"ALTER TABLE card_analysis ADD COLUMN priority_score REAL DEFAULT 0",
		"ALTER TABLE card_analysis ADD COLUMN avg_position REAL DEFAULT 0",
		"ALTER TABLE card_analysis ADD COLUMN open_card_30d INTEGER DEFAULT 0",
		"ALTER TABLE card_analysis ADD COLUMN orders_30d INTEGER DEFAULT 0",
		"ALTER TABLE card_analysis ADD COLUMN top_queries TEXT DEFAULT ''",
	}
	for _, m := range migrations {
		r.db.ExecContext(ctx, m) // ignore error — column already exists
	}

	return nil
}

// EnsureRows создаёт строки в card_analysis для указанных nm_id, если их ещё нет.
func (r *ResultsRepo) EnsureRows(ctx context.Context, cards []CardData) (int, error) {
	const insertSQL = `
	INSERT OR IGNORE INTO card_analysis (nm_id, vendor_code, title, subject_id, subject_name)
	VALUES (?, ?, ?, ?, ?)`

	stmt, err := r.db.PrepareContext(ctx, insertSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare ensure row: %w", err)
	}
	defer stmt.Close()

	inserted := 0
	for _, c := range cards {
		res, err := stmt.ExecContext(ctx, c.NmID, c.VendorCode, c.Title, c.SubjectID, c.SubjectName)
		if err != nil {
			return inserted, fmt.Errorf("ensure row nm_id=%d: %w", c.NmID, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted++
		}
	}
	return inserted, nil
}

// BackfillMetrics заполняет метрики в card_analysis из wb-sales.db.
// ATTACH-ит source DB и обновляет: рейтинги, видимость, позицию, поисковые запросы, priority_score.
// Идемпотентно — можно вызывать повторно для обновления метрик.
func (r *ResultsRepo) BackfillMetrics(ctx context.Context, sourceDBPath string) (updated int, err error) {
	if _, err := r.db.ExecContext(ctx, fmt.Sprintf("ATTACH DATABASE '%s' AS src", sourceDBPath)); err != nil {
		return 0, fmt.Errorf("attach source db: %w", err)
	}
	defer r.db.ExecContext(ctx, "DETACH DATABASE src")

	type updateStep struct {
		name string
		sql  string
	}

	steps := []updateStep{
		{"ratings", `
			UPDATE card_analysis
			SET product_rating = COALESCE(
				    (SELECT COALESCE(product_rating, 0) FROM src.products WHERE src.products.nm_id = card_analysis.nm_id), 0),
			    feedback_rating = COALESCE(
				    (SELECT COALESCE(feedback_rating, 0) FROM src.products WHERE src.products.nm_id = card_analysis.nm_id), 0)`},
		{"max_visibility", `
			UPDATE card_analysis
			SET max_visibility = COALESCE((
				SELECT MAX(visibility) FROM src.search_positions_daily
				WHERE src.search_positions_daily.nm_id = card_analysis.nm_id
				  AND snapshot_date >= DATE('now', '-14 day')
			), 0)`},
		{"avg_position", `
			UPDATE card_analysis
			SET avg_position = COALESCE((
				SELECT AVG(avg_position) FROM src.search_positions_daily
				WHERE src.search_positions_daily.nm_id = card_analysis.nm_id
				  AND snapshot_date >= DATE('now', '-14 day')
			), 0)`},
		{"top_query", `
			UPDATE card_analysis
			SET top_query = COALESCE((
				SELECT search_text FROM (
					SELECT search_text, SUM(COALESCE(open_card, 0)) AS total_opens
					FROM src.search_queries_daily
					WHERE src.search_queries_daily.nm_id = card_analysis.nm_id
					  AND snapshot_date >= DATE('now', '-30 day')
					GROUP BY search_text
					ORDER BY total_opens DESC
					LIMIT 1
				)
			), '')`},
		{"top_queries", `
			UPDATE card_analysis
			SET top_queries = COALESCE((
				SELECT GROUP_CONCAT(sub.search_text, ', ') FROM (
					SELECT search_text, SUM(COALESCE(open_card, 0)) AS total_opens
					FROM src.search_queries_daily
					WHERE src.search_queries_daily.nm_id = card_analysis.nm_id
					  AND snapshot_date >= DATE('now', '-30 day')
					GROUP BY search_text
					ORDER BY total_opens DESC
					LIMIT 3
				) sub
			), '')`},
		{"open_card_30d", `
			UPDATE card_analysis
			SET open_card_30d = COALESCE((
				SELECT SUM(COALESCE(open_card, 0)) FROM src.search_queries_daily
				WHERE src.search_queries_daily.nm_id = card_analysis.nm_id
				  AND snapshot_date >= DATE('now', '-30 day')
			), 0)`},
		{"orders_30d", `
			UPDATE card_analysis
			SET orders_30d = COALESCE((
				SELECT SUM(COALESCE(orders, 0)) FROM src.search_queries_daily
				WHERE src.search_queries_daily.nm_id = card_analysis.nm_id
				  AND snapshot_date >= DATE('now', '-30 day')
			), 0)`},
		{"priority_score", `
			UPDATE card_analysis
			SET priority_score =
				(1.0 - COALESCE(max_visibility, 0) / 100.0) * 0.5 +
				(1.0 - COALESCE(product_rating, 0) / 10.0) * 0.3 +
				MIN(COALESCE(open_card_30d, 0) / 100.0, 1.0) * 0.2`},
	}

	for _, step := range steps {
		res, err := r.db.ExecContext(ctx, step.sql)
		if err != nil {
			return 0, fmt.Errorf("backfill %s: %w", step.name, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			updated += int(n)
		}
	}

	return updated, nil
}

// SaveTextAnalysis сохраняет результаты текстового анализа (этап 1).
func (r *ResultsRepo) SaveTextAnalysis(ctx context.Context, nmID int, hasDiscrepancy bool, summary string) error {
	const sql_ = `
	UPDATE card_analysis
	SET text_has_discrepancy = ?, text_summary = ?, text_checked_at = ?, updated_at = ?
	WHERE nm_id = ?`

	discrepancy := 0
	if hasDiscrepancy {
		discrepancy = 1
	}
	now := time.Now().Format(time.DateTime)

	res, err := r.db.ExecContext(ctx, sql_, discrepancy, summary, now, now, nmID)
	if err != nil {
		return fmt.Errorf("save text analysis nm_id=%d: %w", nmID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no row for nm_id=%d", nmID)
	}
	return nil
}


// LoadPendingTextCards возвращает nm_id карточек, ещё не обработанных Stage 1 (text_done = 0).
func (r *ResultsRepo) LoadPendingTextCards(ctx context.Context, nmIDs []int) ([]int, error) {
	if len(nmIDs) == 0 {
		return nil, nil
	}
	ph := make([]string, len(nmIDs))
	args := make([]any, len(nmIDs))
	for i, id := range nmIDs {
		ph[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf("SELECT nm_id FROM card_analysis WHERE text_done = 0 AND nm_id IN (%s)", strings.Join(ph, ","))
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query pending text: %w", err)
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MarkTextDone отмечает карточку как полностью обработанную Stage 1.
func (r *ResultsRepo) MarkTextDone(ctx context.Context, nmID int) error {
	_, err := r.db.ExecContext(ctx, "UPDATE card_analysis SET text_done = 1 WHERE nm_id = ?", nmID)
	if err != nil {
		return fmt.Errorf("mark text done nm_id=%d: %w", nmID, err)
	}
	return nil
}

// MarkVisionDone отмечает карточку как полностью обработанную Stage 3.
func (r *ResultsRepo) MarkVisionDone(ctx context.Context, nmID int) error {
	_, err := r.db.ExecContext(ctx, "UPDATE card_analysis SET vision_done = 1 WHERE nm_id = ?", nmID)
	if err != nil {
		return fmt.Errorf("mark vision done nm_id=%d: %w", nmID, err)
	}
	return nil
}

// MarkGenerateDone отмечает карточку как полностью обработанную Stage 4.
func (r *ResultsRepo) MarkGenerateDone(ctx context.Context, nmID int) error {
	_, err := r.db.ExecContext(ctx, "UPDATE card_analysis SET generate_done = 1 WHERE nm_id = ?", nmID)
	if err != nil {
		return fmt.Errorf("mark generate done nm_id=%d: %w", nmID, err)
	}
	return nil
}

// LoadPendingGenerateCards возвращает nm_id карточек, ещё не обработанных Stage 4 (generate_done = 0).
func (r *ResultsRepo) LoadPendingGenerateCards(ctx context.Context, nmIDs []int) ([]int, error) {
	if len(nmIDs) == 0 {
		return nil, nil
	}
	ph := make([]string, len(nmIDs))
	args := make([]any, len(nmIDs))
	for i, id := range nmIDs {
		ph[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf("SELECT nm_id FROM card_analysis WHERE generate_done = 0 AND nm_id IN (%s)", strings.Join(ph, ","))
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query pending generate: %w", err)
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// LoadTextDiscrepancies возвращает nm_id карточек с text_has_discrepancy = 1, не обработанных Vision.
func (r *ResultsRepo) LoadTextDiscrepancies(ctx context.Context) ([]int, error) {
	rows, err := r.db.QueryContext(ctx,
		"SELECT nm_id FROM card_analysis WHERE text_has_discrepancy = 1 AND vision_done = 0")
	if err != nil {
		return nil, fmt.Errorf("query text discrepancies: %w", err)
	}
	defer rows.Close()

	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan nm_id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SaveVisionAnalysis сохраняет результаты Vision анализа (этап 3).
func (r *ResultsRepo) SaveVisionAnalysis(ctx context.Context, nmID int, productType, attributes, photoURLs, summary string, hasDiscrepancy bool) error {
	const sql_ = `
	UPDATE card_analysis
	SET vision_product_type = ?, vision_attributes = ?, vision_photo_urls = ?,
	    vision_summary = ?, vision_has_discrepancy = ?, vision_checked_at = ?, updated_at = ?
	WHERE nm_id = ?`

	discrepancy := 0
	if hasDiscrepancy {
		discrepancy = 1
	}
	now := time.Now().Format(time.DateTime)

	res, err := r.db.ExecContext(ctx, sql_, productType, attributes, photoURLs, summary, discrepancy, now, now, nmID)
	if err != nil {
		return fmt.Errorf("save vision analysis nm_id=%d: %w", nmID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no row for nm_id=%d", nmID)
	}
	return nil
}

// SaveNewParams сохраняет новые параметры карточки (этап 4).
func (r *ResultsRepo) SaveNewParams(ctx context.Context, nmID int, title, description, characteristics string, subjectID int, subjectName string) error {
	const sql_ = `
	UPDATE card_analysis
	SET new_title = ?, new_description = ?, new_characteristics = ?,
	    new_subject_id = ?, new_subject_name = ?, updated_at = ?
	WHERE nm_id = ?`

	now := time.Now().Format(time.DateTime)
	res, err := r.db.ExecContext(ctx, sql_, title, description, characteristics, subjectID, subjectName, now, nmID)
	if err != nil {
		return fmt.Errorf("save new params nm_id=%d: %w", nmID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no row for nm_id=%d", nmID)
	}
	return nil
}

// SaveWBUpdate сохраняет результат обновления WB (этап 5).
func (r *ResultsRepo) SaveWBUpdate(ctx context.Context, nmID int, response string) error {
	const sql_ = `
	UPDATE card_analysis
	SET wb_updated = 1, wb_update_response = ?, wb_updated_at = ?, updated_at = ?
	WHERE nm_id = ?`

	now := time.Now().Format(time.DateTime)
	res, err := r.db.ExecContext(ctx, sql_, response, now, now, nmID)
	if err != nil {
		return fmt.Errorf("save wb update nm_id=%d: %w", nmID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no row for nm_id=%d", nmID)
	}
	return nil
}

// LogChange записывает изменение в card_change_log.
func (r *ResultsRepo) LogChange(ctx context.Context, nmID int, vendorCode, field, oldValue, newValue string) error {
	const sql_ = `
	INSERT INTO card_change_log (nm_id, vendor_code, field, old_value, new_value)
	VALUES (?, ?, ?, ?, ?)`

	_, err := r.db.ExecContext(ctx, sql_, nmID, vendorCode, field, oldValue, newValue)
	if err != nil {
		return fmt.Errorf("log change nm_id=%d field=%s: %w", nmID, field, err)
	}
	return nil
}

// AnalysisRow — строка из card_analysis для чтения.
type AnalysisRow struct {
	NmID              int
	VendorCode        string
	Title             string
	SubjectName       string
	TextHasDiscrepancy *int
	TextSummary       string
	VisionProductType string
	VisionAttributes  string
	VisionSummary     string
	NewTitle          string
	NewDescription    string
	NewCharacteristics string
	NewSubjectID      *int
	NewSubjectName    string
}

// LoadAnalysisForUpdate загружает строки с новыми параметрами для обновления WB (этап 5).
func (r *ResultsRepo) LoadAnalysisForUpdate(ctx context.Context) ([]AnalysisRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT nm_id, vendor_code, title, subject_name,
		       new_title, new_description, new_characteristics,
		       new_subject_id, new_subject_name
		FROM card_analysis
		WHERE wb_updated = 0
		  AND (new_title != '' OR new_description != '' OR new_characteristics != '')`)
	if err != nil {
		return nil, fmt.Errorf("query for update: %w", err)
	}
	defer rows.Close()

	var result []AnalysisRow
	for rows.Next() {
		var r AnalysisRow
		var newSubjectID sql.NullInt64
		var newSubjectName sql.NullString
		if err := rows.Scan(&r.NmID, &r.VendorCode, &r.Title, &r.SubjectName,
			&r.NewTitle, &r.NewDescription, &r.NewCharacteristics,
			&newSubjectID, &newSubjectName); err != nil {
			return nil, fmt.Errorf("scan analysis row: %w", err)
		}
		if newSubjectID.Valid {
			v := int(newSubjectID.Int64)
			r.NewSubjectID = &v
		}
		if newSubjectName.Valid {
			r.NewSubjectName = newSubjectName.String
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// LoadVisionDiscrepancies возвращает nm_id карточек с vision_has_discrepancy = 1, не обработанных Stage 4.
func (r *ResultsRepo) LoadVisionDiscrepancies(ctx context.Context) ([]int, error) {
	rows, err := r.db.QueryContext(ctx,
		"SELECT nm_id FROM card_analysis WHERE vision_has_discrepancy = 1 AND generate_done = 0")
	if err != nil {
		return nil, fmt.Errorf("query vision discrepancies: %w", err)
	}
	defer rows.Close()

	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan nm_id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// VisionAnalysisRow — данные Vision анализа для генерации новых параметров.
type VisionAnalysisRow struct {
	NmID              int
	VendorCode        string
	Title             string
	VisionProductType string
	VisionAttributes  string
	VisionSummary     string
}

// LoadAnalysisForVision загружает данные Vision анализа для этапа 4.
func (r *ResultsRepo) LoadAnalysisForVision(ctx context.Context, nmIDs []int) ([]VisionAnalysisRow, error) {
	if len(nmIDs) == 0 {
		return nil, nil
	}

	ph := make([]string, len(nmIDs))
	args := make([]any, len(nmIDs))
	for i, id := range nmIDs {
		ph[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT nm_id, vendor_code, title,
		       COALESCE(vision_product_type, ''),
		       COALESCE(vision_attributes, ''),
		       COALESCE(vision_summary, '')
		FROM card_analysis
		WHERE nm_id IN (%s)`, strings.Join(ph, ","))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query vision analysis: %w", err)
	}
	defer rows.Close()

	var result []VisionAnalysisRow
	for rows.Next() {
		var r VisionAnalysisRow
		if err := rows.Scan(&r.NmID, &r.VendorCode, &r.Title,
			&r.VisionProductType, &r.VisionAttributes, &r.VisionSummary); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// Stats возвращает сводку по таблице card_analysis.
func (r *ResultsRepo) Stats(ctx context.Context) (total, textChecked, textDiscrepancy, visionChecked, visionDiscrepancy, generated, wbUpdated int, err error) {
	err = r.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       SUM(CASE WHEN text_checked_at IS NOT NULL THEN 1 ELSE 0 END),
		       SUM(CASE WHEN text_has_discrepancy = 1 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN vision_checked_at IS NOT NULL THEN 1 ELSE 0 END),
		       SUM(CASE WHEN vision_has_discrepancy = 1 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN generate_done = 1 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN wb_updated = 1 THEN 1 ELSE 0 END)
		FROM card_analysis`).Scan(&total, &textChecked, &textDiscrepancy, &visionChecked, &visionDiscrepancy, &generated, &wbUpdated)
	return
}

// charcEntry — элемент JSON из new_characteristics.
type charcEntry struct {
	CharcID int    `json:"charc_id"`
	Name    string `json:"name"`
	Value   string `json:"value"`
}

// formatCharacteristicsJSON превращает JSON характеристик в читаемую строку.
func formatCharacteristicsJSON(jsonStr string) string {
	if jsonStr == "" {
		return ""
	}
	var chars []charcEntry
	if err := json.Unmarshal([]byte(jsonStr), &chars); err != nil {
		return jsonStr
	}
	var parts []string
	for _, c := range chars {
		parts = append(parts, c.Name+": "+c.Value)
	}
	return strings.Join(parts, "; ")
}

// xlsxRow хранит данные одной строки анализа для двухпроходного экспорта.
type xlsxRow struct {
	NmID              int
	VendorCode        string
	ProductRating     float64
	FeedbackRating    float64
	MaxVisibility     float64
	PriorityScore     float64
	AvgPosition       float64
	OpenCard30d       int
	Orders30d         int
	TopQuery          string
	TopQueries        string
	Title             string
	SubjectName       string
	TextDone          int
	TextDisc          int
	TextSummary       string
	VisionDone        int
	VisionProductType string
	VisionDisc        int
	VisionSummary     string
	GenerateDone      int
	NewTitle          string
	NewDesc           string
	NewChars          string
	WbUpdated         int
}


// ExportXLSX выгружает card_analysis в XLSX файл с превью фото в первом столбце.
func (r *ResultsRepo) ExportXLSX(ctx context.Context, path string, getPhotos func(ctx context.Context, nmIDs []int) map[int][]byte) (int, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT nm_id, vendor_code, title, subject_name,
		       text_done, text_has_discrepancy, text_summary,
		       vision_done, vision_product_type, vision_has_discrepancy, vision_summary,
		       generate_done, new_title, new_description, new_characteristics,
		       wb_updated,
		       COALESCE(product_rating, 0), COALESCE(feedback_rating, 0),
		       COALESCE(max_visibility, 0), COALESCE(priority_score, 0),
		       COALESCE(avg_position, 0), COALESCE(open_card_30d, 0), COALESCE(orders_30d, 0),
		       COALESCE(top_query, ''), COALESCE(top_queries, '')
		FROM card_analysis
		ORDER BY COALESCE(priority_score, 0) DESC, nm_id`)
	if err != nil {
		return 0, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	// Pass 1: scan all rows into memory, collect nmIDs.
	var data []xlsxRow
	for rows.Next() {
		var (
			nmID                           int
			vendorCode, title, subjectName string
			textDone                       int
			textDiscNull                   sql.NullInt64
			textSummary                    string
			visionDone                     int
			visionProductType              string
			visionDiscNull                 sql.NullInt64
			visionSummary                  string
			generateDone                   int
			newTitle, newDesc, newChars    string
			wbUpdated                      int
			productRating, feedbackRating  float64
			maxVisibility, priorityScore   float64
			avgPosition                    float64
			openCard30d, orders30d         int
			topQuery, topQueries           string
		)
		if err := rows.Scan(&nmID, &vendorCode, &title, &subjectName,
			&textDone, &textDiscNull, &textSummary,
			&visionDone, &visionProductType, &visionDiscNull, &visionSummary,
			&generateDone, &newTitle, &newDesc, &newChars,
			&wbUpdated,
			&productRating, &feedbackRating,
			&maxVisibility, &priorityScore,
			&avgPosition, &openCard30d, &orders30d,
			&topQuery, &topQueries); err != nil {
			return 0, fmt.Errorf("scan row: %w", err)
		}

		textDisc := 0
		if textDiscNull.Valid {
			textDisc = int(textDiscNull.Int64)
		}
		visionDisc := 0
		if visionDiscNull.Valid {
			visionDisc = int(visionDiscNull.Int64)
		}

		data = append(data, xlsxRow{
			NmID: nmID, VendorCode: vendorCode,
			ProductRating: productRating, FeedbackRating: feedbackRating,
			MaxVisibility: maxVisibility, PriorityScore: priorityScore,
			AvgPosition: avgPosition, OpenCard30d: openCard30d, Orders30d: orders30d,
			TopQuery: topQuery, TopQueries: topQueries,
			Title: title, SubjectName: subjectName,
			TextDone: textDone, TextDisc: textDisc, TextSummary: textSummary,
			VisionDone: visionDone, VisionProductType: visionProductType,
			VisionDisc: visionDisc, VisionSummary: visionSummary,
			GenerateDone: generateDone, NewTitle: newTitle, NewDesc: newDesc,
			NewChars: newChars, WbUpdated: wbUpdated,
		})
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("rows: %w", err)
	}

	// Load photos.
	nmIDs := make([]int, len(data))
	for i, d := range data {
		nmIDs[i] = d.NmID
	}
	photos := getPhotos(ctx, nmIDs)

	// Pass 2: write XLSX.
	f := excelize.NewFile()
	sheet := "Card Analysis"
	f.SetSheetName("Sheet1", sheet)

	// Column order:
	//   photo | nm_id | vendor_code | рейтинг WB | рейтинг отзывов | видимость | priority | ср.позиция | открытия | заказы | топ запрос | топ запросы | subject | ...
	headers := []string{
		"photo",
		"nm_id", "vendor_code",
		"рейтинг WB", "рейтинг отзывов",
		"видимость (%)", "priority", "ср. позиция",
		"открытия (30д)", "заказы (30д)",
		"топ запрос", "топ запросы",
		"subject",
		"title (было)", "title (новое)",
		"description (новое)",
		"характеристики (новые)",
		"text: расхождение", "text: описание",
		"vision: тип изделия", "vision: расхождение", "vision: описание",
		"text done", "vision done", "generate done", "wb updated",
	}

	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#E0E0E0"}},
		Alignment: &excelize.Alignment{Horizontal: "center", WrapText: true},
	})

	discStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Color: "#CC0000", Bold: true},
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#FFE0E0"}},
	})

	priorityStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Color: "#006600", Bold: true},
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#E0FFE0"}},
	})

	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, h)
		f.SetCellStyle(sheet, cell, cell, headerStyle)
	}

	// Photo column width.
	f.SetColWidth(sheet, "A", "A", 14.3)

	// Pre-compute data column names and cell references.
	dataColNames := make([]string, len(headers)-1)
	for i := range dataColNames {
		dataColNames[i], _ = excelize.ColumnNumberToName(i + 2)
	}
	maxRow := len(data) + 1
	colCells := make([][]string, len(dataColNames))
	for ci, colName := range dataColNames {
		colCells[ci] = make([]string, maxRow)
		for ri := 0; ri < maxRow; ri++ {
			colCells[ci][ri] = colName + fmt.Sprintf("%d", ri+2)
		}
	}

	for rowNum := 0; rowNum < len(data); rowNum++ {
		d := data[rowNum]
		row := rowNum + 2

		textDiscStr := boolStr(d.TextDisc)
		visionDiscStr := boolStr(d.VisionDisc)

		// Embed photo in column A — NOT CHANGED.
		if photoBytes, ok := photos[d.NmID]; ok && len(photoBytes) > 0 {
			if err := f.AddPictureFromBytes(sheet, fmt.Sprintf("A%d", row), &excelize.Picture{
				Extension: ".jpg",
				File:      photoBytes,
				Format: &excelize.GraphicOptions{
					AltText:             fmt.Sprintf("nm_%d", d.NmID),
					AutoFit:             true,
					AutoFitIgnoreAspect: true,
					Hyperlink:           fmt.Sprintf("https://www.wildberries.ru/catalog/%d/detail.aspx", d.NmID),
					HyperlinkType:       "External",
				},
			}); err != nil {
				fmt.Printf("WARN: embed photo nm_id=%d: %v\n", d.NmID, err)
			}
		}
		f.SetRowHeight(sheet, row, 56.4)

		// Data columns start at column 2 (B).
		vals := []any{
			d.NmID, d.VendorCode,
			d.ProductRating, d.FeedbackRating,
			d.MaxVisibility, d.PriorityScore, d.AvgPosition,
			d.OpenCard30d, d.Orders30d,
			d.TopQuery, d.TopQueries,
			d.SubjectName,
			d.Title, d.NewTitle,
			d.NewDesc,
			formatCharacteristicsJSON(d.NewChars),
			textDiscStr, d.TextSummary,
			d.VisionProductType, visionDiscStr, d.VisionSummary,
			d.TextDone, d.VisionDone, d.GenerateDone, d.WbUpdated,
		}

		ri := rowNum
		for i, v := range vals {
			f.SetCellValue(sheet, colCells[i][ri], v)
			// text discrepancy = col index 16 (0-based)
			if i == 16 && textDiscStr == "Да" {
				f.SetCellStyle(sheet, colCells[i][ri], colCells[i][ri], discStyle)
			}
			// vision discrepancy = col index 19
			if i == 19 && visionDiscStr == "Да" {
				f.SetCellStyle(sheet, colCells[i][ri], colCells[i][ri], discStyle)
			}
			// product rating < 5 = col index 2
			if i == 2 && d.ProductRating > 0 && d.ProductRating < 5.0 {
				f.SetCellStyle(sheet, colCells[i][ri], colCells[i][ri], discStyle)
			}
			// feedback rating < 4 = col index 3
			if i == 3 && d.FeedbackRating > 0 && d.FeedbackRating < 4.0 {
				f.SetCellStyle(sheet, colCells[i][ri], colCells[i][ri], discStyle)
			}
			// priority score > 1.0 = col index 5 → green
			if i == 5 && d.PriorityScore > 1.0 {
				f.SetCellStyle(sheet, colCells[i][ri], colCells[i][ri], priorityStyle)
			}
		}
	}

	// Column widths.
	// Default 18 for all data columns.
	for i := 1; i < len(headers); i++ {
		col, _ := excelize.ColumnNumberToName(i + 1)
		f.SetColWidth(sheet, col, col, 18)
	}
	// Narrow numeric columns: C(nm_id) D(vendor) E(рейтинг WB) F(рейтинг отзывов) G(видимость) H(priority) I(ср.позиция)
	for _, idx := range []int{2, 3, 4, 5, 6, 7, 8} {
		col, _ := excelize.ColumnNumberToName(idx + 1)
		f.SetColWidth(sheet, col, col, 12)
	}
	// Wider: J(открытия) K(заказы) — 14
	for _, idx := range []int{9, 10} {
		col, _ := excelize.ColumnNumberToName(idx + 1)
		f.SetColWidth(sheet, col, col, 14)
	}
	// Wide text columns: L(топ запрос) M(топ запросы) N(subject) O(title было) P(title новое) Q(description) R(характеристики) T(text описание) W(vision описание)
	for _, idx := range []int{11, 12, 13, 14, 15, 16, 17, 19, 22} {
		col, _ := excelize.ColumnNumberToName(idx + 1)
		f.SetColWidth(sheet, col, col, 40)
	}

	if err := f.SaveAs(path); err != nil {
		return 0, fmt.Errorf("save xlsx: %w", err)
	}
	return len(data), nil
}

func boolStr(v int) string {
	if v == 1 {
		return "Да"
	}
	return "Нет"
}

// FilterByThresholds возвращает nm_id карточек, подходящих под пороговые фильтры.
// Порог = 0 → условие не применяется.
// Артикулы без строки в card_analysis пропускаются (включаются в результат).
func (r *ResultsRepo) FilterByThresholds(ctx context.Context, nmIDs []int, f FilterConfig) ([]int, error) {
	if !f.hasThresholds() || len(nmIDs) == 0 {
		return nmIDs, nil
	}

	ph := make([]string, len(nmIDs))
	args := make([]any, len(nmIDs))
	for i, id := range nmIDs {
		ph[i] = "?"
		args[i] = id
	}

	var conds []string
	if f.MaxProductRating > 0 {
		conds = append(conds, "(product_rating < ? OR product_rating = 0)")
		args = append(args, f.MaxProductRating)
	}
	if f.MaxFeedbackRating > 0 {
		conds = append(conds, "(feedback_rating < ? OR feedback_rating = 0)")
		args = append(args, f.MaxFeedbackRating)
	}
	if f.MaxVisibility > 0 {
		conds = append(conds, "(max_visibility < ? OR max_visibility = 0)")
		args = append(args, f.MaxVisibility)
	}

	query := fmt.Sprintf(
		"SELECT nm_id FROM card_analysis WHERE nm_id IN (%s) AND %s",
		strings.Join(ph, ","),
		strings.Join(conds, " AND "),
	)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query thresholds: %w", err)
	}
	defer rows.Close()

	passing := make(map[int]bool)
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		passing[id] = true
	}

	var result []int
	for _, id := range nmIDs {
		if passing[id] {
			result = append(result, id)
		}
	}
	return result, rows.Err()
}
