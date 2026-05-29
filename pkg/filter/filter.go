// Package filter provides unified product filtering for WB (and future Ozon) utilities.
//
// Two backends:
//   - Matches(): in-memory filtering on Filterable interface
//   - BuildSQL(): SQL WHERE + JOIN generation for database queries
//
// AND/OR semantics:
//   - Between filter fields: AND (must pass ALL non-empty fields)
//   - Within list fields:    OR  (match ANY element)
//   - Empty/zero fields:     SKIP (no filter on that dimension)
//   - Exclude* fields:       NOT (must NOT match)
package filter

import (
	"strings"
	"unicode/utf8"
)

// Filter defines product filtering criteria for WB utilities.
//
// All fields use AND between each other. List fields use OR within.
// Empty/zero fields are ignored.
type Filter struct {
	// Identity (OR within list, AND between fields)
	NmIDs       []int    `yaml:"nm_ids"`       // WB product IDs. Empty = no filter
	VendorCodes []string `yaml:"vendor_codes"` // supplier articles. Empty = no filter

	// Vendor code derived (AND between, OR within)
	AllowedYears        []int    `yaml:"allowed_years"`         // year from vendor_code chars 2-3. OR within
	ExcludeLengths      []int    `yaml:"exclude_lengths"`       // exclude by vendor_code length. NOT IN
	ExcludeVendorCodes  []string `yaml:"exclude_vendor_codes"`  // skip these vendor codes. NOT IN
	VendorCodePrefix    string   `yaml:"vendor_code_prefix"`    // first char(s) of vendor_code

	// WB category (OR within list)
	SubjectIDs  []int    `yaml:"subject_ids"` // WB subject IDs
	SubjectName string   `yaml:"subject"`     // case-insensitive exact match
	Seasons     []string `yaml:"seasons"`     // from card_characteristics. OR within

	// Stock
	InStock bool `yaml:"in_stock"` // has stock in latest stocks_daily_warehouses snapshot

	// 1C categories — three separate fields, AND between levels, OR within each
	OneCType       []string `yaml:"onec_type"`       // Обувь/Одежда/Аксессуары
	CategoryLevel1 []string `yaml:"category_level1"` // 1C category L1
	CategoryLevel2 []string `yaml:"category_level2"` // 1C category L2

	// Active status — 1C is_article_blocked (blocked from sale at warehouse)
	ActiveOnly bool `yaml:"active_only"` // COALESCE(onec_goods.is_article_blocked, 0) = 0
}

// Empty returns true if no filter fields are set (all defaults/empty).
func (f *Filter) Empty() bool {
	return len(f.NmIDs) == 0 &&
		len(f.VendorCodes) == 0 &&
		len(f.AllowedYears) == 0 &&
		len(f.ExcludeLengths) == 0 &&
		len(f.ExcludeVendorCodes) == 0 &&
		f.VendorCodePrefix == "" &&
		len(f.SubjectIDs) == 0 &&
		f.SubjectName == "" &&
		len(f.Seasons) == 0 &&
		!f.InStock &&
		len(f.OneCType) == 0 &&
		len(f.CategoryLevel1) == 0 &&
		len(f.CategoryLevel2) == 0 &&
		!f.ActiveOnly
}

// Filterable provides product attributes for in-memory filtering.
type Filterable interface {
	GetNmID() int
	GetVendorCode() string
	GetSubjectID() int
	GetSubjectName() string
	GetSeasons() []string
}

// Filterable1C extends Filterable with 1C data for in-memory filtering.
// If the item does not implement this, 1C-related filters are skipped.
type Filterable1C interface {
	Filterable
	GetOneCType() string
	GetCategoryLevel1() string
	GetCategoryLevel2() string
	IsArticleBlocked() bool
}

// Matches returns true if the item passes all non-empty filter criteria.
// stockSet is optional (nil = no stock filter even if InStock is true).
func (f *Filter) Matches(item Filterable, stockSet map[int]bool) bool {
	// Identity filters
	if len(f.NmIDs) > 0 {
		if !intSet(f.NmIDs)[item.GetNmID()] {
			return false
		}
	}
	if len(f.VendorCodes) > 0 {
		if !stringSet(f.VendorCodes)[item.GetVendorCode()] {
			return false
		}
	}

	// Vendor code derived filters
	vc := item.GetVendorCode()
	if len(f.ExcludeVendorCodes) > 0 {
		if stringSet(f.ExcludeVendorCodes)[vc] {
			return false
		}
	}
	if len(f.ExcludeLengths) > 0 {
		if intSet(f.ExcludeLengths)[utf8.RuneCountInString(vc)] {
			return false
		}
	}
	if len(f.AllowedYears) > 0 {
		year := extractYear(vc)
		if year < 0 || !intSet(f.AllowedYears)[year] {
			return false
		}
	}
	if f.VendorCodePrefix != "" {
		if !strings.HasPrefix(vc, f.VendorCodePrefix) {
			return false
		}
	}

	// WB category
	if len(f.SubjectIDs) > 0 {
		if !intSet(f.SubjectIDs)[item.GetSubjectID()] {
			return false
		}
	}
	if f.SubjectName != "" {
		if !strings.EqualFold(item.GetSubjectName(), f.SubjectName) {
			return false
		}
	}

	// Seasons
	if len(f.Seasons) > 0 {
		seasonSet := stringSet(f.Seasons)
		matched := false
		for _, s := range item.GetSeasons() {
			if seasonSet[s] {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// Stock
	if f.InStock && stockSet != nil {
		if !stockSet[item.GetNmID()] {
			return false
		}
	}

	// 1C filters — only if item implements Filterable1C
	if item1C, ok := item.(Filterable1C); ok {
		if len(f.OneCType) > 0 {
			if !stringSet(f.OneCType)[item1C.GetOneCType()] {
				return false
			}
		}
		if len(f.CategoryLevel1) > 0 {
			if !stringSet(f.CategoryLevel1)[item1C.GetCategoryLevel1()] {
				return false
			}
		}
		if len(f.CategoryLevel2) > 0 {
			if !stringSet(f.CategoryLevel2)[item1C.GetCategoryLevel2()] {
				return false
			}
		}
		if f.ActiveOnly {
			if item1C.IsArticleBlocked() {
				return false
			}
		}
	}

	return true
}
