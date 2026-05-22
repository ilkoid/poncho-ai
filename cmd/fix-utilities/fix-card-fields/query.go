package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// CardData — basic card info from the cards table.
type CardData struct {
	NmID        int
	VendorCode  string
	Title       string
	SubjectID   int
	SubjectName string
}

// CardChar — one characteristic from card_characteristics.
type CardChar struct {
	CharID int
	Name   string
	Value  string // JSON array like ["text"] or [42]
}

// stagedCard — a card with matched changes, ready for staging table.
type stagedCard struct {
	CardData
	Changes   []changeEntry
	AllChars  []CardChar
	Sizes     []wb.CardSize
}

// changeEntry — one matched replacement for a card.
type changeEntry struct {
	CharID int    `json:"char_id"`
	Old    string `json:"old"`
	New    string `json:"new"`
}

// loadFilteredCards returns cards matching the filter config.
func loadFilteredCards(ctx context.Context, db *sql.DB, f Filters) ([]CardData, error) {
	where, args := buildFilterClause(f)
	query := fmt.Sprintf(`
		SELECT c.nm_id, c.vendor_code, COALESCE(c.title, ''),
		       c.subject_id, COALESCE(c.subject_name, '')
		FROM cards c
		WHERE %s
		ORDER BY c.nm_id
	`, where)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query cards: %w", err)
	}
	defer rows.Close()

	var cards []CardData
	for rows.Next() {
		var c CardData
		if err := rows.Scan(&c.NmID, &c.VendorCode, &c.Title, &c.SubjectID, &c.SubjectName); err != nil {
			return nil, fmt.Errorf("scan card: %w", err)
		}
		cards = append(cards, c)
	}
	return cards, rows.Err()
}

func buildFilterClause(f Filters) (string, []any) {
	var conds []string
	var args []any

	if len(f.SubjectIDs) > 0 {
		ph := placeholders(len(f.SubjectIDs))
		args = append(args, intSliceToAny(f.SubjectIDs)...)
		conds = append(conds, "c.subject_id IN ("+ph+")")
	}
	if len(f.VendorCodes) > 0 {
		ph := placeholders(len(f.VendorCodes))
		args = append(args, stringSliceToAny(f.VendorCodes)...)
		conds = append(conds, "c.vendor_code IN ("+ph+")")
	}
	if len(f.NmIDs) > 0 {
		ph := placeholders(len(f.NmIDs))
		args = append(args, intSliceToAny(f.NmIDs)...)
		conds = append(conds, "c.nm_id IN ("+ph+")")
	}
	if f.VendorCodePrefix != "" {
		conds = append(conds, "SUBSTR(c.vendor_code, 1, 1) = ?")
		args = append(args, f.VendorCodePrefix)
	}
	if len(f.VendorCodeYears) > 0 {
		ph := placeholders(len(f.VendorCodeYears))
		yearStrs := make([]string, len(f.VendorCodeYears))
		for i, y := range f.VendorCodeYears {
			yearStrs[i] = fmt.Sprintf("%02d", y%100)
		}
		args = append(args, stringSliceToAny(yearStrs)...)
		conds = append(conds, "SUBSTR(c.vendor_code, 2, 2) IN ("+ph+")")
	}
	if f.InStock {
		conds = append(conds, `c.nm_id IN (
			SELECT nm_id FROM stocks_daily_warehouses
			WHERE snapshot_date = (SELECT MAX(snapshot_date) FROM stocks_daily_warehouses)
			GROUP BY nm_id HAVING SUM(quantity) > 0
		)`)
	}

	if len(conds) == 0 {
		return "1=1", nil
	}
	return strings.Join(conds, " AND "), args
}

