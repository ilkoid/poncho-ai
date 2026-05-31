package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/cards"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: PgCardsRepo implements cards.CardsWriter.
var _ cards.CardsWriter = (*PgCardsRepo)(nil)

// PgCardsRepo implements cards.CardsWriter for PostgreSQL.
// Focused repository (ISP) — only cards persistence methods.
type PgCardsRepo struct {
	pool *pgxpool.Pool
}

// NewPgCardsRepo creates a new PostgreSQL cards repository.
func NewPgCardsRepo(pool *pgxpool.Pool) *PgCardsRepo {
	return &PgCardsRepo{pool: pool}
}

// InitSchema creates cards tables if they don't exist.
func (r *PgCardsRepo) InitSchema(ctx context.Context) error {
	return initCardsSchema(ctx, r.pool)
}

// SaveCards saves a batch of cards with all nested data.
// Uses ON CONFLICT for upsert semantics, DELETE+INSERT for child records
// (within transaction). Chunk size: 500 cards per transaction.
func (r *PgCardsRepo) SaveCards(ctx context.Context, cards []wb.ProductCard) (int, error) {
	if len(cards) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(cards); i += cardsChunkSize {
		end := i + cardsChunkSize
		if end > len(cards) {
			end = len(cards)
		}
		chunk := cards[i:end]

		n, err := r.saveCardsChunk(ctx, chunk)
		if err != nil {
			return 0, fmt.Errorf("save chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

// CountCards returns total number of cards in the database.
func (r *PgCardsRepo) CountCards(ctx context.Context) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, "SELECT count(*) FROM cards").Scan(&count)
	return count, err
}

// saveCardsChunk saves up to 500 cards in a single transaction.
func (r *PgCardsRepo) saveCardsChunk(ctx context.Context, chunk []wb.ProductCard) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, card := range chunk {
		// Extract wholesale fields
		var wholesaleEnabled bool
		var wholesaleQuantum int
		if card.Wholesale != nil {
			wholesaleEnabled = card.Wholesale.Enabled
			wholesaleQuantum = card.Wholesale.Quantum
		}

		// Extract dimensions fields
		var dimLength, dimWidth, dimHeight, dimWeight float64
		var dimIsValid bool
		if card.Dimensions != nil {
			dimLength = card.Dimensions.Length
			dimWidth = card.Dimensions.Width
			dimHeight = card.Dimensions.Height
			dimWeight = card.Dimensions.WeightBrutto
			dimIsValid = card.Dimensions.IsValid
		}

		// Upsert main card (ON CONFLICT DO UPDATE)
		_, err := tx.Exec(ctx, insertCardSQL,
			card.NmID, card.ImtID, card.NmUUID, card.SubjectID, card.SubjectName,
			card.VendorCode, card.Brand, card.Title, card.Description,
			card.NeedKiz, card.Video,
			wholesaleEnabled, wholesaleQuantum,
			dimLength, dimWidth, dimHeight, dimWeight, dimIsValid,
			card.CreatedAt, card.UpdatedAt,
		)
		if err != nil {
			return 0, fmt.Errorf("upsert card nm_id=%d: %w", card.NmID, err)
		}

		// Delete old child records
		_, _ = tx.Exec(ctx, deletePhotosSQL, card.NmID)
		_, _ = tx.Exec(ctx, deleteSizesSQL, card.NmID)
		_, _ = tx.Exec(ctx, deleteCharacteristicsSQL, card.NmID)
		_, _ = tx.Exec(ctx, deleteTagsSQL, card.NmID)

		// Insert photos
		for _, photo := range card.Photos {
			_, err := tx.Exec(ctx, insertPhotoSQL,
				card.NmID, photo.Big, photo.C246x328, photo.C516x688, photo.Square, photo.Tm,
			)
			if err != nil {
				return 0, fmt.Errorf("insert photo nm_id=%d: %w", card.NmID, err)
			}
		}

		// Insert sizes (ON CONFLICT for chrt_id upsert)
		for _, size := range card.Sizes {
			skusJSON, err := json.Marshal(size.Skus)
			if err != nil {
				return 0, fmt.Errorf("marshal skus nm_id=%d: %w", card.NmID, err)
			}
			_, err = tx.Exec(ctx, insertSizeSQL,
				size.ChrtID, card.NmID, size.TechSize, size.WBSize, string(skusJSON),
			)
			if err != nil {
				return 0, fmt.Errorf("insert size nm_id=%d: %w", card.NmID, err)
			}
		}

		// Insert characteristics (ON CONFLICT for nm_id+char_id)
		for _, char := range card.Characteristics {
			valueJSON, err := json.Marshal(char.Values())
			if err != nil {
				return 0, fmt.Errorf("marshal characteristic nm_id=%d: %w", card.NmID, err)
			}
			_, err = tx.Exec(ctx, insertCharacteristicSQL,
				card.NmID, char.ID, char.Name, string(valueJSON),
			)
			if err != nil {
				return 0, fmt.Errorf("insert characteristic nm_id=%d: %w", card.NmID, err)
			}
		}

		// Insert tags (ON CONFLICT for nm_id+tag_id)
		for _, tag := range card.Tags {
			_, err := tx.Exec(ctx, insertTagSQL,
				card.NmID, tag.ID, tag.Name, tag.Color,
			)
			if err != nil {
				return 0, fmt.Errorf("insert tag nm_id=%d: %w", card.NmID, err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return len(chunk), nil
}

const cardsChunkSize = 500

// SQL statements — PostgreSQL syntax ($1, $2, ... placeholders).
// ON CONFLICT ... DO UPDATE replaces SQLite's INSERT OR REPLACE.
var (
	// Main card upsert — update all fields on conflict
	insertCardSQL = `
INSERT INTO cards (
    nm_id, imt_id, nm_uuid, subject_id, subject_name,
    vendor_code, brand, title, description,
    need_kiz, video,
    wholesale_enabled, wholesale_quantum,
    dim_length, dim_width, dim_height, dim_weight_brutto, dim_is_valid,
    created_at, updated_at, downloaded_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'))
ON CONFLICT (nm_id) DO UPDATE SET
    imt_id = EXCLUDED.imt_id,
    nm_uuid = EXCLUDED.nm_uuid,
    subject_id = EXCLUDED.subject_id,
    subject_name = EXCLUDED.subject_name,
    vendor_code = EXCLUDED.vendor_code,
    brand = EXCLUDED.brand,
    title = EXCLUDED.title,
    description = EXCLUDED.description,
    need_kiz = EXCLUDED.need_kiz,
    video = EXCLUDED.video,
    wholesale_enabled = EXCLUDED.wholesale_enabled,
    wholesale_quantum = EXCLUDED.wholesale_quantum,
    dim_length = EXCLUDED.dim_length,
    dim_width = EXCLUDED.dim_width,
    dim_height = EXCLUDED.dim_height,
    dim_weight_brutto = EXCLUDED.dim_weight_brutto,
    dim_is_valid = EXCLUDED.dim_is_valid,
    created_at = EXCLUDED.created_at,
    updated_at = EXCLUDED.updated_at,
    downloaded_at = EXCLUDED.downloaded_at`

	// Child record inserts (DELETE+INSERT pattern for atomicity)
	insertPhotoSQL = `
INSERT INTO card_photos (nm_id, big, c246x328, c516x688, square, tm)
VALUES ($1,$2,$3,$4,$5,$6)`

	insertSizeSQL = `
INSERT INTO card_sizes (chrt_id, nm_id, tech_size, wb_size, skus_json)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (chrt_id) DO UPDATE SET
    nm_id = EXCLUDED.nm_id,
    tech_size = EXCLUDED.tech_size,
    wb_size = EXCLUDED.wb_size,
    skus_json = EXCLUDED.skus_json`

	insertCharacteristicSQL = `
INSERT INTO card_characteristics (nm_id, char_id, name, json_value)
VALUES ($1,$2,$3,$4)
ON CONFLICT (nm_id, char_id) DO UPDATE SET
    name = EXCLUDED.name,
    json_value = EXCLUDED.json_value`

	insertTagSQL = `
INSERT INTO card_tags (nm_id, tag_id, name, color)
VALUES ($1,$2,$3,$4)
ON CONFLICT (nm_id, tag_id) DO UPDATE SET
    name = EXCLUDED.name,
    color = EXCLUDED.color`

	// Delete child records before re-insert
	deletePhotosSQL          = `DELETE FROM card_photos WHERE nm_id = $1`
	deleteSizesSQL           = `DELETE FROM card_sizes WHERE nm_id = $1`
	deleteCharacteristicsSQL = `DELETE FROM card_characteristics WHERE nm_id = $1`
	deleteTagsSQL            = `DELETE FROM card_tags WHERE nm_id = $1`
)

// Ensure pgx.Tx satisfies our needs (used in saveCardsChunk).
var _ pgx.Tx = (pgx.Tx)(nil)
