package sqlite

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

const (
	// Main card insert
	insertCardSQL = `
INSERT OR REPLACE INTO cards (
	nm_id, imt_id, nm_uuid, subject_id, subject_name,
	vendor_code, brand, title, description,
	need_kiz, video,
	wholesale_enabled, wholesale_quantum,
	dim_length, dim_width, dim_height, dim_weight_brutto, dim_is_valid,
	created_at, updated_at, downloaded_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP)`

	// Photo insert
	insertPhotoSQL = `
INSERT INTO card_photos (nm_id, big, c246x328, c516x688, square, tm)
VALUES (?,?,?,?,?,?)`

	// Size insert
	insertSizeSQL = `
INSERT OR REPLACE INTO card_sizes (chrt_id, nm_id, tech_size, wb_size, skus_json)
VALUES (?,?,?,?,?)`

	// Characteristic insert
	insertCharacteristicSQL = `
INSERT INTO card_characteristics (nm_id, char_id, name, json_value)
VALUES (?,?,?,?)`

	// Tag insert
	insertTagSQL = `
INSERT INTO card_tags (nm_id, tag_id, name, color)
VALUES (?,?,?,?)`

	// Delete child records for a card (before re-insert)
	deletePhotosSQL = `DELETE FROM card_photos WHERE nm_id = ?`
	deleteSizesSQL = `DELETE FROM card_sizes WHERE nm_id = ?`
	deleteCharacteristicsSQL = `DELETE FROM card_characteristics WHERE nm_id = ?`
	deleteTagsSQL = `DELETE FROM card_tags WHERE nm_id = ?`

	// Cursor persistence
	insertMetaSQL = `INSERT OR REPLACE INTO cards_download_meta (key, value) VALUES (?,?)`
	getMetaSQL = `SELECT value FROM cards_download_meta WHERE key = ?`
)

const cardsChunkSize = 500 // Conservative: each card has many child records

