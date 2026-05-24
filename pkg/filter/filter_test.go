package filter

import (
	"strings"
	"testing"
)

// testItem implements Filterable for testing.
type testItem struct {
	nmID        int
	vendorCode  string
	subjectID   int
	subjectName string
	seasons     []string
}

func (t testItem) GetNmID() int            { return t.nmID }
func (t testItem) GetVendorCode() string    { return t.vendorCode }
func (t testItem) GetSubjectID() int        { return t.subjectID }
func (t testItem) GetSubjectName() string   { return t.subjectName }
func (t testItem) GetSeasons() []string     { return t.seasons }

// testItem1C implements Filterable1C for testing.
type testItem1C struct {
	testItem
	oneCType        string
	categoryLevel1  string
	categoryLevel2  string
	articleBlocked  bool
}

func (t testItem1C) GetOneCType() string       { return t.oneCType }
func (t testItem1C) GetCategoryLevel1() string  { return t.categoryLevel1 }
func (t testItem1C) GetCategoryLevel2() string  { return t.categoryLevel2 }
func (t testItem1C) IsArticleBlocked() bool     { return t.articleBlocked }

func item(vc string, nmID int) testItem {
	return testItem{nmID: nmID, vendorCode: vc}
}

func itemFull(vc string, nmID, subjID int, subj string, seasons []string) testItem {
	return testItem{nmID: nmID, vendorCode: vc, subjectID: subjID, subjectName: subj, seasons: seasons}
}

// --- Matches() tests ---

func TestMatches_EmptyFilter(t *testing.T) {
	f := Filter{}
	if !f.Matches(item("12621749", 100), nil) {
		t.Error("empty filter should match everything")
	}
}

func TestMatches_NmIDs(t *testing.T) {
	f := Filter{NmIDs: []int{100, 200, 300}}
	if !f.Matches(item("12621749", 200), nil) {
		t.Error("should match nm_id in list")
	}
	if f.Matches(item("12621749", 999), nil) {
		t.Error("should not match nm_id not in list")
	}
}

func TestMatches_VendorCodes(t *testing.T) {
	f := Filter{VendorCodes: []string{"12621749", "25631749"}}
	if !f.Matches(item("12621749", 100), nil) {
		t.Error("should match vendor_code in list")
	}
	if f.Matches(item("99999999", 100), nil) {
		t.Error("should not match vendor_code not in list")
	}
}

func TestMatches_AllowedYears(t *testing.T) {
	f := Filter{AllowedYears: []int{26, 25}}
	if !f.Matches(item("12621749", 100), nil) {
		t.Error("year=26 should match allowed_years [26,25]")
	}
	if f.Matches(item("12431749", 100), nil) {
		t.Error("year=24 should not match allowed_years [26,25]")
	}
	if f.Matches(item("AB", 100), nil) {
		t.Error("short vendor_code should not match year filter")
	}
}

func TestMatches_ExcludeLengths(t *testing.T) {
	f := Filter{ExcludeLengths: []int{5, 6}}
	if !f.Matches(item("12621749", 100), nil) {
		t.Error("8-char vendor_code should pass exclude_lengths [5,6]")
	}
	if f.Matches(item("12345", 100), nil) {
		t.Error("5-char vendor_code should be excluded")
	}
}

func TestMatches_VendorCodePrefix(t *testing.T) {
	f := Filter{VendorCodePrefix: "1"}
	if !f.Matches(item("12621749", 100), nil) {
		t.Error("vendor_code starting with '1' should match prefix '1'")
	}
	if f.Matches(item("22621749", 100), nil) {
		t.Error("vendor_code starting with '2' should not match prefix '1'")
	}
}

func TestMatches_SubjectIDs(t *testing.T) {
	f := Filter{SubjectIDs: []int{10, 20}}
	if !f.Matches(itemFull("12621749", 100, 10, "Обувь", nil), nil) {
		t.Error("subject_id=10 should match [10,20]")
	}
	if f.Matches(itemFull("12621749", 100, 99, "Обувь", nil), nil) {
		t.Error("subject_id=99 should not match [10,20]")
	}
}

