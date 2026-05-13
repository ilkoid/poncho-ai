package main

import (
	"database/sql"
	"fmt"
	"log"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// CardData holds all data needed to score a single card.
type CardData struct {
	NmID        int
	SubjectID   int
	SubjectName string
	VendorCode  string
	Brand       string
	Title       string
	Description string
	Video       string
	DimLength   float64
	DimWidth    float64
	DimHeight   float64
	PhotoCount  int
	SizeCount   int
	Year        string // extracted from created_at
	CharIDs     map[int]bool
}

// FeedbackStats holds aggregated feedback data for a product.
type FeedbackStats struct {
	AvgRating     float64
	FeedbackCount int
	AnswerRate    float64 // 0.0-1.0
	HasFeedbacks  bool
}

// SourceRepo reads card data from SQLite (read-only).
type SourceRepo struct {
	db *sql.DB
}

// OpenSourceDB opens a read-only connection to the source database.
func OpenSourceDB(dbPath string) (*SourceRepo, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&_busy_timeout=5000", dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open source db: %w", err)
	}
	return &SourceRepo{db: db}, nil
}

// Close closes the database connection.
func (r *SourceRepo) Close() error {
	return r.db.Close()
}

// LoadCards loads all card data with photo and size counts.
// Filters by year if yearFilter > 0, and by subject if subjectFilter is non-empty.
func (r *SourceRepo) LoadCards(yearFilter int, subjectFilter string) ([]CardData, error) {
	var where []string
	var args []any

	if yearFilter > 0 {
		where = append(where, "SUBSTR(c.created_at, 1, 4) = ?")
		args = append(args, fmt.Sprintf("%d", yearFilter))
	}
	if subjectFilter != "" {
		where = append(where, "c.subject_name = ?")
		args = append(args, subjectFilter)
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT
			c.nm_id, c.subject_id, c.subject_name, c.vendor_code, c.brand,
			c.title, c.description, c.video,
			c.dim_length, c.dim_width, c.dim_height,
			COALESCE(p.photo_count, 0),
			COALESCE(s.size_count, 0),
			SUBSTR(c.created_at, 1, 4)
		FROM cards c
		LEFT JOIN (SELECT nm_id, COUNT(*) as photo_count FROM card_photos GROUP BY nm_id) p ON c.nm_id = p.nm_id
		LEFT JOIN (SELECT nm_id, COUNT(DISTINCT chrt_id) as size_count FROM card_sizes GROUP BY nm_id) s ON c.nm_id = s.nm_id
		%s
		ORDER BY c.nm_id
	`, whereClause)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query cards: %w", err)
	}
	defer rows.Close()

	var cards []CardData
	for rows.Next() {
		var c CardData
		if err := rows.Scan(&c.NmID, &c.SubjectID, &c.SubjectName, &c.VendorCode, &c.Brand,
			&c.Title, &c.Description, &c.Video,
			&c.DimLength, &c.DimWidth, &c.DimHeight,
			&c.PhotoCount, &c.SizeCount, &c.Year); err != nil {
			return nil, fmt.Errorf("scan card: %w", err)
		}
		c.CharIDs = make(map[int]bool)
		cards = append(cards, c)
	}
	return cards, rows.Err()
}

// LoadCharacteristics loads all card characteristics and fills CharIDs in cards map.
// Returns a set of all unique subject IDs found.
func (r *SourceRepo) LoadCharacteristics(cardsMap map[int]*CardData) (map[int]bool, error) {
	rows, err := r.db.Query("SELECT nm_id, char_id FROM card_characteristics ORDER BY nm_id")
	if err != nil {
		return nil, fmt.Errorf("query characteristics: %w", err)
	}
	defer rows.Close()

	subjects := make(map[int]bool)
	var count int
	for rows.Next() {
		var nmID, charID int
		if err := rows.Scan(&nmID, &charID); err != nil {
			return nil, fmt.Errorf("scan characteristic: %w", err)
		}
		if card, ok := cardsMap[nmID]; ok {
			card.CharIDs[charID] = true
			subjects[card.SubjectID] = true
		}
		count++
	}
	if count > 0 {
		log.Printf("Loaded %d characteristic entries for %d subjects", count, len(subjects))
	}
	return subjects, rows.Err()
}

// LoadFeedbackStats loads aggregated feedback statistics per product.
func (r *SourceRepo) LoadFeedbackStats() (map[int]*FeedbackStats, error) {
	rows, err := r.db.Query(`
		SELECT
			product_nm_id,
			AVG(CAST(product_valuation AS REAL)),
			COUNT(*),
			1.0 * SUM(CASE WHEN answer_text IS NOT NULL AND answer_text != '' THEN 1 ELSE 0 END) / COUNT(*)
		FROM feedbacks
		WHERE product_valuation IS NOT NULL
		GROUP BY product_nm_id
	`)
	if err != nil {
		return nil, fmt.Errorf("query feedback stats: %w", err)
	}
	defer rows.Close()

	stats := make(map[int]*FeedbackStats)
	for rows.Next() {
		var nmID int
		var s FeedbackStats
		if err := rows.Scan(&nmID, &s.AvgRating, &s.FeedbackCount, &s.AnswerRate); err != nil {
			return nil, fmt.Errorf("scan feedback stats: %w", err)
		}
		s.HasFeedbacks = true
		stats[nmID] = &s
	}
	return stats, rows.Err()
}

// LoadSubjectAverages returns average characteristic count per subject.
func (r *SourceRepo) LoadSubjectAverages() (map[int]float64, error) {
	rows, err := r.db.Query(`
		SELECT subject_id, AVG(char_count)
		FROM (SELECT c.subject_id, c.nm_id, COUNT(*) as char_count
		      FROM card_characteristics cc JOIN cards c ON cc.nm_id = c.nm_id
		      GROUP BY c.nm_id)
		GROUP BY subject_id
	`)
	if err != nil {
		return nil, fmt.Errorf("query subject averages: %w", err)
	}
	defer rows.Close()

	avgs := make(map[int]float64)
	for rows.Next() {
		var subjectID int
		var avg float64
		if err := rows.Scan(&subjectID, &avg); err != nil {
			return nil, fmt.Errorf("scan subject avg: %w", err)
		}
		avgs[subjectID] = avg
	}
	return avgs, rows.Err()
}

// CountCards returns total card count (for progress display).
func (r *SourceRepo) CountCards() (int, error) {
	var count int
	err := r.db.QueryRow("SELECT COUNT(*) FROM cards").Scan(&count)
	return count, err
}

// CharFrequency — частота характеристики в предмете.
// Если char_id встречается у 90%+ карточек предмета — он де-факто «обязательный».
type CharFrequency struct {
	SubjectID int
	CharID    int
	Name      string
	Frequency float64 // 0.0-1.0, доля карточек предмета с этой характеристикой
	CardCount int     // сколько карточек предмета всего
}

// LoadCharFrequencies computes characteristic frequency per subject from local data.
// Replaces WB API dictionary — derived from actual data instead of theoretical recommendations.
func (r *SourceRepo) LoadCharFrequencies() ([]CharFrequency, error) {
	rows, err := r.db.Query(`
		WITH subject_counts AS (
			SELECT subject_id, COUNT(DISTINCT nm_id) as card_count
			FROM cards GROUP BY subject_id
		)
		SELECT
			c.subject_id,
			cc.char_id,
			cc.name,
			1.0 * COUNT(DISTINCT cc.nm_id) / sc.card_count,
			sc.card_count
		FROM card_characteristics cc
		JOIN cards c ON cc.nm_id = c.nm_id
		JOIN subject_counts sc ON c.subject_id = sc.subject_id
		GROUP BY c.subject_id, cc.char_id, cc.name
		HAVING COUNT(DISTINCT cc.nm_id) >= 2
		ORDER BY c.subject_id, COUNT(DISTINCT cc.nm_id) DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query char frequencies: %w", err)
	}
	defer rows.Close()

	var freqs []CharFrequency
	for rows.Next() {
		var f CharFrequency
		if err := rows.Scan(&f.SubjectID, &f.CharID, &f.Name, &f.Frequency, &f.CardCount); err != nil {
			return nil, fmt.Errorf("scan char freq: %w", err)
		}
		freqs = append(freqs, f)
	}
	return freqs, rows.Err()
}