// boolToInt converts boolean to integer for SQLite storage.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SaveCards saves a batch of cards with all nested data.
// Uses INSERT OR REPLACE for cards, DELETE+INSERT for children (within transaction).
// Chunk size: 500 cards per transaction.
// Returns count of inserted cards.
func (r *SQLiteSalesRepository) SaveCards(ctx context.Context, cards []wb.ProductCard) (int, error) {
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

// saveCardsChunk saves up to 500 cards in a single transaction.
// For each card: INSERT OR REPLACE main card, DELETE+INSERT all child records.
func (r *SQLiteSalesRepository) saveCardsChunk(ctx context.Context, chunk []wb.ProductCard) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Prepare all statements
	cardStmt, err := tx.PrepareContext(ctx, insertCardSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare card statement: %w", err)
	}
	defer cardStmt.Close()

	photoStmt, err := tx.PrepareContext(ctx, insertPhotoSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare photo statement: %w", err)
	}
	defer photoStmt.Close()

	sizeStmt, err := tx.PrepareContext(ctx, insertSizeSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare size statement: %w", err)
	}
	defer sizeStmt.Close()

	charStmt, err := tx.PrepareContext(ctx, insertCharacteristicSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare characteristic statement: %w", err)
	}
	defer charStmt.Close()

	tagStmt, err := tx.PrepareContext(ctx, insertTagSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare tag statement: %w", err)
	}
	defer tagStmt.Close()

	// Delete statements
	deletePhotosStmt, err := tx.PrepareContext(ctx, deletePhotosSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare delete photos statement: %w", err)
	}
	defer deletePhotosStmt.Close()

	deleteSizesStmt, err := tx.PrepareContext(ctx, deleteSizesSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare delete sizes statement: %w", err)
	}
	defer deleteSizesStmt.Close()

	deleteCharsStmt, err := tx.PrepareContext(ctx, deleteCharacteristicsSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare delete characteristics statement: %w", err)
	}
	defer deleteCharsStmt.Close()

	deleteTagsStmt, err := tx.PrepareContext(ctx, deleteTagsSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare delete tags statement: %w", err)
	}
	defer deleteTagsStmt.Close()

	for _, card := range chunk {
		// Extract wholesale fields (nullable pointer)
		wholesaleEnabled := 0
		wholesaleQuantum := 0
		if card.Wholesale != nil {
			wholesaleEnabled = boolToInt(card.Wholesale.Enabled)
			wholesaleQuantum = card.Wholesale.Quantum
		}

		// Extract dimensions fields (nullable pointer)
		dimLength, dimWidth, dimHeight, dimWeight := 0.0, 0.0, 0.0, 0.0
		dimIsValid := 0
		if card.Dimensions != nil {
			dimLength = card.Dimensions.Length
			dimWidth = card.Dimensions.Width
			dimHeight = card.Dimensions.Height
			dimWeight = card.Dimensions.WeightBrutto
			dimIsValid = boolToInt(card.Dimensions.IsValid)
		}

		// Insert main card
		_, err := cardStmt.ExecContext(ctx,
			card.NmID, card.ImtID, card.NmUUID, card.SubjectID, card.SubjectName,
			card.VendorCode, card.Brand, card.Title, card.Description,
			boolToInt(card.NeedKiz), card.Video,
			wholesaleEnabled, wholesaleQuantum,
			dimLength, dimWidth, dimHeight, dimWeight, dimIsValid,
			card.CreatedAt, card.UpdatedAt,
		)
		if err != nil {
			return 0, fmt.Errorf("insert card nm_id=%d: %w", card.NmID, err)
		}

		// Delete old child records for this card
		deletePhotosStmt.ExecContext(ctx, card.NmID)
		deleteSizesStmt.ExecContext(ctx, card.NmID)
		deleteCharsStmt.ExecContext(ctx, card.NmID)
		deleteTagsStmt.ExecContext(ctx, card.NmID)

		// Insert photos
		for _, photo := range card.Photos {
			_, err := photoStmt.ExecContext(ctx,
				card.NmID, photo.Big, photo.C246x328, photo.C516x688, photo.Square, photo.Tm,
			)
			if err != nil {
				return 0, fmt.Errorf("insert photo nm_id=%d: %w", card.NmID, err)
			}
		}

		// Insert sizes
		for _, size := range card.Sizes {
			skusJSON, err := json.Marshal(size.Skus)
			if err != nil {
				return 0, fmt.Errorf("marshal skus nm_id=%d: %w", card.NmID, err)
			}
			_, err = sizeStmt.ExecContext(ctx,
				size.ChrtID, card.NmID, size.TechSize, size.WBSize, string(skusJSON),
			)
			if err != nil {
				return 0, fmt.Errorf("insert size nm_id=%d: %w", card.NmID, err)
			}
		}

		// Insert characteristics
		for _, char := range card.Characteristics {
			valueJSON, err := json.Marshal(char.Values())
			if err != nil {
				return 0, fmt.Errorf("marshal characteristic value nm_id=%d: %w", card.NmID, err)
			}
			_, err = charStmt.ExecContext(ctx,
				card.NmID, char.ID, char.Name, string(valueJSON),
			)
			if err != nil {
				return 0, fmt.Errorf("insert characteristic nm_id=%d: %w", card.NmID, err)
			}
		}

		// Insert tags
		for _, tag := range card.Tags {
			_, err := tagStmt.ExecContext(ctx,
				card.NmID, tag.ID, tag.Name, tag.Color,
			)
			if err != nil {
				return 0, fmt.Errorf("insert tag nm_id=%d: %w", card.NmID, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return len(chunk), nil
}

// CountCards returns total number of cards in the database.
func (r *SQLiteSalesRepository) CountCards(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT count(*) FROM cards").Scan(&count)
	return count, err
}

// GetCardsLastCursor retrieves the last saved cursor for resume functionality.
// Returns updatedAt timestamp and nmID from the last page.
// Returns zero values if no cursor is saved (database is empty or first run).
func (r *SQLiteSalesRepository) GetCardsLastCursor(ctx context.Context) (updatedAt string, nmID int, err error) {
	var value string
	err = r.db.QueryRowContext(ctx, getMetaSQL, "last_cursor").Scan(&value)
	if err != nil {
		// No cursor saved yet (first run)
		return "", 0, nil
	}

	// Parse JSON: {"updated_at":"2024-01-01T00:00:00Z","nm_id":123456}
	var cursor struct {
		UpdatedAt string `json:"updated_at"`
		NmID      int    `json:"nm_id"`
	}
	if err := json.Unmarshal([]byte(value), &cursor); err != nil {
		return "", 0, fmt.Errorf("parse cursor JSON: %w", err)
	}

	return cursor.UpdatedAt, cursor.NmID, nil
}

// SaveCardsCursor saves the current cursor position for resume functionality.
// Stores updatedAt timestamp and nmID in metadata table.
func (r *SQLiteSalesRepository) SaveCardsCursor(ctx context.Context, updatedAt string, nmID int) error {
	cursor := struct {
		UpdatedAt string `json:"updated_at"`
		NmID      int    `json:"nm_id"`
	}{
		UpdatedAt: updatedAt,
		NmID:      nmID,
	}

	valueJSON, err := json.Marshal(cursor)
	if err != nil {
		return fmt.Errorf("marshal cursor: %w", err)
	}

	_, err = r.db.ExecContext(ctx, insertMetaSQL, "last_cursor", string(valueJSON))
	return err
}
