package filter

import (
	"strings"
	"testing"
)

// TestSQLParity verifies that BuildSQL() generates SQL equivalent to the
// hand-written buildFilterClause() in fix-card-fields/query.go and
// check-card-consistency/query.go.
//
// Expected SQL fragments are extracted from manual analysis of the old code.
// Each test case represents a real filter config from existing utilities.

func TestSQLParity_NmIDsOnly(t *testing.T) {
	// From fix-card-dimensions config: nm_ids: [739760572, 739760578]
	f := Filter{NmIDs: []int{739760572, 739760578}}
	r, _ := f.BuildSQL(SQLConfig{})

	assertContains(t, r.Where, "c.nm_id IN (?,?)")
	assertArgsCount(t, r.Args, 2)
	assertArgsEqual(t, r.Args[0], 739760572)
	assertArgsEqual(t, r.Args[1], 739760578)
	assertNoJOINs(t, r)
}

func TestSQLParity_VendorCodesOnly(t *testing.T) {
	// From fix-card-dimensions: vendor_codes filter
	f := Filter{VendorCodes: []string{"12621749", "25631749"}}
	r, _ := f.BuildSQL(SQLConfig{})

	assertContains(t, r.Where, "c.vendor_code IN (?,?)")
	assertArgsCount(t, r.Args, 2)
	assertNoJOINs(t, r)
}

func TestSQLParity_SubjectIDs(t *testing.T) {
	// From fix-card-fields config: subject_ids: [105]
	f := Filter{SubjectIDs: []int{105}}
	r, _ := f.BuildSQL(SQLConfig{})

	assertContains(t, r.Where, "c.subject_id IN (?)")
	assertArgsEqual(t, r.Args[0], 105)
	assertNoJOINs(t, r)
}

func TestSQLParity_VendorCodeYears(t *testing.T) {
	// From fix-card-fields config: vendor_code_years: [22]
	// Old SQL: SUBSTR(c.vendor_code, 2, 2) IN (?)
	f := Filter{AllowedYears: []int{22}}
	r, _ := f.BuildSQL(SQLConfig{})

	assertContains(t, r.Where, "SUBSTR(c.vendor_code, 2, 2) IN (?)")
	assertArgsEqual(t, r.Args[0], "22") // year as 2-digit string
	assertNoJOINs(t, r)
}

func TestSQLParity_VendorCodePrefix(t *testing.T) {
	// From fix-card-fields config: vendor_code_prefix: "1"
	// Old SQL: SUBSTR(c.vendor_code, 1, 1) = ?
	f := Filter{VendorCodePrefix: "1"}
	r, _ := f.BuildSQL(SQLConfig{})

	assertContains(t, r.Where, "SUBSTR(c.vendor_code, 1, ?) = ?")
	assertArgsEqual(t, r.Args[0], 1)    // length of prefix
	assertArgsEqual(t, r.Args[1], "1")  // prefix value
	assertNoJOINs(t, r)
}

func TestSQLParity_InStock(t *testing.T) {
	// From all utilities: in_stock: true
	// Old SQL: c.nm_id IN (SELECT nm_id FROM stocks_daily_warehouses ...)
	f := Filter{InStock: true}
	r, _ := f.BuildSQL(SQLConfig{})

	assertContains(t, r.Where, "stocks_daily_warehouses")
	assertContains(t, r.Where, "SUM(quantity) > 0")
	assertContains(t, r.Where, "MAX(snapshot_date)")
	assertNoJOINs(t, r) // in_stock uses subquery, not JOIN
}

func TestSQLParity_ExcludeLengths(t *testing.T) {
	// From fix-card-dimensions: exclude_lengths: [5, 6]
	// Old SQL: LENGTH(c.vendor_code) != 5 AND LENGTH(c.vendor_code) != 6
	// New SQL: LENGTH(c.vendor_code) NOT IN (?,?)
	f := Filter{ExcludeLengths: []int{5, 6}}
	r, _ := f.BuildSQL(SQLConfig{})

	assertContains(t, r.Where, "LENGTH(c.vendor_code) NOT IN (?,?)")
	assertArgsEqual(t, r.Args[0], 5)
	assertArgsEqual(t, r.Args[1], 6)
	assertNoJOINs(t, r)
}

func TestSQLParity_Seasons(t *testing.T) {
	// From check-card-consistency: seasons: ["демисезон"]
	// Old SQL: c.nm_id IN (SELECT cc.nm_id FROM card_characteristics cc, json_each(cc.json_value) je WHERE cc.name = 'Сезон' AND (LOWER(je.value) = LOWER(?)))
	f := Filter{Seasons: []string{"демисезон"}}
	r, _ := f.BuildSQL(SQLConfig{})

	assertContains(t, r.Where, "card_characteristics")
	assertContains(t, r.Where, "json_each")
	assertContains(t, r.Where, "Сезон")
	assertContains(t, r.Where, "LOWER(je.value)")
	assertArgsEqual(t, r.Args[0], "%демисезон%")
	assertNoJOINs(t, r)
}

