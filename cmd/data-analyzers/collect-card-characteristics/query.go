package main

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/filter"

	_ "modernc.org/sqlite"
)

// CharRow holds aggregated characteristic data for one char within one subject.
type CharRow struct {
	CharID    int
	Name      string   // characteristic name (e.g. "Сезон", "Цвет")
	Values    []string // sorted unique values (deduplicated)
	CardCount int      // how many cards have this characteristic
}

// SubjectData holds all characteristics for one WB subject.
type SubjectData struct {
	SubjectName     string
	SubjectID       int
	CardCount       int       // total cards in this subject (from cards table)
	Characteristics []CharRow // sorted by CardCount DESC
}

// SubjectEntry is a lightweight subject record for --list-subjects.
type SubjectEntry struct {
	SubjectID   int
	SubjectName string
}

// SourceRepo provides read-only access to wb-sales.db.
type SourceRepo struct {
	db *sql.DB
}

// NewSourceRepo opens the source database in read-only mode.
func NewSourceRepo(dbPath string) (*SourceRepo, error) {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro&_busy_timeout=5000", dbPath))
	if err != nil {
		return nil, fmt.Errorf("open source db: %w", err)
	}
	return &SourceRepo{db: db}, nil
}

// Close closes the source database.
func (r *SourceRepo) Close() error {
	return r.db.Close()
}

// LoadCharacteristics collects all historical characteristic values across WB subjects.
//
// Strategy: single query with GROUP_CONCAT(DISTINCT) over json_each() to unnest JSON arrays,
// then double deduplication: SQL-level DISTINCT + Go-level map[string]struct{}.
// Returns data sorted by subject_name ASC, characteristics sorted by card_count DESC.
func (r *SourceRepo) LoadCharacteristics(ctx context.Context, f *filter.Filter) ([]SubjectData, error) {
	sqlResult, err := f.BuildSQL(filter.SQLConfig{CardsAlias: "c"})
	if err != nil {
		return nil, fmt.Errorf("build filter SQL: %w", err)
	}

	// --- Build WHERE clause ---
	var whereConds []string
	var args []any

	if sqlResult.Where != "" {
		whereConds = append(whereConds, sqlResult.Where)
		args = append(args, sqlResult.Args...)
	}
	whereConds = append(whereConds, "c.subject_name IS NOT NULL", "c.subject_name != ''")
	whereClause := "WHERE " + strings.Join(whereConds, " AND ")

	// --- Build JOINs ---
	joinClause := strings.Join(sqlResult.JOINs, " ")
	if joinClause != "" {
		joinClause = " " + joinClause
	}

	// --- Main query: one row per (subject, characteristic) with aggregated values ---
	mainQuery := fmt.Sprintf(`
		SELECT c.subject_name,
		       c.subject_id,
		       cc.char_id,
		       cc.name,
		       GROUP_CONCAT(DISTINCT je.value) AS all_values,
		       COUNT(DISTINCT c.nm_id)         AS card_count
		FROM cards c
		JOIN card_characteristics cc ON c.nm_id = cc.nm_id,
		     json_each(cc.json_value) je
		%s
		%s
		GROUP BY c.subject_name, c.subject_id, cc.char_id, cc.name
		ORDER BY c.subject_name, card_count DESC
	`, joinClause, whereClause)

	rows, err := r.db.QueryContext(ctx, mainQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query characteristics: %w", err)
	}
	defer rows.Close()

	// --- Group into SubjectData ---
	subjectMap := make(map[string]*SubjectData) // key: subject_name
	var subjectOrder []string                    // preserve insertion order

	for rows.Next() {
		var subjectName string
		var subjectID, charID, cardCount int
		var charName, allValues string

		if err := rows.Scan(&subjectName, &subjectID, &charID, &charName, &allValues, &cardCount); err != nil {
			return nil, fmt.Errorf("scan characteristic row: %w", err)
		}

		sd, exists := subjectMap[subjectName]
		if !exists {
			sd = &SubjectData{
				SubjectName: subjectName,
				SubjectID:   subjectID,
			}
			subjectMap[subjectName] = sd
			subjectOrder = append(subjectOrder, subjectName)
		}

		// Parse and deduplicate values (SQL DISTINCT is good, but Go map guarantees it)
		values := dedupAndSort(allValues)

		sd.Characteristics = append(sd.Characteristics, CharRow{
			CharID:    charID,
			Name:      charName,
			Values:    values,
			CardCount: cardCount,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate characteristics: %w", err)
	}

	// --- Auxiliary query: total card count per subject ---
	countQuery := fmt.Sprintf(`
		SELECT c.subject_name, c.subject_id, COUNT(DISTINCT c.nm_id)
		FROM cards c
		%s
		%s
		GROUP BY c.subject_name, c.subject_id
	`, joinClause, whereClause)

	countRows, err := r.db.QueryContext(ctx, countQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query subject card counts: %w", err)
	}
	defer countRows.Close()

	for countRows.Next() {
		var name string
		var id, count int
		if err := countRows.Scan(&name, &id, &count); err != nil {
			return nil, fmt.Errorf("scan subject count: %w", err)
		}
		if sd, ok := subjectMap[name]; ok {
			sd.CardCount = count
		}
	}
	if err := countRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subject counts: %w", err)
	}

	// --- Build ordered result ---
	result := make([]SubjectData, 0, len(subjectOrder))
	for _, name := range subjectOrder {
		result = append(result, *subjectMap[name])
	}

	return result, nil
}

// dedupAndSort splits a comma-separated values string, deduplicates, and sorts alphabetically.
func dedupAndSort(csv string) []string {
	if csv == "" {
		return nil
	}

	seen := make(map[string]struct{})
	for _, v := range strings.Split(csv, ",") {
		v = strings.TrimSpace(v)
		if v != "" {
			seen[v] = struct{}{}
		}
	}

	result := make([]string, 0, len(seen))
	for v := range seen {
		result = append(result, v)
	}
	sort.Strings(result)
	return result
}

// LoadAllSubjects loads all distinct WB subjects from cards table.
func (r *SourceRepo) LoadAllSubjects(ctx context.Context) ([]SubjectEntry, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT subject_id, subject_name
		FROM cards
		WHERE subject_name IS NOT NULL AND subject_name != ''
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