func TestMatches_SubjectName(t *testing.T) {
	f := Filter{SubjectName: "Обувь"}
	if !f.Matches(itemFull("12621749", 100, 0, "Обувь", nil), nil) {
		t.Error("exact case-insensitive match should work")
	}
	if !f.Matches(itemFull("12621749", 100, 0, "обувь", nil), nil) {
		t.Error("EqualFold should handle case difference")
	}
	if f.Matches(itemFull("12621749", 100, 0, "Кроссовки (Обувь)", nil), nil) {
		t.Error("should not match substring")
	}
}

func TestMatches_Seasons(t *testing.T) {
	f := Filter{Seasons: []string{"демисезон", "лето"}}
	if !f.Matches(itemFull("12621749", 100, 0, "", []string{"демисезон"}), nil) {
		t.Error("season 'демисезон' should match")
	}
	if f.Matches(itemFull("12621749", 100, 0, "", []string{"зима"}), nil) {
		t.Error("season 'зима' should not match")
	}
	if f.Matches(itemFull("12621749", 100, 0, "", nil), nil) {
		t.Error("no seasons should not match")
	}
}

func TestMatches_InStock(t *testing.T) {
	f := Filter{InStock: true}
	stockSet := map[int]bool{100: true, 200: true}

	if !f.Matches(item("12621749", 100), stockSet) {
		t.Error("nm_id in stockSet should match")
	}
	if f.Matches(item("12621749", 999), stockSet) {
		t.Error("nm_id not in stockSet should not match")
	}
	// nil stockSet = skip filter
	if !f.Matches(item("12621749", 999), nil) {
		t.Error("nil stockSet should skip InStock filter")
	}
}

func TestMatches_OneCType(t *testing.T) {
	f := Filter{OneCType: []string{"Обувь"}}
	item1 := testItem1C{testItem: item("12621749", 100), oneCType: "Обувь"}
	item2 := testItem1C{testItem: item("12621749", 100), oneCType: "Одежда"}

	if !f.Matches(item1, nil) {
		t.Error("onec_type 'Обувь' should match")
	}
	if f.Matches(item2, nil) {
		t.Error("onec_type 'Одежда' should not match filter ['Обувь']")
	}

	// Non-1C item: 1C filters are skipped
	plainItem := item("12621749", 100)
	if !f.Matches(plainItem, nil) {
		t.Error("non-1C item should skip 1C filters")
	}
}

func TestMatches_CategoryLevels(t *testing.T) {
	f := Filter{OneCType: []string{"Обувь"}, CategoryLevel1: []string{"Женская"}}
	item1 := testItem1C{testItem: item("12621749", 100), oneCType: "Обувь", categoryLevel1: "Женская"}
	item2 := testItem1C{testItem: item("12621749", 100), oneCType: "Обувь", categoryLevel1: "Мужская"}
	item3 := testItem1C{testItem: item("12621749", 100), oneCType: "Одежда", categoryLevel1: "Женская"}

	if !f.Matches(item1, nil) {
		t.Error("Обувь+Женская should match")
	}
	if f.Matches(item2, nil) {
		t.Error("Обувь+Мужская should not match CategoryLevel1=['Женская']")
	}
	if f.Matches(item3, nil) {
		t.Error("Одежда+Женская should not match OneCType=['Обувь']")
	}
}

func TestMatches_ActiveOnly(t *testing.T) {
	f := Filter{ActiveOnly: true}
	item1 := testItem1C{testItem: item("12621749", 100), articleBlocked: false}
	item2 := testItem1C{testItem: item("12621749", 100), articleBlocked: true}

	if !f.Matches(item1, nil) {
		t.Error("non-blocked article should match active_only")
	}
	if f.Matches(item2, nil) {
		t.Error("blocked article should not match active_only")
	}
}

func TestMatches_CombinedAND(t *testing.T) {
	f := Filter{
		NmIDs:       []int{100},
		AllowedYears: []int{26},
		ActiveOnly:   true,
	}
	stockSet := map[int]bool{100: true}

	good := testItem1C{testItem: item("12621749", 100), articleBlocked: false}
	badID := testItem1C{testItem: item("12621749", 999), articleBlocked: false}
	badYear := testItem1C{testItem: item("12431749", 100), articleBlocked: false}
	badBlocked := testItem1C{testItem: item("12621749", 100), articleBlocked: true}

	if !f.Matches(good, stockSet) {
		t.Error("all fields matching should pass")
	}
	if f.Matches(badID, stockSet) {
		t.Error("wrong nm_id should fail")
	}
	if f.Matches(badYear, stockSet) {
		t.Error("wrong year should fail")
	}
	if f.Matches(badBlocked, stockSet) {
		t.Error("blocked article should fail")
	}
}

