package filter

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

const testDBPath = "/var/db/wb-sales.db"

// openTestDB opens a read-only connection to the test database.
// Skips the test if the database doesn't exist.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	if _, err := os.Stat(testDBPath); os.IsNotExist(err) {
		t.Skipf("test database not found: %s", testDBPath)
	}
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro", testDBPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	return db
}

// dbCard holds card data loaded from the real database for in-memory filtering.
// Implements both Filterable and Filterable1C.
type dbCard struct {
	nmID            int
	vendorCode      string
	subjectID       int
	subjectName     string
	seasons         []string
	oneCType        string
	categoryLevel1  string
	categoryLevel2  string
	articleBlocked  bool
	hasOneC         bool // whether 1C data was found for this card
}

func (c dbCard) GetNmID() int            { return c.nmID }
func (c dbCard) GetVendorCode() string    { return c.vendorCode }
func (c dbCard) GetSubjectID() int        { return c.subjectID }
func (c dbCard) GetSubjectName() string   { return c.subjectName }
func (c dbCard) GetSeasons() []string     { return c.seasons }
func (c dbCard) GetOneCType() string      { return c.oneCType }
func (c dbCard) GetCategoryLevel1() string { return c.categoryLevel1 }
func (c dbCard) GetCategoryLevel2() string { return c.categoryLevel2 }
func (c dbCard) IsArticleBlocked() bool   { return c.articleBlocked }

// loadCards loads all cards from the database with joined 1C data.
func loadCards(t *testing.T, db *sql.DB) []dbCard {
	t.Helper()

	// Load basic card data
	rows, err := db.Query(`
		SELECT c.nm_id, c.vendor_code, c.subject_id, COALESCE(c.subject_name, '')
		FROM cards c
		WHERE c.vendor_code != ''
		ORDER BY c.nm_id
	`)
	if err != nil {
		t.Fatalf("query cards: %v", err)
	}
	defer rows.Close()

	cardMap := make(map[int]*dbCard)
	var nmIDs []int
	for rows.Next() {
		var c dbCard
		if err := rows.Scan(&c.nmID, &c.vendorCode, &c.subjectID, &c.subjectName); err != nil {
			t.Fatalf("scan card: %v", err)
		}
		cardMap[c.nmID] = &c
		nmIDs = append(nmIDs, c.nmID)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows iteration: %v", err)
	}

	if len(cardMap) == 0 {
		t.Skip("no cards in database")
	}
	t.Logf("loaded %d cards", len(cardMap))

	// Load seasons from card_characteristics
	loadSeasons(t, db, nmIDs, cardMap)

	// Load 1C data from onec_goods (joined by vendor_code = article)
	loadOneCData(t, db, cardMap)

	// Convert to slice
	result := make([]dbCard, 0, len(cardMap))
	for _, c := range cardMap {
		result = append(result, *c)
	}
	return result
}

func loadSeasons(t *testing.T, db *sql.DB, nmIDs []int, cardMap map[int]*dbCard) {
	t.Helper()
	// Load in batches of 500 to avoid SQLite variable limit
	const batchSize = 500
	for i := 0; i < len(nmIDs); i += batchSize {
		end := min(i+batchSize, len(nmIDs))
		batch := nmIDs[i:end]
		ph := placeholders(len(batch))
		args := intSliceToAny(batch)

		rows, err := db.Query(fmt.Sprintf(`
			SELECT cc.nm_id, cc.json_value
			FROM card_characteristics cc
			WHERE cc.name = 'Сезон' AND cc.nm_id IN (%s)
		`, ph), args...)
		if err != nil {
			t.Logf("warning: query seasons: %v", err)
			continue
		}

		for rows.Next() {
			var nmID int
			var jsonValue string
			if err := rows.Scan(&nmID, &jsonValue); err != nil {
				rows.Close()
				continue
			}
			var values []string
			if err := json.Unmarshal([]byte(jsonValue), &values); err == nil {
				if c, ok := cardMap[nmID]; ok {
					c.seasons = values
				}
			}
		}
		rows.Close()
	}
}

func loadOneCData(t *testing.T, db *sql.DB, cardMap map[int]*dbCard) {
	t.Helper()
	// Build vendor_code set
	vcSet := make(map[string]bool, len(cardMap))
	for _, c := range cardMap {
		vcSet[c.vendorCode] = true
	}

	rows, err := db.Query(`
		SELECT o.article, o.type, o.category_level1, o.category_level2,
		       COALESCE(o.is_article_blocked, 0)
		FROM onec_goods o
		WHERE o.article != ''
	`)
	if err != nil {
		t.Logf("warning: query onec_goods: %v", err)
		return
	}
	defer rows.Close()

	// Build reverse map: vendor_code → card
	vcToCard := make(map[string]*dbCard)
	for _, c := range cardMap {
		vcToCard[c.vendorCode] = c
	}

	count := 0
	for rows.Next() {
		var article, oneCType, catL1, catL2 string
		var blocked int
		if err := rows.Scan(&article, &oneCType, &catL1, &catL2, &blocked); err != nil {
			continue
		}
		if c, ok := vcToCard[article]; ok {
			c.oneCType = oneCType
			c.categoryLevel1 = catL1
			c.categoryLevel2 = catL2
			c.articleBlocked = blocked != 0
			c.hasOneC = true
			count++
		}
	}
	t.Logf("loaded 1C data for %d cards", count)
}