// loadCharacteristics bulk-loads characteristics for given nm_ids.
func loadCharacteristics(ctx context.Context, db *sql.DB, nmIDs []int) (map[int][]CardChar, error) {
	if len(nmIDs) == 0 {
		return nil, nil
	}
	ph := placeholders(len(nmIDs))
	args := intSliceToAny(nmIDs)

	query := fmt.Sprintf(`
		SELECT nm_id, char_id, name, json_value
		FROM card_characteristics
		WHERE nm_id IN (%s)
	`, ph)

	rows, err := db.QueryContext(ctx, query, args...)
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

// loadSizes bulk-loads sizes for given nm_ids.
func loadSizes(ctx context.Context, db *sql.DB, nmIDs []int) (map[int][]wb.CardSize, error) {
	if len(nmIDs) == 0 {
		return nil, nil
	}
	ph := placeholders(len(nmIDs))
	args := intSliceToAny(nmIDs)

	query := fmt.Sprintf(`
		SELECT nm_id, chrt_id, tech_size, wb_size, skus_json
		FROM card_sizes
		WHERE nm_id IN (%s)
	`, ph)

	rows, err := db.QueryContext(ctx, query, args...)
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

// loadCardFields returns brand, title, description, and dimensions for a single card.
func loadCardFields(ctx context.Context, db *sql.DB, nmID int) (brand, title, desc string, dims wb.CardDimensions, err error) {
	var dimValid int
	err = db.QueryRowContext(ctx, `
		SELECT COALESCE(brand,''), COALESCE(title,''), COALESCE(description,''),
		       COALESCE(dim_length,0), COALESCE(dim_width,0), COALESCE(dim_height,0),
		       COALESCE(dim_weight_brutto,0), COALESCE(dim_is_valid,0)
		FROM cards WHERE nm_id = ?
	`, nmID).Scan(&brand, &title, &desc,
		&dims.Length, &dims.Width, &dims.Height, &dims.WeightBrutto, &dimValid)
	dims.IsValid = dimValid != 0
	return
}

// matchRules checks all fix rules against a card's characteristics.
// Returns the list of matched changes. A rule matches when the current value
// equals SearchValue (or when SearchValue is empty and the char is missing/empty).
func matchRules(rules []FixRule, chars []CardChar) []changeEntry {
	charMap := make(map[int]CardChar, len(chars))
	for _, c := range chars {
		charMap[c.CharID] = c
	}

	var changes []changeEntry
	for _, rule := range rules {
		curr, exists := charMap[rule.CharID]
		if matchesRule(rule, curr.Value, exists) {
			old := ""
			if exists {
				old = curr.Value
			}
			changes = append(changes, changeEntry{
				CharID: rule.CharID,
				Old:    old,
				New:    fmtVal(rule.ReplaceValue),
			})
		}
	}
	return changes
}

func matchesRule(rule FixRule, currentJSON string, exists bool) bool {
	if isEmptyMatch(rule.SearchValue) {
		return !exists || currentJSON == "" || currentJSON == "[]" || currentJSON == "null"
	}
	if !exists {
		return false
	}

	switch rule.ValueType {
	case "number":
		return matchNumber(fmtVal(rule.SearchValue), currentJSON)
	case "boolean":
		return matchBool(fmtVal(rule.SearchValue), currentJSON)
	default:
		return matchString(fmtVal(rule.SearchValue), currentJSON)
	}
}

func isEmptyMatch(v any) bool {
	switch val := v.(type) {
	case nil:
		return true
	case string:
		return val == ""
	default:
		return false
	}
}

func matchString(search string, currentJSON string) bool {
	current := unwrapJSONString(currentJSON)
	return strings.EqualFold(strings.TrimSpace(search), strings.TrimSpace(current))
}

func matchNumber(search string, currentJSON string) bool {
	searchF, err := strconv.ParseFloat(search, 64)
	if err != nil {
		return false
	}
	currentF, err := unwrapJSONNumber(currentJSON)
	if err != nil {
		return false
	}
	return searchF == currentF
}

func matchBool(search string, currentJSON string) bool {
	searchB := toBool(search)
	currentB := toBool(unwrapJSONString(currentJSON))
	return searchB == currentB
}

// unwrapJSONString extracts a scalar string from JSON like ["text"] or "text".
// For non-string scalars, converts to string representation.
func unwrapJSONString(s string) string {
	if s == "" || s == "[]" {
		return ""
	}
	var val any
	if err := json.Unmarshal([]byte(s), &val); err != nil {
		return s
	}
	switch v := val.(type) {
	case string:
		return v
	case []any:
		if len(v) == 0 {
			return ""
		}
		if len(v) == 1 {
			switch elem := v[0].(type) {
			case string:
				return elem
			case float64:
				if elem == float64(int(elem)) {
					return strconv.Itoa(int(elem))
				}
				return strconv.FormatFloat(elem, 'f', -1, 64)
			case bool:
				return strconv.FormatBool(elem)
			}
		}
		b, _ := json.Marshal(v)
		return string(b)
	case float64:
		if v == float64(int(v)) {
			return strconv.Itoa(int(v))
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	default:
		return s
	}
}

// unwrapJSONNumber extracts a float64 from JSON like [42] or 42.
func unwrapJSONNumber(s string) (float64, error) {
	var val any
	if err := json.Unmarshal([]byte(s), &val); err != nil {
		return strconv.ParseFloat(s, 64)
	}
	switch v := val.(type) {
	case float64:
		return v, nil
	case []any:
		if len(v) == 1 {
			if n, ok := v[0].(float64); ok {
				return n, nil
			}
		}
	}
	return 0, fmt.Errorf("not a number: %s", s)
}

func toBool(s string) bool {
	lower := strings.ToLower(strings.TrimSpace(s))
	return lower == "true" || lower == "1" || lower == "да"
}

func fmtVal(v any) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case bool:
		return strconv.FormatBool(val)
	case int:
		return strconv.Itoa(val)
	case float64:
		if val == float64(int(val)) {
			return strconv.Itoa(int(val))
		}
		return strconv.FormatFloat(val, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", val)
	}
}

func placeholders(n int) string {
	p := make([]string, n)
	for i := range p {
		p[i] = "?"
	}
	return strings.Join(p, ",")
}

func intSliceToAny(s []int) []any {
	a := make([]any, len(s))
	for i, v := range s {
		a[i] = v
	}
	return a
}

func stringSliceToAny(s []string) []any {
	a := make([]any, len(s))
	for i, v := range s {
		a[i] = v
	}
	return a
}

// FilterNmIDsByYear reuses the logic from config package to filter by year digits.
func FilterNmIDsByYear(entries []config.YearEntry, allowedYears []int) []int {
	return config.FilterNmIDsByYear(entries, allowedYears)
}
