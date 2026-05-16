package main

import (
	"context"
	"database/sql"
	"fmt"
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
	return &SourceRepo{db: db}, nil
}

func (r *SourceRepo) Close() error { return r.db.Close() }

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

// PhotoData — URL фото карточки.
type PhotoData struct {
	NmID int
	URL  string // big (900x1200)
}

// LoadCardsForAnalysis загружает карточки с фильтрацией по году, сезону, предмету и vendor_codes.
func (r *SourceRepo) LoadCardsForAnalysis(ctx context.Context, filter FilterConfig) ([]CardData, error) {
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

	if filter.Subject != "" {
		where = append(where, "c.subject_name = ?")
		args = append(args, filter.Subject)
	}

	// Исключаем vendor_code указанных длин (5=мусор, 6=устаревшие)
	if len(filter.ExcludeLengths) > 0 {
		for _, l := range filter.ExcludeLengths {
			where = append(where, fmt.Sprintf("LENGTH(c.vendor_code) != %d", l))
		}
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