// --- BuildSQL() tests ---

func TestBuildSQL_EmptyFilter(t *testing.T) {
	f := Filter{}
	r, err := f.BuildSQL(SQLConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Where != "" {
		t.Errorf("expected empty Where, got: %s", r.Where)
	}
	if len(r.Args) > 0 {
		t.Errorf("expected no args, got: %v", r.Args)
	}
	if len(r.JOINs) > 0 {
		t.Errorf("expected no JOINs, got: %v", r.JOINs)
	}
}

func TestBuildSQL_NmIDs(t *testing.T) {
	f := Filter{NmIDs: []int{100, 200}}
	r, _ := f.BuildSQL(SQLConfig{})
	if !strings.Contains(r.Where, "c.nm_id IN (?,?)") {
		t.Errorf("expected nm_id IN clause, got: %s", r.Where)
	}
	if len(r.Args) != 2 || r.Args[0].(int) != 100 || r.Args[1].(int) != 200 {
		t.Errorf("expected args [100,200], got: %v", r.Args)
	}
}

func TestBuildSQL_VendorCodes(t *testing.T) {
	f := Filter{VendorCodes: []string{"12621749", "25631749"}}
	r, _ := f.BuildSQL(SQLConfig{})
	if !strings.Contains(r.Where, "c.vendor_code IN (?,?)") {
		t.Errorf("expected vendor_code IN clause, got: %s", r.Where)
	}
}

func TestBuildSQL_AllowedYears(t *testing.T) {
	f := Filter{AllowedYears: []int{26, 25}}
	r, _ := f.BuildSQL(SQLConfig{})
	if !strings.Contains(r.Where, "SUBSTR(c.vendor_code, 2, 2) IN (?,?)") {
		t.Errorf("expected SUBSTR clause, got: %s", r.Where)
	}
	if len(r.Args) != 2 || r.Args[0].(string) != "26" || r.Args[1].(string) != "25" {
		t.Errorf("expected year strings ['26','25'], got: %v", r.Args)
	}
}

func TestBuildSQL_ExcludeLengths(t *testing.T) {
	f := Filter{ExcludeLengths: []int{5, 6}}
	r, _ := f.BuildSQL(SQLConfig{})
	if !strings.Contains(r.Where, "LENGTH(c.vendor_code) NOT IN (?,?)") {
		t.Errorf("expected LENGTH NOT IN clause, got: %s", r.Where)
	}
}

func TestBuildSQL_VendorCodePrefix(t *testing.T) {
	f := Filter{VendorCodePrefix: "1"}
	r, _ := f.BuildSQL(SQLConfig{})
	if !strings.Contains(r.Where, "SUBSTR(c.vendor_code, 1, ?) = ?") {
		t.Errorf("expected SUBSTR prefix clause, got: %s", r.Where)
	}
	if len(r.Args) != 2 {
		t.Errorf("expected 2 args (len, prefix), got: %v", r.Args)
	}
}

func TestBuildSQL_SubjectIDs(t *testing.T) {
	f := Filter{SubjectIDs: []int{10, 20}}
	r, _ := f.BuildSQL(SQLConfig{})
	if !strings.Contains(r.Where, "c.subject_id IN (?,?)") {
		t.Errorf("expected subject_id IN clause, got: %s", r.Where)
	}
}

func TestBuildSQL_SubjectName(t *testing.T) {
	f := Filter{SubjectName: "Обувь"}
	r, _ := f.BuildSQL(SQLConfig{})
	if !strings.Contains(r.Where, "LOWER(c.subject_name) = LOWER(?)") {
		t.Errorf("expected subject_name exact match clause, got: %s", r.Where)
	}
	if r.Args[0].(string) != "Обувь" {
		t.Errorf("expected 'Обувь', got: %v", r.Args[0])
	}
}

func TestBuildSQL_Seasons(t *testing.T) {
	f := Filter{Seasons: []string{"демисезон", "лето"}}
	r, _ := f.BuildSQL(SQLConfig{})
	if !strings.Contains(r.Where, "card_characteristics") {
		t.Errorf("expected seasons subquery with card_characteristics, got: %s", r.Where)
	}
	if !strings.Contains(r.Where, "json_each") {
		t.Errorf("expected json_each in seasons subquery, got: %s", r.Where)
	}
}

func TestBuildSQL_InStock(t *testing.T) {
	f := Filter{InStock: true}
	r, _ := f.BuildSQL(SQLConfig{})
	if !strings.Contains(r.Where, "stocks_daily_warehouses") {
		t.Errorf("expected stocks subquery, got: %s", r.Where)
	}
}

func TestBuildSQL_OneCType(t *testing.T) {
	f := Filter{OneCType: []string{"Обувь"}}
	r, _ := f.BuildSQL(SQLConfig{})
	if !strings.Contains(r.Where, "o.type IN (?)") {
		t.Errorf("expected o.type IN clause, got: %s", r.Where)
	}
	if len(r.JOINs) != 1 || !strings.Contains(r.JOINs[0], "onec_goods") {
		t.Errorf("expected 1 JOIN to onec_goods, got: %v", r.JOINs)
	}
}

func TestBuildSQL_CategoryLevel1(t *testing.T) {
	f := Filter{CategoryLevel1: []string{"Женская", "Мужская"}}
	r, _ := f.BuildSQL(SQLConfig{})
	if !strings.Contains(r.Where, "o.category_level1 IN (?,?)") {
		t.Errorf("expected category_level1 IN clause, got: %s", r.Where)
	}
	if len(r.JOINs) != 1 {
		t.Errorf("expected 1 JOIN, got: %d", len(r.JOINs))
	}
}

func TestBuildSQL_ActiveOnly(t *testing.T) {
	f := Filter{ActiveOnly: true}
	r, _ := f.BuildSQL(SQLConfig{})
	if !strings.Contains(r.Where, "COALESCE(o.is_article_blocked, 0) = 0") {
		t.Errorf("expected is_article_blocked clause, got: %s", r.Where)
	}
	if len(r.JOINs) != 1 || !strings.Contains(r.JOINs[0], "onec_goods") {
		t.Errorf("expected 1 JOIN to onec_goods, got: %v", r.JOINs)
	}
}

func TestBuildSQL_Multiple1CFields_ShareJOIN(t *testing.T) {
	f := Filter{
		OneCType:       []string{"Обувь"},
		CategoryLevel1: []string{"Женская"},
		ActiveOnly:     true,
	}
	r, _ := f.BuildSQL(SQLConfig{})
	if len(r.JOINs) != 1 {
		t.Errorf("expected 1 shared JOIN for multiple 1C fields, got: %d", len(r.JOINs))
	}
}

func TestBuildSQL_CustomAlias(t *testing.T) {
	f := Filter{NmIDs: []int{100}}
	r, _ := f.BuildSQL(SQLConfig{CardsAlias: "cards"})
	if !strings.Contains(r.Where, "cards.nm_id IN") {
		t.Errorf("expected custom alias 'cards', got: %s", r.Where)
	}
}

func TestBuildSQL_AllFields(t *testing.T) {
	f := Filter{
		NmIDs:            []int{100},
		VendorCodes:      []string{"12621749"},
		AllowedYears:     []int{26},
		ExcludeLengths:   []int{5},
		VendorCodePrefix: "1",
		SubjectIDs:       []int{10},
		SubjectName:      "обувь",
		Seasons:          []string{"демисезон"},
		InStock:          true,
		OneCType:         []string{"Обувь"},
		CategoryLevel1:   []string{"Женская"},
		ActiveOnly:       true,
	}
	r, _ := f.BuildSQL(SQLConfig{})

	// Should have conditions for all fields
	conds := strings.Split(r.Where, " AND ")
	if len(conds) < 10 {
		t.Errorf("expected 10+ conditions for all-fields filter, got %d: %s", len(conds), r.Where)
	}
	if len(r.JOINs) != 1 {
		t.Errorf("expected 1 JOIN, got: %v", r.JOINs)
	}
}

func TestFilter_Empty(t *testing.T) {
	var empty Filter
	if !empty.Empty() {
		t.Error("zero-value Filter should be empty")
	}
	withIDs := Filter{NmIDs: []int{1}}
	if withIDs.Empty() {
		t.Error("Filter with NmIDs should not be empty")
	}
	withStock := Filter{InStock: true}
	if withStock.Empty() {
		t.Error("Filter with InStock should not be empty")
	}
}
