package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/wb"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/config"

	_ "github.com/mattn/go-sqlite3"
)

// SourceRepo — read-only доступ к wb-sales.db для чтения карточек, фото и характеристик.
type SourceRepo struct {
	db *sql.DB
}

func NewSourceRepo(dbPath string) (*SourceRepo, error) {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open source db: %w", err)
	}
	db.Exec("PRAGMA journal_mode=WAL")
	return &SourceRepo{db: db}, nil
}

func (r *SourceRepo) Close() error { return r.db.Close() }

// CountCards возвращает общее количество карточек в source DB (без фильтров).
func (r *SourceRepo) CountCards(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM cards").Scan(&count)
	return count, err
}

// CardData — данные карточки для текстового анализа (этап 1).
type CardData struct {
	NmID            int
	VendorCode      string
	Title           string
	Description     string
	SubjectID       int
	SubjectName     string
	Characteristics []CardChar
}

// CardChar — одна характеристика карточки.
type CardChar struct {
	CharID int
	Name   string
	Value  string
}

// RatingInfo — рейтинги товара из таблицы products (wb-sales.db).
type RatingInfo struct {
	ProductRating  float64 // 0-10, внутренний рейтинг качества WB
	FeedbackRating float64 // 1-5, звёздочный рейтинг покупателей
}

// LoadRatings загружает рейтинги из products по списку nm_id.
// Если nm_id нет в таблице — возвращает нулевые значения.
func (r *SourceRepo) LoadRatings(ctx context.Context, nmIDs []int) (map[int]RatingInfo, error) {
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
		SELECT nm_id, COALESCE(product_rating, 0), COALESCE(feedback_rating, 0)
		FROM products
		WHERE nm_id IN (%s)
	`, strings.Join(ph, ","))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query ratings: %w", err)
	}
	defer rows.Close()

	result := make(map[int]RatingInfo, len(nmIDs))
	for rows.Next() {
		var nmID int
		var ri RatingInfo
		if err := rows.Scan(&nmID, &ri.ProductRating, &ri.FeedbackRating); err != nil {
			return nil, fmt.Errorf("scan rating: %w", err)
		}
		result[nmID] = ri
	}
	return result, rows.Err()
}

// PhotoData — URL фото карточки.
type PhotoData struct {
	NmID int
	URL  string // big (900x1200)
}

// buildFilterClause строит WHERE-условия для карточек по FilterConfig.
// Возвращает (where-фрагменты, args). Если вернулся nil where — нет условий.
func (r *SourceRepo) buildFilterClause(ctx context.Context, filter FilterConfig) ([]string, []any) {
	var where []string
	var args []any

	// Приоритет: nm_ids > vendor_codes > allowed_years
	if len(filter.NmIDs) > 0 {
		ph := make([]string, len(filter.NmIDs))
		for i, id := range filter.NmIDs {
			ph[i] = "?"
			args = append(args, id)
		}
		where = append(where, "c.nm_id IN ("+strings.Join(ph, ",")+")")
	} else if len(filter.VendorCodes) > 0 {
		ph := make([]string, len(filter.VendorCodes))
		for i, vc := range filter.VendorCodes {
			ph[i] = "?"
			args = append(args, vc)
		}
		where = append(where, "c.vendor_code IN ("+strings.Join(ph, ",")+")")
	} else if len(filter.AllowedYears) > 0 {
		entries := r.loadYearEntries(ctx)
		filtered := config.FilterNmIDsByYear(entries, filter.AllowedYears)
		if len(filtered) == 0 {
			return nil, nil
		}
		ph := make([]string, len(filtered))
		for i, id := range filtered {
			ph[i] = "?"
			args = append(args, id)
		}
		where = append(where, "c.nm_id IN ("+strings.Join(ph, ",")+")")
	}

	if len(filter.Seasons) > 0 {
		// Сезон хранится в card_characteristics как JSON-массив: ["демисезон"]
		// Значения могут быть в разном регистре — сравниваем через LOWER().
		ph := make([]string, len(filter.Seasons))
		for i, s := range filter.Seasons {
			ph[i] = "LOWER(je.value) = LOWER(?)"
			args = append(args, s)
		}
		where = append(where, `c.nm_id IN (
			SELECT cc.nm_id
			FROM card_characteristics cc, json_each(cc.json_value) je
			WHERE cc.name = 'Сезон' AND (`+strings.Join(ph, " OR ")+`)
		)`)
	}

	if filter.Subject != "" {
		where = append(where, "LOWER(c.subject_name) = LOWER(?)")
		args = append(args, filter.Subject)
	}

	if len(filter.SubjectIDs) > 0 {
		ph := make([]string, len(filter.SubjectIDs))
		for i, id := range filter.SubjectIDs {
			ph[i] = "?"
			args = append(args, id)
		}
		where = append(where, "c.subject_id IN ("+strings.Join(ph, ",")+")")
	}

	if len(filter.ExcludeLengths) > 0 {
		for _, l := range filter.ExcludeLengths {
			where = append(where, fmt.Sprintf("LENGTH(c.vendor_code) != %d", l))
		}
	}

	if filter.InStock {
		where = append(where, `c.nm_id IN (
			SELECT nm_id
			FROM stocks_daily_warehouses
			WHERE snapshot_date = (SELECT MAX(snapshot_date) FROM stocks_daily_warehouses)
			GROUP BY nm_id
			HAVING SUM(quantity) > 0
		)`)
	}

	return where, args
}

// CountCardsFiltered возвращает количество карточек по FilterConfig (без загрузки данных).
func (r *SourceRepo) CountCardsFiltered(ctx context.Context, filter FilterConfig) (int, error) {
	where, args := r.buildFilterClause(ctx, filter)
	if where == nil {
		return 0, nil
	}

	query := "SELECT COUNT(*) FROM cards c"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	var count int
	if err := r.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count cards: %w", err)
	}
	return count, nil
}

// LoadCardsForAnalysis загружает карточки с фильтрацией по году, сезону, предмету и vendor_codes.
func (r *SourceRepo) LoadCardsForAnalysis(ctx context.Context, filter FilterConfig) ([]CardData, error) {
	where, args := r.buildFilterClause(ctx, filter)
	if where == nil {
		return nil, nil
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT c.nm_id, c.vendor_code, c.title, c.description, c.subject_id, c.subject_name
		FROM cards c
		%s
		ORDER BY c.vendor_code
	`, whereClause)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query cards: %w", err)
	}
	defer rows.Close()

	var cards []CardData
	for rows.Next() {
		var c CardData
		if err := rows.Scan(&c.NmID, &c.VendorCode, &c.Title, &c.Description, &c.SubjectID, &c.SubjectName); err != nil {
			return nil, fmt.Errorf("scan card: %w", err)
		}
		cards = append(cards, c)
	}
	return cards, rows.Err()
}

