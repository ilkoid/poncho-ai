package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// CardChar mirrors a row from card_characteristics.
type CardChar struct {
	CharID int    `json:"char_id"`
	Name   string `json:"name"`
	Value  string `json:"json_value"`
}

// loadCardFields loads brand, title, description and dimensions for a single card.
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

// loadCharacteristics loads all characteristics for multiple cards.
// Returns map[nmID][]CardChar.
func loadCharacteristics(ctx context.Context, db *sql.DB, nmIDs []int) (map[int][]CardChar, error) {
	if len(nmIDs) == 0 {
		return nil, nil
	}

	placeholders := strings.Repeat("?,", len(nmIDs))
	placeholders = placeholders[:len(placeholders)-1]

	args := make([]any, len(nmIDs))
	for i, id := range nmIDs {
		args[i] = id
	}

	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT nm_id, char_id, COALESCE(name,''), json_value
		FROM card_characteristics
		WHERE nm_id IN (%s)
	`, placeholders), args...)
	if err != nil {
		return nil, fmt.Errorf("query characteristics: %w", err)
	}
	defer rows.Close()

	result := make(map[int][]CardChar)
	for rows.Next() {
		var nmID int
		var ch CardChar
		if err := rows.Scan(&nmID, &ch.CharID, &ch.Name, &ch.Value); err != nil {
			return nil, fmt.Errorf("scan char: %w", err)
		}
		result[nmID] = append(result[nmID], ch)
	}
	return result, rows.Err()
}

// loadSizes loads all sizes for multiple cards.
// Returns map[nmID][]wb.CardSize.
func loadSizes(ctx context.Context, db *sql.DB, nmIDs []int) (map[int][]wb.CardSize, error) {
	if len(nmIDs) == 0 {
		return nil, nil
	}

	placeholders := strings.Repeat("?,", len(nmIDs))
	placeholders = placeholders[:len(placeholders)-1]

	args := make([]any, len(nmIDs))
	for i, id := range nmIDs {
		args[i] = id
	}

	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT nm_id, chrt_id, tech_size, wb_size, skus_json
		FROM card_sizes
		WHERE nm_id IN (%s)
	`, placeholders), args...)
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