func loadStockSet(t *testing.T, db *sql.DB) map[int]bool {
	t.Helper()
	rows, err := db.Query(`
		SELECT nm_id FROM stocks_daily_warehouses
		WHERE snapshot_date = (SELECT MAX(snapshot_date) FROM stocks_daily_warehouses)
		GROUP BY nm_id HAVING SUM(quantity) > 0
	`)
	if err != nil {
		t.Logf("warning: query stocks: %v", err)
		return nil
	}
	defer rows.Close()

	stockSet := make(map[int]bool)
	for rows.Next() {
		var nmID int
		if err := rows.Scan(&nmID); err != nil {
			continue
		}
		stockSet[nmID] = true
	}
	t.Logf("loaded stock set: %d products in stock", len(stockSet))
	return stockSet
}

// queryWithBuildSQL uses BuildSQL to generate a query and returns matching nm_ids.
func queryWithBuildSQL(t *testing.T, db *sql.DB, f Filter) []int {
	t.Helper()
	r, err := f.BuildSQL(SQLConfig{CardsAlias: "c"})
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}

	query := "SELECT c.nm_id FROM cards c"
	for _, join := range r.JOINs {
		query += " " + join
	}
	if r.Where != "" {
		query += " WHERE " + r.Where
	}

	rows, err := db.Query(query, r.Args...)
	if err != nil {
		t.Fatalf("query with BuildSQL: %v\nquery: %s\nargs: %v", err, query, r.Args)
	}
	defer rows.Close()

	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan nm_id: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

// filterWithMatches uses Matches() to filter cards in-memory.
func filterWithMatches(cards []dbCard, f Filter, stockSet map[int]bool) []int {
	var ids []int
	for _, c := range cards {
		if f.Matches(c, stockSet) {
			ids = append(ids, c.nmID)
		}
	}
	return ids
}

func intSlicesEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	sortedA := make([]int, len(a))
	copy(sortedA, a)
	sort.Ints(sortedA)
	sortedB := make([]int, len(b))
	copy(sortedB, b)
	sort.Ints(sortedB)
	for i := range sortedA {
		if sortedA[i] != sortedB[i] {
			return false
		}
	}
	return true
}

// diffIDs returns elements in a but not in b, and vice versa.
func diffIDs(a, b []int) (onlyA, onlyB []int) {
	setA := make(map[int]bool, len(a))
	for _, id := range a {
		setA[id] = true
	}
	setB := make(map[int]bool, len(b))
	for _, id := range b {
		setB[id] = true
	}
	for id := range setA {
		if !setB[id] {
			onlyA = append(onlyA, id)
		}
	}
	for id := range setB {
		if !setA[id] {
			onlyB = append(onlyB, id)
		}
	}
	sort.Ints(onlyA)
	sort.Ints(onlyB)
	return
}

// --- Cross-backend parity tests ---

func TestCrossBackendParity(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	cards := loadCards(t, db)
	stockSet := loadStockSet(t, db)

	tests := []struct {
		name   string
		filter Filter
	}{
		{"empty_filter", Filter{}},
		{"nm_ids", Filter{NmIDs: []int{739760572, 739760578, 739942138}}},
		{"vendor_codes", Filter{VendorCodes: []string{"12621749", "25631749"}}},
		{"allowed_years", Filter{AllowedYears: []int{26, 25}}},
		{"exclude_lengths", Filter{ExcludeLengths: []int{5, 6}}},
		{"vendor_code_prefix", Filter{VendorCodePrefix: "1"}},
		{"subject_ids", Filter{SubjectIDs: []int{105, 81}}},
		{"subject_name", Filter{SubjectName: "Кроссовки"}},
		{"seasons", Filter{Seasons: []string{"демисезон"}}},
		{"in_stock", Filter{InStock: true}},
		{"onec_type", Filter{OneCType: []string{"Обувь"}}},
		{"category_level1", Filter{CategoryLevel1: []string{"Женская"}}},
		{"active_only", Filter{ActiveOnly: true}},
		{"combined_years_and_stock", Filter{AllowedYears: []int{26}, InStock: true}},
		{"combined_1c_fields", Filter{OneCType: []string{"Обувь"}, CategoryLevel1: []string{"Женская"}, ActiveOnly: true}},
		{"combined_all_simple", Filter{AllowedYears: []int{26}, ExcludeLengths: []int{5, 6}, VendorCodePrefix: "1"}},
		// Real config from fix-card-dimensions
		{"real_fix_dimensions", Filter{
			NmIDs:          []int{739760572, 739760578, 739760584, 739760587, 739760590, 739760602, 739762031, 739942138, 739942222, 739942359, 739942422, 746928089, 746928165, 748717371, 748813761, 748881213, 749626137},
			ExcludeLengths: []int{5, 6},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlIDs := queryWithBuildSQL(t, db, tt.filter)
			memIDs := filterWithMatches(cards, tt.filter, stockSet)

			if !intSlicesEqual(sqlIDs, memIDs) {
				onlySQL, onlyMem := diffIDs(sqlIDs, memIDs)
				t.Errorf("MISMATCH: SQL=%d, Mem=%d\n  only_in_SQL: %v\n  only_in_Mem: %v",
					len(sqlIDs), len(memIDs), onlySQL, onlyMem)
			} else {
				t.Logf("OK: %d products match (SQL==Mem)", len(sqlIDs))
			}
		})
	}
}