// LoadCharacteristics загружает характеристики для указанных nm_id.
func (r *SourceRepo) LoadCharacteristics(ctx context.Context, nmIDs []int) (map[int][]CardChar, error) {
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
		SELECT nm_id, char_id, name, json_value
		FROM card_characteristics
		WHERE nm_id IN (%s)
	`, strings.Join(ph, ","))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query characteristics: %w", err)
	}
	defer rows.Close()

	result := make(map[int][]CardChar)
	for rows.Next() {
		var nmID, charID int
		var name, jsonValue string
		if err := rows.Scan(&nmID, &charID, &name, &jsonValue); err != nil {
			return nil, fmt.Errorf("scan characteristic: %w", err)
		}
		result[nmID] = append(result[nmID], CardChar{
			CharID: charID,
			Name:   name,
			Value:  jsonValue,
		})
	}

	return result, rows.Err()
}

// LoadTitleDescription возвращает текущие title и description карточки из source DB.
func (r *SourceRepo) LoadTitleDescription(ctx context.Context, nmID int) (string, string, error) {
	var title, desc string
	err := r.db.QueryRowContext(ctx,
		"SELECT COALESCE(title,''), COALESCE(description,'') FROM cards WHERE nm_id = ?", nmID,
	).Scan(&title, &desc)
	return title, desc, err
}

	// LoadSizes загружает размеры для указанных nm_id из card_sizes.
func (r *SourceRepo) LoadSizes(ctx context.Context, nmIDs []int) (map[int][]wb.CardSize, error) {
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
		SELECT nm_id, chrt_id, tech_size, wb_size, skus_json
		FROM card_sizes
		WHERE nm_id IN (%s)
	`, strings.Join(ph, ","))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query sizes: %w", err)
	}
	defer rows.Close()

	result := make(map[int][]wb.CardSize)
	for rows.Next() {
		var nmID, chrtID int
		var techSize, wbSize, skusJSON string
		if err := rows.Scan(&nmID, &chrtID, &techSize, &wbSize, &skusJSON); err != nil {
			return nil, fmt.Errorf("scan size: %w", err)
		}
		var skus []string
		if err := json.Unmarshal([]byte(skusJSON), &skus); err != nil {
			skus = []string{}
		}
		result[nmID] = append(result[nmID], wb.CardSize{
			ChrtID:   chrtID,
			TechSize: techSize,
			WBSize:   wbSize,
			Skus:     skus,
		})
	}
	return result, rows.Err()
}