func TestSQLParity_SubjectName(t *testing.T) {
	// From check-card-consistency: subject: "Кроссовки"
	// Old SQL: LOWER(c.subject_name) = LOWER(?)
	// New SQL: LOWER(c.subject_name) LIKE LOWER(?) — uses LIKE for substring match
	f := Filter{SubjectName: "Кроссовки"}
	r, _ := f.BuildSQL(SQLConfig{})

	assertContains(t, r.Where, "LOWER(c.subject_name) = LOWER(?)")
	// covered above
	assertArgsEqual(t, r.Args[0], "Кроссовки")
	assertNoJOINs(t, r)
}

func TestSQLParity_OneCType(t *testing.T) {
	// New filter: onec_type: ["Обувь"]
	f := Filter{OneCType: []string{"Обувь"}}
	r, _ := f.BuildSQL(SQLConfig{})

	assertContains(t, r.Where, "o.type IN (?)")
	assertArgsEqual(t, r.Args[0], "Обувь") // value is in args, not in WHERE string
	assertJOINCount(t, r, 1)
	assertContains(t, r.JOINs[0], "onec_goods")
	assertContains(t, r.JOINs[0], "o.article = c.vendor_code")
}

func TestSQLParity_ActiveOnly(t *testing.T) {
	// New filter: active_only: true
	f := Filter{ActiveOnly: true}
	r, _ := f.BuildSQL(SQLConfig{})

	assertContains(t, r.Where, "COALESCE(o.is_article_blocked, 0) = 0")
	assertJOINCount(t, r, 1)
	assertContains(t, r.JOINs[0], "onec_goods")
}

func TestSQLParity_CategoryLevels(t *testing.T) {
	// New filter: onec_type + category_level1 + category_level2
	f := Filter{
		OneCType:       []string{"Обувь"},
		CategoryLevel1: []string{"Женская"},
		CategoryLevel2: []string{"Кроссовки"},
	}
	r, _ := f.BuildSQL(SQLConfig{})

	assertContains(t, r.Where, "o.type IN (?)")
	assertContains(t, r.Where, "o.category_level1 IN (?)")
	assertContains(t, r.Where, "o.category_level2 IN (?)")
	assertJOINCount(t, r, 1) // single JOIN for all 1C fields
}

func TestSQLParity_RealFixCardFieldsConfig(t *testing.T) {
	// From fix-card-fields/config.yaml:
	// filters: {subject_ids: [105], vendor_code_years: [22], in_stock: false}
	f := Filter{
		SubjectIDs:   []int{105},
		AllowedYears: []int{22},
	}
	r, _ := f.BuildSQL(SQLConfig{})

	assertContains(t, r.Where, "c.subject_id IN (?)")
	assertContains(t, r.Where, "SUBSTR(c.vendor_code, 2, 2) IN (?)")
	assertNoJOINs(t, r)
	assertArgsCount(t, r.Args, 2) // 105, "22"
}

func TestSQLParity_RealFixCardDimensionsConfig(t *testing.T) {
	// From fix-card-dimensions/config.yaml:
	// filters: {nm_ids: [...], exclude_lengths: [5, 6]}
	f := Filter{
		NmIDs:          []int{739760572, 739760578, 739760584},
		ExcludeLengths: []int{5, 6},
	}
	r, _ := f.BuildSQL(SQLConfig{})

	assertContains(t, r.Where, "c.nm_id IN (?,?,?)")
	assertContains(t, r.Where, "LENGTH(c.vendor_code) NOT IN (?,?)")
	assertArgsCount(t, r.Args, 5) // 3 nm_ids + 2 exclude_lengths
	assertNoJOINs(t, r)
}

func TestSQLParity_AllFieldsProduceAND(t *testing.T) {
	// Verify that multiple conditions are AND-combined
	f := Filter{
		NmIDs:        []int{100},
		SubjectIDs:   []int{10},
		InStock:      true,
	}
	r, _ := f.BuildSQL(SQLConfig{})

	conds := strings.Split(r.Where, " AND ")
	if len(conds) != 3 {
		t.Errorf("expected 3 AND-separated conditions, got %d: %s", len(conds), r.Where)
	}
}

// --- helpers ---

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected SQL to contain %q\nactual: %s", needle, haystack)
	}
}

func assertArgsCount(t *testing.T, args []any, expected int) {
	t.Helper()
	if len(args) != expected {
		t.Errorf("expected %d args, got %d: %v", expected, len(args), args)
	}
}

func assertArgsEqual(t *testing.T, arg any, expected any) {
	t.Helper()
	if arg != expected {
		t.Errorf("expected arg %v, got %v", expected, arg)
	}
}

func assertNoJOINs(t *testing.T, r *SQLResult) {
	t.Helper()
	if len(r.JOINs) > 0 {
		t.Errorf("expected no JOINs, got: %v", r.JOINs)
	}
}

func assertJOINCount(t *testing.T, r *SQLResult, expected int) {
	t.Helper()
	if len(r.JOINs) != expected {
		t.Errorf("expected %d JOINs, got %d: %v", expected, len(r.JOINs), r.JOINs)
	}
}
