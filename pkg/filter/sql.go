package filter

import (
	"fmt"
	"strings"
)

// SQLConfig controls SQL generation behavior.
type SQLConfig struct {
	CardsAlias string // table alias for cards (default: "c")
}

// SQLResult holds generated SQL parts for filter application.
type SQLResult struct {
	Where string   // WHERE conditions (without "WHERE" keyword). Empty = no conditions
	Args  []any    // SQL parameters
	JOINs []string // Required JOIN clauses (deduplicated)
}

// BuildSQL generates SQL WHERE clause and required JOINs from filter criteria.
//
// Returns SQLResult with:
//   - Where: conditions joined by AND (caller prepends "WHERE" if non-empty)
//   - Args: SQL parameters matching placeholder positions
//   - JOINs: required LEFT JOIN clauses (e.g., onec_goods for 1C filters)
//
// If no filter fields are set, returns empty Where and nil Args/JOINs.
func (f *Filter) BuildSQL(cfg SQLConfig) (*SQLResult, error) {
	alias := cfg.CardsAlias
	if alias == "" {
		alias = "c"
	}

	var conds []string
	var args []any
	needOneCJoin := false

	// Identity filters
	if len(f.NmIDs) > 0 {
		conds = append(conds, alias+".nm_id IN ("+placeholders(len(f.NmIDs))+")")
		args = append(args, intSliceToAny(f.NmIDs)...)
	}
	if len(f.VendorCodes) > 0 {
		conds = append(conds, alias+".vendor_code IN ("+placeholders(len(f.VendorCodes))+")")
		args = append(args, stringSliceToAny(f.VendorCodes)...)
	}

	// Vendor code derived filters
	if len(f.ExcludeVendorCodes) > 0 {
		conds = append(conds, alias+".vendor_code NOT IN ("+placeholders(len(f.ExcludeVendorCodes))+")")
		args = append(args, stringSliceToAny(f.ExcludeVendorCodes)...)
	}
	if len(f.ExcludeLengths) > 0 {
		conds = append(conds, "LENGTH("+alias+".vendor_code) NOT IN ("+placeholders(len(f.ExcludeLengths))+")")
		args = append(args, intSliceToAny(f.ExcludeLengths)...)
	}
	if len(f.AllowedYears) > 0 {
		yearStrs := make([]string, len(f.AllowedYears))
		for i, y := range f.AllowedYears {
			yearStrs[i] = fmt.Sprintf("%02d", y%100)
		}
		conds = append(conds, "SUBSTR("+alias+".vendor_code, 2, 2) IN ("+placeholders(len(yearStrs))+")")
		args = append(args, stringSliceToAny(yearStrs)...)
	}
	if f.VendorCodePrefix != "" {
		conds = append(conds, "SUBSTR("+alias+".vendor_code, 1, ?) = ?")
		args = append(args, len(f.VendorCodePrefix), f.VendorCodePrefix)
	}

	// WB category
	if len(f.SubjectIDs) > 0 {
		conds = append(conds, alias+".subject_id IN ("+placeholders(len(f.SubjectIDs))+")")
		args = append(args, intSliceToAny(f.SubjectIDs)...)
	}
	if f.SubjectName != "" {
		conds = append(conds, "LOWER("+alias+".subject_name) = LOWER(?)")
		args = append(args, f.SubjectName)
	}

	// Seasons
	if len(f.Seasons) > 0 {
		ph := make([]string, len(f.Seasons))
		for i, s := range f.Seasons {
			ph[i] = "LOWER(je.value) LIKE LOWER(?)"
			args = append(args, "%"+s+"%")
		}
		conds = append(conds, alias+".nm_id IN (SELECT cc.nm_id FROM card_characteristics cc, json_each(cc.json_value) je WHERE cc.name = 'Сезон' AND ("+strings.Join(ph, " OR ")+"))")
	}

	// Stock
	if f.InStock {
		conds = append(conds, alias+`.nm_id IN (
			SELECT nm_id FROM stocks_daily_warehouses
			WHERE snapshot_date = (SELECT MAX(snapshot_date) FROM stocks_daily_warehouses)
			GROUP BY nm_id HAVING SUM(quantity) > 0
		)`)
	}

	// 1C categories + active status
	if len(f.OneCType) > 0 {
		needOneCJoin = true
		conds = append(conds, "o.type IN ("+placeholders(len(f.OneCType))+")")
		args = append(args, stringSliceToAny(f.OneCType)...)
	}
	if len(f.CategoryLevel1) > 0 {
		needOneCJoin = true
		conds = append(conds, "o.category_level1 IN ("+placeholders(len(f.CategoryLevel1))+")")
		args = append(args, stringSliceToAny(f.CategoryLevel1)...)
	}
	if len(f.CategoryLevel2) > 0 {
		needOneCJoin = true
		conds = append(conds, "o.category_level2 IN ("+placeholders(len(f.CategoryLevel2))+")")
		args = append(args, stringSliceToAny(f.CategoryLevel2)...)
	}
	if f.ActiveOnly {
		needOneCJoin = true
		conds = append(conds, "COALESCE(o.is_article_blocked, 0) = 0")
	}

	// Build result
	result := &SQLResult{}
	if len(conds) > 0 {
		result.Where = strings.Join(conds, " AND ")
		result.Args = args
	}

	if needOneCJoin {
		result.JOINs = []string{
			fmt.Sprintf("LEFT JOIN onec_goods o ON o.article = %s.vendor_code", alias),
		}
	}

	return result, nil
}