// LoadPhotos загружает URL фото для указанных nm_id, сортированные по порядку.
func (r *SourceRepo) LoadPhotos(ctx context.Context, nmIDs []int, limitPerCard int) (map[int][]string, error) {
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
		SELECT nm_id, big
		FROM card_photos
		WHERE nm_id IN (%s)
		ORDER BY nm_id, rowid
	`, strings.Join(ph, ","))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query photos: %w", err)
	}
	defer rows.Close()

	result := make(map[int][]string)
	for rows.Next() {
		var nmID int
		var bigURL string
		if err := rows.Scan(&nmID, &bigURL); err != nil {
			return nil, fmt.Errorf("scan photo: %w", err)
		}
		if len(result[nmID]) < limitPerCard {
			result[nmID] = append(result[nmID], bigURL)
		}
	}
	return result, rows.Err()
}

// LoadThumbnailURLs возвращает URL первой tm-миниатюры для каждого nm_id.
func (r *SourceRepo) LoadThumbnailURLs(ctx context.Context, nmIDs []int) (map[int]string, error) {
	if len(nmIDs) == 0 {
		return nil, nil
	}

	r.db.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_card_photos_nm_id_rowid ON card_photos(nm_id, id)")

	ph := make([]string, len(nmIDs))
	args := make([]any, len(nmIDs))
	for i, id := range nmIDs {
		ph[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT p.nm_id, p.tm
		FROM card_photos p
		WHERE p.id IN (
			SELECT MIN(id) FROM card_photos WHERE nm_id IN (%s) GROUP BY nm_id
		)
	`, strings.Join(ph, ","))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query thumbnails: %w", err)
	}
	defer rows.Close()

	result := make(map[int]string, len(nmIDs))
	for rows.Next() {
		var nmID int
		var tmURL string
		if err := rows.Scan(&nmID, &tmURL); err != nil {
			return nil, fmt.Errorf("scan thumbnail: %w", err)
		}
		if tmURL != "" {
			result[nmID] = tmURL
		}
	}
	return result, rows.Err()
}

