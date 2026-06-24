// PostgreSQL counterpart of the SQLite CardUpdater in cardupdate.go.
//
// WB API fully overwrites cards (POST /content/v2/cards/update), so every card
// fixer must load ALL fields before building the update payload. PgCardsRepo is
// write-only (no read method), so the safe full-card loader for PostgreSQL lives
// here — the package that owns the invariant — instead of being duplicated in
// each PG fixer. Returns the same FullCardData/CardChar types, so the existing
// backend-agnostic ToUpdateItem works unchanged.
package cardupdate

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// PGCardUpdater loads full card data from PostgreSQL for safe WB updates.
type PGCardUpdater struct {
	pool *pgxpool.Pool
}

// NewPGCardUpdater creates a PG-backed full-card loader.
func NewPGCardUpdater(pool *pgxpool.Pool) *PGCardUpdater {
	return &PGCardUpdater{pool: pool}
}

// LoadFullCard loads all card data from PostgreSQL for a safe WB update.
// Mirrors CardUpdater.LoadFullCard: core fields + characteristics + sizes.
// Uses $1 placeholders and scans dim_is_valid as bool (BOOLEAN in PG).
//
// MUST load child tables too — UpdateCards overwrites characteristics and sizes
// in full, so omitting them would wipe those fields.
func (u *PGCardUpdater) LoadFullCard(ctx context.Context, nmID int) (FullCardData, error) {
	var card FullCardData

	err := u.pool.QueryRow(ctx, `
		SELECT nm_id, COALESCE(vendor_code,''), COALESCE(brand,''),
		       COALESCE(title,''), COALESCE(description,''),
		       COALESCE(dim_length,0), COALESCE(dim_width,0),
		       COALESCE(dim_height,0), COALESCE(dim_weight_brutto,0),
		       COALESCE(dim_is_valid,false)
		FROM cards WHERE nm_id = $1
	`, nmID).Scan(
		&card.NmID, &card.VendorCode, &card.Brand,
		&card.Title, &card.Description,
		&card.Dimensions.Length, &card.Dimensions.Width,
		&card.Dimensions.Height, &card.Dimensions.WeightBrutto,
		&card.Dimensions.IsValid,
	)
	if err != nil {
		return FullCardData{}, fmt.Errorf("load card core for nm_id=%d: %w", nmID, err)
	}

	chars, err := u.loadCharacteristics(ctx, nmID)
	if err != nil {
		return FullCardData{}, fmt.Errorf("load chars: %w", err)
	}
	card.Characteristics = chars

	sizes, err := u.loadSizes(ctx, nmID)
	if err != nil {
		return FullCardData{}, fmt.Errorf("load sizes: %w", err)
	}
	card.Sizes = sizes

	return card, nil
}

func (u *PGCardUpdater) loadCharacteristics(ctx context.Context, nmID int) ([]CardChar, error) {
	rows, err := u.pool.Query(ctx, `
		SELECT char_id, COALESCE(name,''), json_value FROM card_characteristics WHERE nm_id = $1
	`, nmID)
	if err != nil {
		return nil, fmt.Errorf("query chars: %w", err)
	}
	defer rows.Close()

	var chars []CardChar
	for rows.Next() {
		var c CardChar
		if err := rows.Scan(&c.CharID, &c.Name, &c.Value); err != nil {
			return nil, fmt.Errorf("scan char: %w", err)
		}
		chars = append(chars, c)
	}
	return chars, rows.Err()
}

func (u *PGCardUpdater) loadSizes(ctx context.Context, nmID int) ([]wb.CardSize, error) {
	rows, err := u.pool.Query(ctx, `
		SELECT chrt_id, tech_size, wb_size, skus_json FROM card_sizes WHERE nm_id = $1
	`, nmID)
	if err != nil {
		return nil, fmt.Errorf("query sizes: %w", err)
	}
	defer rows.Close()

	var sizes []wb.CardSize
	for rows.Next() {
		var s wb.CardSize
		var skusJSON string
		if err := rows.Scan(&s.ChrtID, &s.TechSize, &s.WBSize, &skusJSON); err != nil {
			return nil, fmt.Errorf("scan size: %w", err)
		}
		if err := json.Unmarshal([]byte(skusJSON), &s.Skus); err != nil {
			s.Skus = nil
		}
		sizes = append(sizes, s)
	}
	return sizes, rows.Err()
}