func (r *SourceRepo) loadYearEntries(ctx context.Context) []config.YearEntry {
	rows, err := r.db.QueryContext(ctx, "SELECT nm_id, vendor_code FROM cards")
	if err != nil {
		return nil
	}
	defer rows.Close()

	var entries []config.YearEntry
	for rows.Next() {
		var e config.YearEntry
		if err := rows.Scan(&e.NmID, &e.VendorCode); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries
}

// LoadLatestStockDate возвращает последнюю дату снапшота остатков.
func (r *SourceRepo) LoadLatestStockDate(ctx context.Context) string {
	var date string
	r.db.QueryRowContext(ctx, "SELECT MAX(snapshot_date) FROM stocks_daily_warehouses").Scan(&date)
	return date
}

// SearchQuery — поисковый запрос из search_queries_daily.
type SearchQuery struct {
	Text      string
	Frequency int
	OpenCard  int
	Orders    int
}

// LoadTopSearchQueries загружает топ поисковых запросов по nm_id за 30 дней.
// Возвращает map[nm_id][]SearchQuery, отсортированных по open_card DESC, orders DESC.
func (r *SourceRepo) LoadTopSearchQueries(ctx context.Context, nmIDs []int, limit int) (map[int][]SearchQuery, error) {
	if len(nmIDs) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}

	ph := make([]string, len(nmIDs))
	args := make([]any, len(nmIDs))
	for i, id := range nmIDs {
		ph[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT nm_id, search_text,
		       SUM(COALESCE(frequency, 0)) AS total_freq,
		       SUM(COALESCE(open_card, 0)) AS total_opens,
		       SUM(COALESCE(orders, 0)) AS total_orders
		FROM search_queries_daily
		WHERE nm_id IN (%s)
		  AND snapshot_date >= DATE('now', '-30 day')
		GROUP BY nm_id, search_text
		ORDER BY total_opens DESC, total_orders DESC
	`, strings.Join(ph, ","))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query search queries: %w", err)
	}
	defer rows.Close()

	result := make(map[int][]SearchQuery)
	for rows.Next() {
		var nmID int
		var sq SearchQuery
		if err := rows.Scan(&nmID, &sq.Text, &sq.Frequency, &sq.OpenCard, &sq.Orders); err != nil {
			return nil, fmt.Errorf("scan search query: %w", err)
		}
		if len(result[nmID]) < limit {
			result[nmID] = append(result[nmID], sq)
		}
	}
	return result, rows.Err()
}

// LoadAllSubjects загружает все уникальные предметы WB из source DB.
func (r *SourceRepo) LoadAllSubjects(ctx context.Context) ([]SubjectEntry, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT subject_id, subject_name
		FROM cards
		ORDER BY subject_name
	`)
	if err != nil {
		return nil, fmt.Errorf("query all subjects: %w", err)
	}
	defer rows.Close()

	var result []SubjectEntry
	for rows.Next() {
		var e SubjectEntry
		if err := rows.Scan(&e.SubjectID, &e.SubjectName); err != nil {
			return nil, fmt.Errorf("scan subject: %w", err)
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// SearchSubjects ищет предметы WB по подстроке в source DB (cards.subject_name).
func (r *SourceRepo) SearchSubjects(ctx context.Context, query string, limit int) ([]SubjectEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT subject_id, subject_name
		FROM cards
		WHERE subject_name LIKE '%' || ? || '%'
		ORDER BY subject_name
		LIMIT ?
	`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("query subjects: %w", err)
	}
	defer rows.Close()

	var result []SubjectEntry
	for rows.Next() {
		var e SubjectEntry
		if err := rows.Scan(&e.SubjectID, &e.SubjectName); err != nil {
			return nil, fmt.Errorf("scan subject: %w", err)
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// CharDictRepo — read-only доступ к кэшу характеристик WB (char-dict-cache.db).
type CharDictRepo struct {
	db *sql.DB
}

func NewCharDictRepo(dbPath string) (*CharDictRepo, error) {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open char-dict-cache: %w", err)
	}
	return &CharDictRepo{db: db}, nil
}

func (r *CharDictRepo) Close() error { return r.db.Close() }

// SubjectEntry — предмет WB из справочника.
type SubjectEntry struct {
	SubjectID   int
	SubjectName string
}

// CharEntry — характеристика предмета из справочника.
type CharEntry struct {
	CharcID   int
	Name      string
	Required  bool
	Popular   bool
	MaxCount  int
}


// LoadCharacteristicsForSubject загружает характеристики предмета из кэша.
func (r *CharDictRepo) LoadCharacteristicsForSubject(ctx context.Context, subjectID int) ([]CharEntry, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT charc_id, name, required, popular, max_count
		FROM subject_char_dictionary
		WHERE subject_id = ?
		ORDER BY popular DESC, required DESC
	`, subjectID)
	if err != nil {
		return nil, fmt.Errorf("query characteristics: %w", err)
	}
	defer rows.Close()

	var chars []CharEntry
	for rows.Next() {
		var c CharEntry
		var required, popular int
		if err := rows.Scan(&c.CharcID, &c.Name, &required, &popular, &c.MaxCount); err != nil {
			return nil, fmt.Errorf("scan characteristic: %w", err)
		}
		c.Required = required == 1
		c.Popular = popular == 1
		chars = append(chars, c)
	}
	return chars, rows.Err()
}

// LoadSubjectNames загружает все предметы с их названиями из кэша.
// Названия берём из отдельной таблицы subjects если есть, иначе из char-записей.
func (r *CharDictRepo) LoadAllSubjectIDs(ctx context.Context) ([]int, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT subject_id FROM subject_char_dictionary
	`)
	if err != nil {
		return nil, fmt.Errorf("query all subjects: %w", err)
	}
	defer rows.Close()

	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SearchMetric — агрегированные поисковые метрики за 30 дней.
type SearchMetric struct {
	OpenCard30d int
	Orders30d   int
}

// LoadVisibility загружает максимальную видимость за 14 дней по списку nm_id.
func (r *SourceRepo) LoadVisibility(ctx context.Context, nmIDs []int) (map[int]float64, error) {
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
		SELECT nm_id, MAX(visibility)
		FROM search_queries_daily
		WHERE nm_id IN (%s)
		  AND snapshot_date >= DATE('now', '-14 day')
		GROUP BY nm_id
	`, strings.Join(ph, ","))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query visibility: %w", err)
	}
	defer rows.Close()

	result := make(map[int]float64, len(nmIDs))
	for rows.Next() {
		var nmID int
		var vis float64
		if err := rows.Scan(&nmID, &vis); err != nil {
			return nil, fmt.Errorf("scan visibility: %w", err)
		}
		result[nmID] = vis
	}
	return result, rows.Err()
}

// LoadSearchMetrics загружает агрегированные поисковые метрики (open_card, orders) за 30 дней.
func (r *SourceRepo) LoadSearchMetrics(ctx context.Context, nmIDs []int) (map[int]SearchMetric, error) {
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
		SELECT nm_id,
		       SUM(COALESCE(open_card, 0)),
		       SUM(COALESCE(orders, 0))
		FROM search_queries_daily
		WHERE nm_id IN (%s)
		  AND snapshot_date >= DATE('now', '-30 day')
		GROUP BY nm_id
	`, strings.Join(ph, ","))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query search metrics: %w", err)
	}
	defer rows.Close()

	result := make(map[int]SearchMetric, len(nmIDs))
	for rows.Next() {
		var nmID int
		var sm SearchMetric
		if err := rows.Scan(&nmID, &sm.OpenCard30d, &sm.Orders30d); err != nil {
			return nil, fmt.Errorf("scan search metric: %w", err)
		}
		result[nmID] = sm
	}
	return result, rows.Err()
}
